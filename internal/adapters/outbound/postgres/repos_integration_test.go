//go:build integration

package postgres_test

import (
	"testing"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
	"github.com/claudioed/facility-layout/internal/domain/slot"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

func TestPostgresSiteRepoRoundTrip(t *testing.T) {
	ctx, pool := newPool(t)
	repo := postgres.NewSiteRepo(pool)

	s, err := site.NewSite("WH1", "Fulfilment Centre One")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := repo.Save(ctx, s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found, err := repo.FindByCode(ctx, "WH1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found == nil || found.Name() != "Fulfilment Centre One" || !found.IsActive() {
		t.Fatalf("unexpected round-trip result %+v", found)
	}

	missing, err := repo.FindByCode(ctx, "NOPE")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if missing != nil {
		t.Fatal("expected (nil, nil) for an unknown site")
	}

	// A decommissioned site round-trips its status.
	if err := found.Decommission(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := repo.Save(ctx, found); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	reloaded, err := repo.FindByCode(ctx, "WH1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reloaded.IsActive() {
		t.Fatal("expected the persisted site to be Decommissioned")
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(all) != 1 || all[0].Code() != "WH1" {
		t.Fatalf("unexpected list %+v", all)
	}
}

func TestPostgresZoneAndAisleRepoRoundTrip(t *testing.T) {
	ctx, pool := newPool(t)

	sites := postgres.NewSiteRepo(pool)
	zones := postgres.NewZoneRepo(pool)
	aisles := postgres.NewAisleRepo(pool)

	s, _ := site.NewSite("WH1", "Fulfilment Centre One")
	if err := sites.Save(ctx, s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	frozen, err := zone.NewZone("WH1", "STOR", "FRZ", shared.Frozen, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ambient, err := zone.NewZone("WH1", "STOR", "AMB", shared.Ambient, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, z := range []*zone.Zone{frozen, ambient} {
		if err := zones.Save(ctx, z); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	found, err := zones.FindByID(ctx, "WH1-STOR-FRZ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found == nil || found.TemperatureClass() != shared.Frozen || found.Hazmat() {
		t.Fatalf("unexpected zone round-trip %+v", found)
	}
	if missing, _ := zones.FindByID(ctx, "WH1-STOR-NOPE"); missing != nil {
		t.Fatal("expected (nil, nil) for an unknown zone")
	}

	inSite, err := zones.ListBySite(ctx, "WH1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(inSite) != 2 || inSite[0].ID() != "WH1-STOR-AMB" {
		t.Fatalf("expected zones ordered by id, got %+v", inSite)
	}

	a9, _ := aisle.NewAisle("WH1-STOR-AMB", "A09", 9, shared.OneWay)
	a7, _ := aisle.NewAisle("WH1-STOR-AMB", "A07", 7, shared.TwoWay)
	for _, a := range []*aisle.Aisle{a9, a7} {
		if err := aisles.Save(ctx, a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	foundAisle, err := aisles.FindByID(ctx, "WH1-STOR-AMB-A07")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if foundAisle == nil || foundAisle.SequenceHint() != 7 || foundAisle.Direction() != shared.TwoWay {
		t.Fatalf("unexpected aisle round-trip %+v", foundAisle)
	}
	if missing, _ := aisles.FindByID(ctx, "WH1-STOR-AMB-A99"); missing != nil {
		t.Fatal("expected (nil, nil) for an unknown aisle")
	}

	inZone, err := aisles.ListByZone(ctx, "WH1-STOR-AMB")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(inZone) != 2 || inZone[0].AisleCode() != "A07" {
		t.Fatalf("expected aisles in walk order, got %+v", inZone)
	}
}

func TestPostgresLocationTypeAndPlacementRuleRoundTrip(t *testing.T) {
	ctx, pool := newPool(t)

	types := postgres.NewLocationTypeRepo(pool)
	rules := postgres.NewPlacementRuleRepo(pool)

	if err := types.Save(ctx, mustLocationType(t, placement.PalletRack, 1200, 2.4)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := types.Save(ctx, mustLocationType(t, placement.Shelf, 60, 0.4)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found, err := types.FindByName(ctx, placement.PalletRack)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found == nil || found.DefaultCapacity().MaxWeightKg() != 1200 {
		t.Fatalf("unexpected location type round-trip %+v", found)
	}
	if missing, _ := types.FindByName(ctx, "Hovercraft"); missing != nil {
		t.Fatal("expected (nil, nil) for an unknown location type")
	}
	allTypes, err := types.List(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(allTypes) != 2 || allTypes[0].Name() != placement.PalletRack {
		t.Fatalf("expected types ordered by name, got %+v", allTypes)
	}

	// A predicate pinned only to a zone code leaves the other two
	// dimensions NULL; one pinned to temperature + hazmat leaves zone NULL.
	zonePinned, err := placement.NewZonePredicate("HAZ", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hazmat := true
	behaviourPinned, err := placement.NewZonePredicate("", shared.Frozen, &hazmat)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ruleA, _ := placement.NewPlacementRule("RULE-HAZ-ONLY-RACK", placement.PalletRack, placement.Allow, zonePinned)
	ruleB, _ := placement.NewPlacementRule("RULE-FRZ-NO-SHELF", placement.Shelf, placement.Deny, behaviourPinned)
	for _, r := range []placement.PlacementRule{ruleA, ruleB} {
		if err := rules.Save(ctx, r); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	foundRule, err := rules.FindByID(ctx, "RULE-FRZ-NO-SHELF")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if foundRule == nil || foundRule.Effect() != placement.Deny {
		t.Fatalf("unexpected rule round-trip %+v", foundRule)
	}
	if got := foundRule.Predicate().String(); got != "temperatureClass=Frozen,hazmat=true" {
		t.Fatalf("the predicate did not round-trip: %q", got)
	}
	if missing, _ := rules.FindByID(ctx, "RULE-NOPE"); missing != nil {
		t.Fatal("expected (nil, nil) for an unknown rule")
	}

	allRules, err := rules.List(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(allRules) != 2 || allRules[0].ID() != "RULE-FRZ-NO-SHELF" {
		t.Fatalf("expected rules ordered by id, got %+v", allRules)
	}
	if allRules[1].Predicate().String() != "zoneCode=HAZ" {
		t.Fatalf("the zone-pinned predicate did not round-trip: %q", allRules[1].Predicate().String())
	}
}

func TestPostgresSlotRepoRoundTrip(t *testing.T) {
	ctx, pool := newPool(t)

	sites := postgres.NewSiteRepo(pool)
	zones := postgres.NewZoneRepo(pool)
	aisles := postgres.NewAisleRepo(pool)
	types := postgres.NewLocationTypeRepo(pool)
	slots := postgres.NewSlotRepo(pool)

	s, _ := site.NewSite("WH1", "Fulfilment Centre One")
	if err := sites.Save(ctx, s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	z, _ := zone.NewZone("WH1", "STOR", "AMB", shared.Ambient, false)
	if err := zones.Save(ctx, z); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a, _ := aisle.NewAisle("WH1-STOR-AMB", "A07", 7, shared.TwoWay)
	if err := aisles.Save(ctx, a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	palletRack := mustLocationType(t, placement.PalletRack, 1200, 2.4)
	if err := types.Save(ctx, palletRack); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	attrs := placement.ZoneAttributes{ZoneID: z.ID(), ZoneCode: z.ZoneCode(), TemperatureClass: z.TemperatureClass()}
	for _, raw := range []string{"WH1-STOR-AMB-A07-03-02-B", "WH1-STOR-AMB-A07-03-01-A", "WH1-STOR-AMB-A07-03-02-A"} {
		built, err := slot.NewLocationSlot(mustCode(t, raw), palletRack, shared.Capacity{}, slot.FunctionalAttributes{}, attrs, nil)
		if err != nil {
			t.Fatalf("unexpected error building %q: %v", raw, err)
		}
		if err := slots.Save(ctx, built); err != nil {
			t.Fatalf("unexpected error saving %q: %v", raw, err)
		}
	}

	found, err := slots.FindByCode(ctx, mustCode(t, "WH1-STOR-AMB-A07-03-02-B"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found == nil || found.LocationType() != placement.PalletRack || found.Capacity().MaxWeightKg() != 1200 {
		t.Fatalf("unexpected slot round-trip %+v", found)
	}
	if !found.IsActive() {
		t.Fatal("expected the persisted slot to be Active")
	}

	missing, err := slots.FindByCode(ctx, mustCode(t, "WH1-STOR-AMB-A07-99-99-Z"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if missing != nil {
		t.Fatal("expected (nil, nil) for an unknown slot")
	}

	byAisle, err := slots.ListByAisle(ctx, "WH1-STOR-AMB-A07")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"WH1-STOR-AMB-A07-03-01-A", "WH1-STOR-AMB-A07-03-02-A", "WH1-STOR-AMB-A07-03-02-B"}
	if len(byAisle) != len(want) {
		t.Fatalf("expected %d slots, got %d", len(want), len(byAisle))
	}
	for i, code := range want {
		if byAisle[i].Code().String() != code {
			t.Fatalf("expected slot %d to be %q, got %q", i, code, byAisle[i].Code())
		}
	}

	byZone, err := slots.ListByZone(ctx, "WH1-STOR-AMB")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(byZone) != 3 {
		t.Fatalf("expected 3 slots in the zone, got %d", len(byZone))
	}

	// Decommission round-trips through the upsert path.
	if err := found.Decommission(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := slots.Save(ctx, found); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	reloaded, err := slots.FindByCode(ctx, mustCode(t, "WH1-STOR-AMB-A07-03-02-B"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reloaded.IsActive() {
		t.Fatal("expected the persisted slot to be Decommissioned")
	}
}
