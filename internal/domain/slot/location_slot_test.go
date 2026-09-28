package slot_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/slot"
)

func mustCode(t *testing.T, raw string) shared.LocationCode {
	t.Helper()
	code, err := shared.ParseLocationCode(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return code
}

func mustCapacity(t *testing.T, weight, volume float64) shared.Capacity {
	t.Helper()
	capacity, err := shared.NewCapacity(weight, volume)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return capacity
}

func mustLocationType(t *testing.T, name string, weight, volume float64) placement.LocationType {
	t.Helper()
	lt, err := placement.NewLocationType(name, placement.Storage, mustCapacity(t, weight, volume))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return lt
}

// mustLocationTypeWithRole builds a LocationType for a non-Storage role. A
// zero weight/volume is passed straight through as the zero Capacity
// (legal for a role that does not RequireCapacity()).
func mustLocationTypeWithRole(t *testing.T, name string, role placement.LocationRole, weight, volume float64) placement.LocationType {
	t.Helper()
	capacity := shared.Capacity{}
	if weight != 0 || volume != 0 {
		capacity = mustCapacity(t, weight, volume)
	}
	lt, err := placement.NewLocationType(name, role, capacity)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return lt
}

// mustFunctional builds FunctionalAttributes for role, using dockFlow for
// Dock and activities for WorkCenter (the caller passes whichever applies
// and leaves the other at its zero value).
func mustFunctional(t *testing.T, role placement.LocationRole, dockFlow slot.DockFlow, activities []slot.Activity) slot.FunctionalAttributes {
	t.Helper()
	f, err := slot.NewFunctionalAttributes(role, dockFlow, activities)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return f
}

func mustRule(t *testing.T, id, locationType string, effect placement.Effect, zoneCode string, temperatureClass shared.TemperatureClass) placement.PlacementRule {
	t.Helper()
	predicate, err := placement.NewZonePredicate(zoneCode, temperatureClass, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rule, err := placement.NewPlacementRule(id, locationType, effect, predicate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return rule
}

// ambientAttrs is the ambient storage-zone attribute set the NewLocationSlot
// subtests seed against.
var ambientAttrs = placement.ZoneAttributes{ZoneID: "WH1-STOR-AMB", ZoneCode: "AMB", TemperatureClass: shared.Ambient}

func TestNewLocationSlot(t *testing.T) {
	t.Run("uses the location type's default capacity envelope", testSlotUsesTypeDefaultCapacity)
	t.Run("an explicit override beats the type default", testSlotCapacityOverrideBeatsDefault)
	t.Run("a zero location code is rejected", testSlotRejectsZeroCode)
	t.Run("a zero location type is rejected", testSlotRejectsZeroType)
	t.Run("zone attributes for a different zone are rejected", testSlotRejectsZoneMismatch)
	t.Run("a location type with no default capacity and no override is rejected", testSlotRejectsBareStorageType)
	t.Run("a Dock-role type with no capacity is accepted", testSlotAcceptsDockRole)
	t.Run("a WorkCenter-role type with activities is accepted", testSlotAcceptsWorkCenterRole)
}

func testSlotUsesTypeDefaultCapacity(t *testing.T) {
	t.Helper()
	palletRack := mustLocationType(t, placement.PalletRack, 1200, 2.4)
	s, err := slot.NewLocationSlot(mustCode(t, "WH1-STOR-AMB-A07-03-02-B"), palletRack, shared.Capacity{}, slot.FunctionalAttributes{}, ambientAttrs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Capacity().MaxWeightKg() != 1200 || s.Capacity().MaxVolumeM3() != 2.4 {
		t.Fatalf("expected the type default envelope, got %v", s.Capacity())
	}
	if s.Code().String() != "WH1-STOR-AMB-A07-03-02-B" || s.LocationType() != placement.PalletRack {
		t.Fatalf("unexpected slot %q/%q", s.Code(), s.LocationType())
	}
	if s.Role() != placement.Storage {
		t.Fatalf("expected Storage role by default, got %q", s.Role())
	}
	if !s.IsActive() || s.Status() != shared.Active {
		t.Fatalf("a newly registered slot must be Active, got %q", s.Status())
	}
}

func testSlotCapacityOverrideBeatsDefault(t *testing.T) {
	t.Helper()
	palletRack := mustLocationType(t, placement.PalletRack, 1200, 2.4)
	s, err := slot.NewLocationSlot(mustCode(t, "WH1-STOR-AMB-A07-03-02-B"), palletRack, mustCapacity(t, 500, 1.1), slot.FunctionalAttributes{}, ambientAttrs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Capacity().MaxWeightKg() != 500 || s.Capacity().MaxVolumeM3() != 1.1 {
		t.Fatalf("expected the override envelope, got %v", s.Capacity())
	}
}

func testSlotRejectsZeroCode(t *testing.T) {
	t.Helper()
	palletRack := mustLocationType(t, placement.PalletRack, 1200, 2.4)
	_, err := slot.NewLocationSlot(shared.LocationCode{}, palletRack, shared.Capacity{}, slot.FunctionalAttributes{}, ambientAttrs, nil)
	if !errors.Is(err, slot.ErrMissingLocationCode) {
		t.Fatalf("expected ErrMissingLocationCode, got %v", err)
	}
}

func testSlotRejectsZeroType(t *testing.T) {
	t.Helper()
	_, err := slot.NewLocationSlot(mustCode(t, "WH1-STOR-AMB-A07-03-02-B"), placement.LocationType{}, shared.Capacity{}, slot.FunctionalAttributes{}, ambientAttrs, nil)
	if !errors.Is(err, slot.ErrMissingLocationType) {
		t.Fatalf("expected ErrMissingLocationType, got %v", err)
	}
}

func testSlotRejectsZoneMismatch(t *testing.T) {
	t.Helper()
	palletRack := mustLocationType(t, placement.PalletRack, 1200, 2.4)
	_, err := slot.NewLocationSlot(mustCode(t, "WH1-STOR-FRZ-A07-03-02-B"), palletRack, shared.Capacity{}, slot.FunctionalAttributes{}, ambientAttrs, nil)
	if !errors.Is(err, slot.ErrZoneMismatch) {
		t.Fatalf("expected ErrZoneMismatch, got %v", err)
	}
}

func testSlotRejectsBareStorageType(t *testing.T) {
	t.Helper()
	bare := placement.RehydrateLocationType(placement.Staging, placement.Storage, shared.Capacity{})
	_, err := slot.NewLocationSlot(mustCode(t, "WH1-STOR-AMB-A07-03-02-B"), bare, shared.Capacity{}, slot.FunctionalAttributes{}, ambientAttrs, nil)
	if !errors.Is(err, shared.ErrInvalidMaxWeight) {
		t.Fatalf("expected ErrInvalidMaxWeight, got %v", err)
	}
}

func testSlotAcceptsDockRole(t *testing.T) {
	t.Helper()
	dockType := mustLocationTypeWithRole(t, "DockDoor", placement.Dock, 0, 0)
	functional := mustFunctional(t, placement.Dock, slot.Outbound, nil)
	dockAttrs := placement.ZoneAttributes{ZoneID: "WH1-DOCK-OB", ZoneCode: "OB", TemperatureClass: shared.Ambient}
	s, err := slot.NewLocationSlot(mustCode(t, "WH1-DOCK-OB-D07-00-00-A"), dockType, shared.Capacity{}, functional, dockAttrs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Role() != placement.Dock || s.Functional().DockFlow() != slot.Outbound {
		t.Fatalf("unexpected dock slot role/flow: %q/%q", s.Role(), s.Functional().DockFlow())
	}
	if !s.Capacity().IsZero() {
		t.Fatalf("expected a Dock slot to have no capacity, got %v", s.Capacity())
	}
}

func testSlotAcceptsWorkCenterRole(t *testing.T) {
	t.Helper()
	workCenterType := mustLocationTypeWithRole(t, "PackStation", placement.WorkCenter, 0, 0)
	functional := mustFunctional(t, placement.WorkCenter, "", []slot.Activity{slot.Pack, slot.VAS})
	wcAttrs := placement.ZoneAttributes{ZoneID: "WH1-PACK-WC", ZoneCode: "WC", TemperatureClass: shared.Ambient}
	s, err := slot.NewLocationSlot(mustCode(t, "WH1-PACK-WC-P01-00-00-A"), workCenterType, shared.Capacity{}, functional, wcAttrs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	activities := s.Functional().Activities()
	if len(activities) != 2 || activities[0] != slot.Pack || activities[1] != slot.VAS {
		t.Fatalf("unexpected work center activities: %v", activities)
	}
}

func TestNewLocationSlotEnforcesPlacementRules(t *testing.T) {
	hazAttrs := placement.ZoneAttributes{ZoneID: "WH1-STOR-HAZ", ZoneCode: "HAZ", TemperatureClass: shared.Ambient, Hazmat: true}
	frozenAttrs := placement.ZoneAttributes{ZoneID: "WH1-STOR-FRZ", ZoneCode: "FRZ", TemperatureClass: shared.Frozen}

	palletRack := mustLocationType(t, placement.PalletRack, 1200, 2.4)
	shelf := mustLocationType(t, placement.Shelf, 60, 0.4)

	rules := placement.RuleSet{
		mustRule(t, "RULE-HAZ-ONLY-RACK", placement.PalletRack, placement.Allow, "HAZ", ""),
		mustRule(t, "RULE-FRZ-NO-SHELF", placement.Shelf, placement.Deny, "", shared.Frozen),
	}

	t.Run("a legal placement is accepted", func(t *testing.T) {
		if _, err := slot.NewLocationSlot(mustCode(t, "WH1-STOR-HAZ-A01-01-01-A"), palletRack, shared.Capacity{}, slot.FunctionalAttributes{}, hazAttrs, rules); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("a type outside the zone's allow-list is rejected naming the rule", func(t *testing.T) {
		_, err := slot.NewLocationSlot(mustCode(t, "WH1-STOR-HAZ-A01-01-01-A"), shelf, shared.Capacity{}, slot.FunctionalAttributes{}, hazAttrs, rules)
		if !errors.Is(err, placement.ErrPlacementRuleViolated) {
			t.Fatalf("expected ErrPlacementRuleViolated, got %v", err)
		}
		if !strings.Contains(err.Error(), "RULE-HAZ-ONLY-RACK") {
			t.Fatalf("expected the violated rule to be named, got %q", err.Error())
		}
	})

	t.Run("a denied type in a frozen zone is rejected naming the rule", func(t *testing.T) {
		_, err := slot.NewLocationSlot(mustCode(t, "WH1-STOR-FRZ-A02-01-01-A"), shelf, shared.Capacity{}, slot.FunctionalAttributes{}, frozenAttrs, rules)
		if !errors.Is(err, placement.ErrPlacementRuleViolated) {
			t.Fatalf("expected ErrPlacementRuleViolated, got %v", err)
		}
		if !strings.Contains(err.Error(), "RULE-FRZ-NO-SHELF") {
			t.Fatalf("expected the violated rule to be named, got %q", err.Error())
		}
	})
}

func TestLocationSlotDecommissionIsOneWay(t *testing.T) {
	attrs := placement.ZoneAttributes{ZoneID: "WH1-STOR-AMB", ZoneCode: "AMB", TemperatureClass: shared.Ambient}
	s, err := slot.NewLocationSlot(mustCode(t, "WH1-STOR-AMB-A07-03-02-B"), mustLocationType(t, placement.PalletRack, 1200, 2.4), shared.Capacity{}, slot.FunctionalAttributes{}, attrs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := s.Decommission(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.IsActive() || s.Status() != shared.Decommissioned {
		t.Fatalf("expected a Decommissioned slot, got %q", s.Status())
	}
	if err := s.Decommission(); !errors.Is(err, slot.ErrAlreadyDecommissioned) {
		t.Fatalf("expected ErrAlreadyDecommissioned, got %v", err)
	}
}

func TestRehydrateLocationSlot(t *testing.T) {
	code := mustCode(t, "WH1-RCV-AMB-D01-01-01-A")
	s := slot.RehydrateLocationSlot(code, placement.Staging, placement.Storage, slot.FunctionalAttributes{}, mustCapacity(t, 900, 3), shared.UnderMaintenance, shared.Point3D{}, shared.Dimensions{}, nil)

	if s.Code() != code || s.LocationType() != placement.Staging {
		t.Fatalf("unexpected rehydrated slot %q/%q", s.Code(), s.LocationType())
	}
	if s.IsActive() {
		t.Fatal("an UnderMaintenance slot must not report as Active")
	}
	if err := s.Decommission(); err != nil {
		t.Fatalf("an UnderMaintenance slot must still be decommissionable: %v", err)
	}
	if s.Status() != shared.Decommissioned {
		t.Fatalf("expected Decommissioned, got %q", s.Status())
	}
}
