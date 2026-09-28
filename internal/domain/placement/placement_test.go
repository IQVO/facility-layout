package placement_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

func mustCapacity(t *testing.T, weight, volume float64) shared.Capacity {
	t.Helper()
	capacity, err := shared.NewCapacity(weight, volume)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return capacity
}

func boolPtr(v bool) *bool { return &v }

func TestNewLocationType(t *testing.T) {
	capacity := mustCapacity(t, 1200, 2.4)

	tests := []struct {
		name     string
		typeName string
		role     placement.LocationRole
		capacity shared.Capacity
		wantErr  error
	}{
		{name: "a pallet rack", typeName: placement.PalletRack, role: placement.Storage, capacity: capacity},
		{name: "an amnesty location", typeName: placement.Amnesty, role: placement.Storage, capacity: capacity},
		{name: "empty name", typeName: "", role: placement.Storage, capacity: capacity, wantErr: placement.ErrEmptyLocationTypeName},
		{name: "missing default capacity for a storage role", typeName: placement.Shelf, role: placement.Storage, capacity: shared.Capacity{}, wantErr: shared.ErrInvalidMaxWeight},
		{name: "unknown role", typeName: placement.Shelf, role: "Warehouse", capacity: capacity, wantErr: placement.ErrUnknownLocationRole},
		{name: "a dock door needs no capacity", typeName: "DockDoor", role: placement.Dock, capacity: shared.Capacity{}},
		{name: "a work center needs no capacity", typeName: "PackStation", role: placement.WorkCenter, capacity: shared.Capacity{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lt, err := placement.NewLocationType(tc.typeName, tc.role, tc.capacity)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if lt.Name() != tc.typeName || lt.Role() != tc.role || lt.DefaultCapacity() != tc.capacity {
				t.Fatalf("unexpected location type %q/%q/%v", lt.Name(), lt.Role(), lt.DefaultCapacity())
			}
		})
	}
}

func TestRehydrateLocationType(t *testing.T) {
	capacity := mustCapacity(t, 50, 0.3)
	lt := placement.RehydrateLocationType(placement.ToteWall, placement.Storage, capacity)
	if lt.Name() != placement.ToteWall || lt.DefaultCapacity().MaxVolumeM3() != 0.3 {
		t.Fatalf("unexpected rehydrated type %q/%v", lt.Name(), lt.DefaultCapacity())
	}
}

// zonePredicateCase is one row of the NewZonePredicate table: either a
// constraining predicate (wantString set) or an invalid one (wantErr set).
type zonePredicateCase struct {
	name             string
	zoneCode         string
	temperatureClass shared.TemperatureClass
	hazmat           *bool
	wantString       string
	wantErr          error
}

func TestNewZonePredicate(t *testing.T) {
	tests := []zonePredicateCase{
		{name: "pinned to a zone code", zoneCode: "HAZ", wantString: "zoneCode=HAZ"},
		{name: "pinned to a temperature class", temperatureClass: shared.Frozen, wantString: "temperatureClass=Frozen"},
		{name: "pinned to hazmat", hazmat: boolPtr(true), wantString: "hazmat=true"},
		{name: "pinned to every dimension", zoneCode: "FRZ", temperatureClass: shared.Frozen, hazmat: boolPtr(false), wantString: "zoneCode=FRZ,temperatureClass=Frozen,hazmat=false"},
		{name: "constrains nothing", wantErr: placement.ErrEmptyPredicate},
		{name: "unknown temperature class", temperatureClass: "Tepid", wantErr: shared.ErrUnknownTemperatureClass},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertNewZonePredicate(t, tc)
		})
	}
}

