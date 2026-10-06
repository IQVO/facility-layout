// Package slot holds the LocationSlot aggregate: the coded leaf location.
// Its identity IS its LocationCode. Registering one is a chain-of-custody
// check, not a bare insert — the Site/Zone/Aisle chain is resolved by the
// use case, and the applicable PlacementRules are handed to the constructor
// here, so the aggregate validates itself without reaching outside itself.
package slot

import (
	"errors"

	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

var (
	// ErrMissingLocationCode is returned when a slot is built without a code.
	ErrMissingLocationCode = errors.New("location slot requires a location code")
	// ErrMissingLocationType is returned when a slot is built without a type.
	ErrMissingLocationType = errors.New("location slot requires a location type")
	// ErrZoneMismatch is returned when the zone attributes handed to the
	// constructor do not describe the zone the location code resolves to —
	// a programming error in the use case, caught here rather than silently
	// validating against the wrong zone's rules.
	ErrZoneMismatch = errors.New("zone attributes do not match the location code's zone")
	// ErrAlreadyDecommissioned is returned when decommissioning a slot that
	// is already decommissioned. Decommission is one-way in v1: a retired
	// code is never reactivated, and re-registering it is rejected as a
	// duplicate.
	ErrAlreadyDecommissioned = errors.New("location slot is already decommissioned")
	// ErrSlotDecommissioned is returned when geometry or pick sequence is
	// set on a Decommissioned slot — a retired location's physical facts
	// are frozen, not updatable.
	ErrSlotDecommissioned = errors.New("cannot update a decommissioned location slot")
	// ErrNegativePickSequence is returned when an explicit pick-sequence
	// override is negative.
	ErrNegativePickSequence = errors.New("pick sequence must not be negative")
)

// LocationSlot is one coded physical slot on the warehouse map. It owns
// whether that code exists, is active, and is legal for a given kind of
// storage unit — never what is stored in it (that is inventory-storage's
// Bin/StockUnit). Since ADR-0016 a slot may instead be a functional
// (non-storage) location — a Dock, Yard, WorkCenter, Drop, Staging, QC,
// Consolidation, or Shipping location — denormalized from its LocationType's
// role, carrying role-conditional FunctionalAttributes. Since ADR-0017 a
// slot may also carry optional physical geometry (position, dimensions, an
// explicit pick-sequence override) — all three remain the zero value until
// SetGeometry/SetPickSequence is called.
type LocationSlot struct {
	code         shared.LocationCode
	locationType string
	role         placement.LocationRole
	functional   FunctionalAttributes
	capacity     shared.Capacity
	status       shared.Status
	position     shared.Point3D
	dimensions   shared.Dimensions
	pickSequence *int
	// version is the optimistic-concurrency token (ADR-0025): inert
	// infrastructure metadata exactly like the aggregate's own id, never
	// read by business logic. NewLocationSlot starts it at 1;
	// RehydrateLocationSlot populates it from the row that was read, and a
	// persistence adapter's Save rejects the write when the stored row's
	// version has moved on (ports.ErrConcurrentModification) so a
	// read-modify-write caller must re-fetch rather than blind-overwrite.
	version int
}

// NewLocationSlot validates and constructs an Active LocationSlot.
//
// capacityOverride may be the zero Capacity, in which case the
// LocationType's default envelope is used — and, per ADR-0016, capacity is
// only required at all when locationType.Role().RequiresCapacity(); a Dock,
// Yard, WorkCenter, QC, or Shipping location may legitimately have no
// capacity envelope.
//
// functional carries the role-conditional DockFlow/Activities; it must
// already be validated for locationType.Role() (see
// placement.NewFunctionalAttributes) — the constructor here only checks
// that its shape matches the type's role, since the slot never re-derives
// the role's requirements itself.
//
// rules is the full set of PlacementRules the use case loaded; attrs
// describes the Zone the code resolves to. Construction fails if the
// LocationType violates any applicable rule, and the error names the
// violated rule.
func NewLocationSlot(
	code shared.LocationCode,
	locationType placement.LocationType,
	capacityOverride shared.Capacity,
	functional FunctionalAttributes,
	attrs placement.ZoneAttributes,
	rules placement.RuleSet,
) (*LocationSlot, error) {
	if code.IsZero() {
		return nil, ErrMissingLocationCode
	}
	if locationType.Name() == "" {
		return nil, ErrMissingLocationType
	}
	if attrs.ZoneID != code.ZoneID() {
		return nil, ErrZoneMismatch
	}

	capacity := capacityOverride
	if capacity.IsZero() {
		capacity = locationType.DefaultCapacity()
	}
	if locationType.Role().RequiresCapacity() && capacity.IsZero() {
		return nil, shared.ErrInvalidMaxWeight
	}

	if err := rules.Check(locationType.Name(), attrs); err != nil {
		return nil, err
	}

	return &LocationSlot{
		code:         code,
		locationType: locationType.Name(),
		role:         locationType.Role(),
		functional:   functional,
		capacity:     capacity,
		status:       shared.Active,
		version:      1,
	}, nil
}

// RehydrateLocationSlot rebuilds a LocationSlot from persisted state
// without re-running the registration invariants (the rule set may have
// changed since; existing slots are not retroactively invalidated).
// position/dimensions are the zero value when no geometry was ever set
// (ADR-0017); pickSequence is nil under the same condition. version is the
// row's optimistic-concurrency version (ADR-0025); pass 1 for a row written
// before the version column existed. Persistence adapters only.
func RehydrateLocationSlot(
	code shared.LocationCode,
	locationType string,
	role placement.LocationRole,
	functional FunctionalAttributes,
	capacity shared.Capacity,
	status shared.Status,
	position shared.Point3D,
	dimensions shared.Dimensions,
	pickSequence *int,
	version int,
) *LocationSlot {
	return &LocationSlot{
		code: code, locationType: locationType, role: role,
		functional: functional, capacity: capacity, status: status,
		position: position, dimensions: dimensions, pickSequence: pickSequence,
		version: version,
	}
}

// Code returns the slot's identity.
func (s *LocationSlot) Code() shared.LocationCode { return s.code }

// LocationType returns the name of the slot's LocationType.
func (s *LocationSlot) LocationType() string { return s.locationType }

// Role returns what this slot is for — Storage by convention for every
// slot registered before ADR-0016.
func (s *LocationSlot) Role() placement.LocationRole { return s.role }

// Functional returns the slot's role-conditional DockFlow/Activities. It
// is the zero FunctionalAttributes for every role other than Dock/WorkCenter.
func (s *LocationSlot) Functional() FunctionalAttributes { return s.functional }

// Capacity returns the slot's effective capacity envelope. It is the zero
// Capacity for a role that does not require one (ADR-0016).
func (s *LocationSlot) Capacity() shared.Capacity { return s.capacity }

// Status returns the slot's lifecycle status.
func (s *LocationSlot) Status() shared.Status { return s.status }

// IsActive reports whether the slot is legal for storage today.
func (s *LocationSlot) IsActive() bool { return s.status == shared.Active }

// Decommission permanently retires the slot. One-way in v1: there is no
// reactivation use case, and re-registering the same code is rejected as a
// duplicate rather than quietly resurrecting it.
func (s *LocationSlot) Decommission() error {
	if s.status == shared.Decommissioned {
		return ErrAlreadyDecommissioned
	}
	s.status = shared.Decommissioned
	return nil
}

// Position returns the slot's physical position, or the zero Point3D if
// none has been set (ADR-0017).
func (s *LocationSlot) Position() shared.Point3D { return s.position }

// Dimensions returns the slot's physical footprint, or the zero Dimensions
// if none has been set (ADR-0017).
func (s *LocationSlot) Dimensions() shared.Dimensions { return s.dimensions }

// PickSequence returns the slot's explicit pick-sequence override, or nil
// when unset — meaning callers should derive an order from the aisle's
// SequenceHint plus the slot's bay/level/position segments instead
// (ADR-0017).
func (s *LocationSlot) PickSequence() *int { return s.pickSequence }

// Version returns the slot's optimistic-concurrency version (ADR-0025).
// Infrastructure metadata only: domain logic never branches on it; the
// persistence adapters use it to reject a Save built from a stale read.
func (s *LocationSlot) Version() int { return s.version }

// SetGeometry records the slot's physical position and footprint. Rejected
// on a Decommissioned slot: a retired location's physical facts are
// frozen. position and dimensions must both be real (non-zero) values —
// use NewPoint3D/NewDimensions to construct them, even for a literal
// (0,0,0) origin.
func (s *LocationSlot) SetGeometry(position shared.Point3D, dimensions shared.Dimensions) error {
	if s.status == shared.Decommissioned {
		return ErrSlotDecommissioned
	}
	if position.IsZero() {
		return shared.ErrInvalidZ
	}
	if dimensions.IsZero() {
		return shared.ErrInvalidDimensions
	}
	s.position = position
	s.dimensions = dimensions
	return nil
}

// SetPickSequence records an explicit pick-sequence override, superseding
// the derived bay/level/position order. Rejected on a Decommissioned slot
// or a negative sequence.
func (s *LocationSlot) SetPickSequence(sequence int) error {
	if s.status == shared.Decommissioned {
		return ErrSlotDecommissioned
	}
	if sequence < 0 {
		return ErrNegativePickSequence
	}
	s.pickSequence = &sequence
	return nil
}
