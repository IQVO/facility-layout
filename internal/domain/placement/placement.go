// Package placement holds LocationType (a reusable classification of slot
// shape/kind, with a default capacity envelope) and PlacementRule (which
// LocationTypes are legal in which Zones). Together they are the mechanism
// that stops "ambient product in the frozen zone": the constraint is
// declared once and enforced at slot-registration time, rather than
// re-checked by every caller.
package placement

import (
	"errors"
	"fmt"
	"strings"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

var (
	// ErrEmptyLocationTypeName is returned when a location type has no name.
	ErrEmptyLocationTypeName = errors.New("location type name must not be empty")
	// ErrEmptyRuleID is returned when a placement rule has no id.
	ErrEmptyRuleID = errors.New("placement rule id must not be empty")
	// ErrEmptyRuleLocationType is returned when a rule references no location type.
	ErrEmptyRuleLocationType = errors.New("placement rule must reference a location type")
	// ErrUnknownEffect is returned when a rule's effect is neither Allow nor Deny.
	ErrUnknownEffect = errors.New("placement rule effect must be one of Allow, Deny")
	// ErrEmptyPredicate is returned when a rule's zone predicate matches
	// nothing at all — a rule that constrains no zone is meaningless and is
	// almost always a typo in the rule definition.
	ErrEmptyPredicate = errors.New("placement rule predicate must constrain at least one of zoneCode, temperatureClass, hazmat")
)

// Known, well-established location types from the domain reference. They
// are not an enum — RegisterLocationType accepts any name — but these are
// the ubiquitous-language ones the fleet already speaks.
const (
	// PalletRack is racked pallet storage.
	PalletRack = "PalletRack"
	// Shelf is small-item shelving.
	Shelf = "Shelf"
	// ToteWall is a wall of totes.
	ToteWall = "ToteWall"
	// BulkFloor is floor-stacked bulk storage.
	BulkFloor = "BulkFloor"
	// Staging is a staging position (outbound/inbound holding).
	Staging = "Staging"
	// Amnesty is where a damaged or mismatched item is set aside during stow.
	Amnesty = "Amnesty"
)

// Effect is what a PlacementRule does when its predicate matches a zone.
type Effect string

const (
	// Allow declares its LocationType legal in matching zones. Declaring ANY
	// Allow rule for a zone makes that zone an allow-list: a LocationType not
	// named by some matching Allow rule is then rejected there.
	Allow Effect = "Allow"
	// Deny declares its LocationType illegal in matching zones. Deny always
	// wins over Allow.
	Deny Effect = "Deny"
)

// ParseEffect validates and converts the string form.
func ParseEffect(value string) (Effect, error) {
	switch Effect(value) {
	case Allow, Deny:
		return Effect(value), nil
	default:
		return "", ErrUnknownEffect
	}
}

// LocationType is a reusable classification of physical slot shape/kind,
// carrying the default capacity envelope slots of that kind get unless they
// override it, and the LocationRole that says what the location is FOR
// (ADR-0016).
type LocationType struct {
	name            string
	role            LocationRole
	defaultCapacity shared.Capacity
}

// NewLocationType validates and constructs a LocationType. defaultCapacity
// must be strictly positive when role.RequiresCapacity() (Storage,
// RoleStaging, Drop, Consolidation); it may be the zero Capacity for a role
// that does not hold inventory the way storage does (Dock, Yard,
// WorkCenter, QC, Shipping). A well-known name (AutomatedStorage, AsrsPort,
// HandoffPoint) must carry its canonical role (ADR-0048).
func NewLocationType(name string, role LocationRole, defaultCapacity shared.Capacity) (LocationType, error) {
	if name == "" {
		return LocationType{}, ErrEmptyLocationTypeName
	}
	if _, err := ParseLocationRole(string(role)); err != nil {
		return LocationType{}, err
	}
	if want, ok := CanonicalRole(name); ok && role != want {
		return LocationType{}, fmt.Errorf("%w: %s must be %s, got %s", ErrLocationTypeRoleMismatch, name, want, role)
	}
	if role.RequiresCapacity() && defaultCapacity.IsZero() {
		return LocationType{}, shared.ErrInvalidMaxWeight
	}
	return LocationType{name: name, role: role, defaultCapacity: defaultCapacity}, nil
}

// RehydrateLocationType rebuilds a LocationType from persisted state.
func RehydrateLocationType(name string, role LocationRole, defaultCapacity shared.Capacity) LocationType {
	return LocationType{name: name, role: role, defaultCapacity: defaultCapacity}
}

// Name returns the location type's name, e.g. "PalletRack".
func (t LocationType) Name() string { return t.name }

// Role returns what this location type is for — Storage by convention for
// every type registered before ADR-0016.
func (t LocationType) Role() LocationRole { return t.role }

// DefaultCapacity returns the envelope slots of this type get by default.
// It is the zero Capacity for a role that does not require one.
func (t LocationType) DefaultCapacity() shared.Capacity { return t.defaultCapacity }

// ZoneAttributes is the subset of a Zone a PlacementRule matches on. The
// use case builds it from the Zone aggregate; this package deliberately
// does not import the zone package, so the two aggregates stay independent.
type ZoneAttributes struct {
	ZoneID           string
	ZoneCode         string
	TemperatureClass shared.TemperatureClass
	Hazmat           bool
}

// ZonePredicate selects the zones a PlacementRule applies to. Every set
// field must match for the predicate to match (AND semantics); unset fields
// are wildcards. At least one field must be set.
type ZonePredicate struct {
	zoneCode         string
	temperatureClass shared.TemperatureClass
	hazmat           *bool
}

// NewZonePredicate builds a predicate. Pass "" for zoneCode or
// temperatureClass and nil for hazmat to leave that dimension unconstrained.
func NewZonePredicate(zoneCode string, temperatureClass shared.TemperatureClass, hazmat *bool) (ZonePredicate, error) {
	if zoneCode == "" && temperatureClass == "" && hazmat == nil {
		return ZonePredicate{}, ErrEmptyPredicate
	}
	if temperatureClass != "" {
		if _, err := shared.ParseTemperatureClass(string(temperatureClass)); err != nil {
			return ZonePredicate{}, err
		}
	}
	return ZonePredicate{zoneCode: zoneCode, temperatureClass: temperatureClass, hazmat: hazmat}, nil
}

// ZoneCode returns the zone code this predicate is pinned to, or "".
func (p ZonePredicate) ZoneCode() string { return p.zoneCode }

// TemperatureClass returns the temperature class this predicate is pinned
// to, or "".
func (p ZonePredicate) TemperatureClass() shared.TemperatureClass { return p.temperatureClass }

// Hazmat returns the hazmat flag this predicate is pinned to, or nil.
func (p ZonePredicate) Hazmat() *bool { return p.hazmat }

// Matches reports whether the predicate selects the given zone.
func (p ZonePredicate) Matches(attrs ZoneAttributes) bool {
	if p.zoneCode != "" && p.zoneCode != attrs.ZoneCode {
		return false
	}
	if p.temperatureClass != "" && p.temperatureClass != attrs.TemperatureClass {
		return false
	}
	if p.hazmat != nil && *p.hazmat != attrs.Hazmat {
		return false
	}
	return true
}

// String renders the predicate for error messages and event payloads.
func (p ZonePredicate) String() string {
	parts := make([]string, 0, 3)
	if p.zoneCode != "" {
		parts = append(parts, "zoneCode="+p.zoneCode)
	}
	if p.temperatureClass != "" {
		parts = append(parts, "temperatureClass="+string(p.temperatureClass))
	}
	if p.hazmat != nil {
		parts = append(parts, fmt.Sprintf("hazmat=%t", *p.hazmat))
	}
	return strings.Join(parts, ",")
}

// PlacementRule declares that a LocationType is (Allow) or is not (Deny)
// legal in every Zone its predicate matches.
type PlacementRule struct {
	id           string
	locationType string
	effect       Effect
	predicate    ZonePredicate
}

// NewPlacementRule validates and constructs a PlacementRule.
func NewPlacementRule(id, locationType string, effect Effect, predicate ZonePredicate) (PlacementRule, error) {
	if id == "" {
		return PlacementRule{}, ErrEmptyRuleID
	}
	if locationType == "" {
		return PlacementRule{}, ErrEmptyRuleLocationType
	}
	if _, err := ParseEffect(string(effect)); err != nil {
		return PlacementRule{}, err
	}
	if predicate.String() == "" {
		return PlacementRule{}, ErrEmptyPredicate
	}
	return PlacementRule{id: id, locationType: locationType, effect: effect, predicate: predicate}, nil
}

// RehydratePlacementRule rebuilds a PlacementRule from persisted state.
func RehydratePlacementRule(id, locationType string, effect Effect, predicate ZonePredicate) PlacementRule {
	return PlacementRule{id: id, locationType: locationType, effect: effect, predicate: predicate}
}

// ID returns the rule's identity.
func (r PlacementRule) ID() string { return r.id }

// LocationType returns the name of the LocationType this rule constrains.
func (r PlacementRule) LocationType() string { return r.locationType }

// Effect returns Allow or Deny.
func (r PlacementRule) Effect() Effect { return r.effect }

// Predicate returns the zone-matching predicate.
func (r PlacementRule) Predicate() ZonePredicate { return r.predicate }

// Describe renders the rule for the domain error raised when it is violated.
func (r PlacementRule) Describe() string {
	return fmt.Sprintf("%s: %s %s where %s", r.id, r.effect, r.locationType, r.predicate.String())
}