// assertNewZonePredicate exercises one table row: the error arm checks the
// typed sentinel, the success arm checks the rendered predicate and that no
// dimension was lost.
func assertNewZonePredicate(t *testing.T, tc zonePredicateCase) {
	t.Helper()
	p, err := placement.NewZonePredicate(tc.zoneCode, tc.temperatureClass, tc.hazmat)
	if tc.wantErr != nil {
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("expected error %v, got %v", tc.wantErr, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.String() != tc.wantString {
		t.Fatalf("expected predicate %q, got %q", tc.wantString, p.String())
	}
	if p.ZoneCode() != tc.zoneCode || p.TemperatureClass() != tc.temperatureClass {
		t.Fatalf("predicate lost its dimensions: %+v", p)
	}
	if (p.Hazmat() == nil) != (tc.hazmat == nil) {
		t.Fatalf("predicate lost its hazmat dimension: %+v", p)
	}
}

func TestZonePredicateMatches(t *testing.T) {
	frozenHazZone := placement.ZoneAttributes{ZoneID: "WH1-STOR-HAZ", ZoneCode: "HAZ", TemperatureClass: shared.Frozen, Hazmat: true}

	tests := []struct {
		name             string
		zoneCode         string
		temperatureClass shared.TemperatureClass
		hazmat           *bool
		want             bool
	}{
		{name: "zone code hit", zoneCode: "HAZ", want: true},
		{name: "zone code miss", zoneCode: "AMB", want: false},
		{name: "temperature hit", temperatureClass: shared.Frozen, want: true},
		{name: "temperature miss", temperatureClass: shared.Ambient, want: false},
		{name: "hazmat hit", hazmat: boolPtr(true), want: true},
		{name: "hazmat miss", hazmat: boolPtr(false), want: false},
		{name: "every dimension hits", zoneCode: "HAZ", temperatureClass: shared.Frozen, hazmat: boolPtr(true), want: true},
		{name: "one dimension misses so the whole predicate misses", zoneCode: "HAZ", temperatureClass: shared.Chilled, hazmat: boolPtr(true), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := placement.NewZonePredicate(tc.zoneCode, tc.temperatureClass, tc.hazmat)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := p.Matches(frozenHazZone); got != tc.want {
				t.Fatalf("expected Matches=%t, got %t", tc.want, got)
			}
		})
	}
}

func TestNewPlacementRule(t *testing.T) {
	predicate, err := placement.NewZonePredicate("HAZ", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		name         string
		id           string
		locationType string
		effect       placement.Effect
		predicate    placement.ZonePredicate
		wantErr      error
	}{
		{name: "allow pallet rack in hazmat", id: "RULE-HAZ-1", locationType: placement.PalletRack, effect: placement.Allow, predicate: predicate},
		{name: "deny shelving in hazmat", id: "RULE-HAZ-2", locationType: placement.Shelf, effect: placement.Deny, predicate: predicate},
		{name: "empty id", id: "", locationType: placement.PalletRack, effect: placement.Allow, predicate: predicate, wantErr: placement.ErrEmptyRuleID},
		{name: "empty location type", id: "RULE-1", locationType: "", effect: placement.Allow, predicate: predicate, wantErr: placement.ErrEmptyRuleLocationType},
		{name: "unknown effect", id: "RULE-1", locationType: placement.PalletRack, effect: "Maybe", predicate: predicate, wantErr: placement.ErrUnknownEffect},
		{name: "zero predicate", id: "RULE-1", locationType: placement.PalletRack, effect: placement.Allow, predicate: placement.ZonePredicate{}, wantErr: placement.ErrEmptyPredicate},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule, err := placement.NewPlacementRule(tc.id, tc.locationType, tc.effect, tc.predicate)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rule.ID() != tc.id || rule.LocationType() != tc.locationType || rule.Effect() != tc.effect {
				t.Fatalf("unexpected rule %+v", rule)
			}
			if !strings.Contains(rule.Describe(), tc.id) || !strings.Contains(rule.Describe(), "zoneCode=HAZ") {
				t.Fatalf("rule description must name the rule and its predicate, got %q", rule.Describe())
			}
		})
	}
}

func TestRehydratePlacementRule(t *testing.T) {
	predicate, err := placement.NewZonePredicate("", shared.Frozen, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rule := placement.RehydratePlacementRule("RULE-FRZ-1", placement.Shelf, placement.Deny, predicate)
	if rule.ID() != "RULE-FRZ-1" || rule.Effect() != placement.Deny {
		t.Fatalf("unexpected rehydrated rule %+v", rule)
	}
	if rule.Predicate().TemperatureClass() != shared.Frozen {
		t.Fatalf("rehydrated rule lost its predicate: %q", rule.Predicate().String())
	}
}

func TestParseEffect(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    placement.Effect
		wantErr error
	}{
		{name: "allow", raw: "Allow", want: placement.Allow},
		{name: "deny", raw: "Deny", want: placement.Deny},
		{name: "unknown", raw: "Perhaps", wantErr: placement.ErrUnknownEffect},
		{name: "empty", raw: "", wantErr: placement.ErrUnknownEffect},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := placement.ParseEffect(tc.raw)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}
