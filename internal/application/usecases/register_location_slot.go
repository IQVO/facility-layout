package usecases

import (
	"context"
	"errors"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/slot"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

// The outcomes a slot registration attempt is counted under. They are the
// attribute values of the ports.LocationMetrics counter, so they are part
// of what this service publishes about itself and are named here, in the
// layer that decides them, rather than in the telemetry adapter.
const (
	// OutcomeAccepted means the slot now exists on the warehouse map.
	OutcomeAccepted = "accepted"
	// OutcomeRejectedByPlacementRule means the chain of custody resolved
	// but a PlacementRule forbids this LocationType in that zone — the
	// invariant this service exists to enforce, worth its own attribute.
	OutcomeRejectedByPlacementRule = "rejected_by_placement_rule"
	// OutcomeRejected covers every other refusal: unknown or inactive
	// parent, duplicate code, unknown location type, invalid capacity.
	OutcomeRejected = "rejected"
)

// RegisterLocationSlot creates one coded leaf slot. This is the core use
// case of the whole service: registering a slot is a chain-of-custody
// check, not a bare insert. The Site -> Zone -> Aisle chain implied by the
// LocationCode must resolve to existing, Active aggregates, and the slot's
// LocationType must satisfy every PlacementRule applicable to its Zone.
type RegisterLocationSlot struct {
	Sites         ports.SiteRepo
	Zones         ports.ZoneRepo
	Aisles        ports.AisleRepo
	Slots         ports.SlotRepo
	LocationTypes ports.LocationTypeRepo
	Rules         ports.PlacementRuleRepo
	Events        ports.EventPublisher
	Clock         ports.Clock
	// Metrics is optional: when nil, registrations are simply not counted.
	// The use case's decisions are identical either way.
	Metrics ports.LocationMetrics
	// UnitOfWork brackets Save + Publish atomically (ADR-0018, optional).
	UnitOfWork ports.UnitOfWork
}

// Execute registers the slot and publishes LocationSlotRegistered.
// capacityOverride may be the zero Capacity, meaning "use the
// LocationType's default envelope". dockFlow/activities are the raw
// request strings (ADR-0016); this use case resolves the LocationType
// first and then builds/validates FunctionalAttributes against its Role,
// so a caller never needs a separate lookup just to know which shape to
// build — and every attempt, including one that fails at that step, still
// goes through the same metrics recording below.
//
// Every attempt — accepted or refused — is counted against the metrics
// port before the result is returned.
func (uc *RegisterLocationSlot) Execute(ctx context.Context, code shared.LocationCode, locationTypeName string, capacityOverride shared.Capacity, dockFlow string, activityNames []string) (*slot.LocationSlot, error) {
	registered, err := uc.register(ctx, code, locationTypeName, capacityOverride, dockFlow, activityNames)
	if uc.Metrics != nil {
		uc.Metrics.LocationSlotRegistered(ctx, registrationOutcome(err))
	}
	return registered, err
}

// registrationOutcome names the metric attribute a registration attempt is
// counted under.
func registrationOutcome(err error) string {
	switch {
	case err == nil:
		return OutcomeAccepted
	case errors.Is(err, placement.ErrPlacementRuleViolated):
		return OutcomeRejectedByPlacementRule
	default:
		return OutcomeRejected
	}
}

// register is Execute's decision logic, kept separate so the outcome of the
// whole attempt can be observed in one place.
func (uc *RegisterLocationSlot) register(ctx context.Context, code shared.LocationCode, locationTypeName string, capacityOverride shared.Capacity, dockFlow string, activityNames []string) (*slot.LocationSlot, error) {
	existing, err := uc.Slots.FindByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrDuplicateLocationCode
	}

	parentZone, err := uc.resolveChain(ctx, code)
	if err != nil {
		return nil, err
	}

	locationType, err := uc.LocationTypes.FindByName(ctx, locationTypeName)
	if err != nil {
		return nil, err
	}
	if locationType == nil {
		return nil, ErrLocationTypeNotFound
	}

	functional, err := functionalAttributesFor(locationType.Role(), dockFlow, activityNames)
	if err != nil {
		return nil, err
	}

	rules, err := uc.Rules.List(ctx)
	if err != nil {
		return nil, err
	}

	s, err := slot.NewLocationSlot(code, *locationType, capacityOverride, functional, zoneAttributes(parentZone), rules)
	if err != nil {
		return nil, err
	}
	err = atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
		if err := uc.Slots.Save(ctx, s); err != nil {
			return err
		}
		f := s.Functional()
		event := shared.NewLocationSlotRegistered(
			uc.Clock.Now(), s.Code(), s.LocationType(), string(s.Role()),
			string(f.DockFlow()), activityStrings(f.Activities()), s.Capacity(),
		)
		return uc.Events.Publish(ctx, event)
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// functionalAttributesFor parses the raw dockFlow/activity strings and
// builds validated FunctionalAttributes for role, so a malformed or
// role-inappropriate value is rejected (and counted) before any repository
// write.
func functionalAttributesFor(role placement.LocationRole, dockFlow string, activityNames []string) (slot.FunctionalAttributes, error) {
	var flow slot.DockFlow
	if dockFlow != "" {
		parsed, err := slot.ParseDockFlow(dockFlow)
		if err != nil {
			return slot.FunctionalAttributes{}, err
		}
		flow = parsed
	}
	activities := make([]slot.Activity, 0, len(activityNames))
	for _, name := range activityNames {
		a, err := slot.ParseActivity(name)
		if err != nil {
			return slot.FunctionalAttributes{}, err
		}
		activities = append(activities, a)
	}
	return slot.NewFunctionalAttributes(role, flow, activities)
}

// activityStrings converts a slot's Activity set to plain strings for the
// event payload, or nil when there are none — keeping the "activities"
// field omitted entirely for every non-WorkCenter slot.
func activityStrings(activities []slot.Activity) []string {
	if len(activities) == 0 {
		return nil
	}
	out := make([]string, 0, len(activities))
	for _, a := range activities {
		out = append(out, string(a))
	}
	return out
}

// resolveChain walks the LocationCode's Site/Area/Zone/Aisle segments and
// proves every link exists and is Active. No orphan slots, ever.
func (uc *RegisterLocationSlot) resolveChain(ctx context.Context, code shared.LocationCode) (*zone.Zone, error) {
	parentSite, err := uc.Sites.FindByCode(ctx, code.Site())
	if err != nil {
		return nil, err
	}
	if parentSite == nil {
		return nil, ErrSiteNotFound
	}
	if !parentSite.IsActive() {
		return nil, ErrSiteNotActive
	}

	parentZone, err := uc.Zones.FindByID(ctx, code.ZoneID())
	if err != nil {
		return nil, err
	}
	if parentZone == nil {
		return nil, ErrZoneNotFound
	}
	if !parentZone.IsActive() {
		return nil, ErrZoneNotActive
	}

	parentAisle, err := uc.Aisles.FindByID(ctx, code.AisleID())
	if err != nil {
		return nil, err
	}
	if parentAisle == nil {
		return nil, ErrAisleNotFound
	}
	if !parentAisle.IsActive() {
		return nil, ErrAisleNotActive
	}

	return parentZone, nil
}

// zoneAttributes projects a Zone aggregate onto the subset a PlacementRule
// matches on. It lives here, in the application layer, so the placement and
// zone packages never need to know about each other.
func zoneAttributes(z *zone.Zone) placement.ZoneAttributes {
	return placement.ZoneAttributes{
		ZoneID:           z.ID(),
		ZoneCode:         z.ZoneCode(),
		TemperatureClass: z.TemperatureClass(),
		Hazmat:           z.Hazmat(),
	}
}

// GetLocationSlot reads one LocationSlot by its code.
type GetLocationSlot struct {
	Slots ports.SlotRepo
}

// Execute returns the slot, or ErrLocationSlotNotFound.
func (uc *GetLocationSlot) Execute(ctx context.Context, code shared.LocationCode) (*slot.LocationSlot, error) {
	s, err := uc.Slots.FindByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrLocationSlotNotFound
	}
	return s, nil
}
