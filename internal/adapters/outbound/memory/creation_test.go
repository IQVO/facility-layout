package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/structure"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

// creationFixtures holds one aggregate per creation repository, each built
// once so the ADR-0047 tests below stay flat.
type creationFixtures struct {
	zone         *zone.Zone
	aisle        *aisle.Aisle
	secondAisle  *aisle.Aisle
	crossAisle   *aisle.CrossAisle
	structure    *structure.FixedStructure
	locationType placement.LocationType
	rule         placement.PlacementRule
}

func mustCreationFixtures(t *testing.T) *creationFixtures {
	t.Helper()
	z, err := zone.NewZone("WH1", "STOR", "AMB", shared.Ambient, false)
	if err != nil {
		t.Fatalf("build zone: %v", err)
	}
	a, err := aisle.NewAisle("WH1-STOR-AMB", "A07", 7, shared.TwoWay)
	if err != nil {
		t.Fatalf("build aisle: %v", err)
	}
	second, err := aisle.NewAisle("WH1-STOR-AMB", "A07", 9, shared.OneWay)
	if err != nil {
		t.Fatalf("build second aisle: %v", err)
	}
	c, err := aisle.NewCrossAisle("WH1-STOR-AMB", "A01", "A02", "03")
	if err != nil {
		t.Fatalf("build cross-aisle: %v", err)
	}
	origin, err := shared.NewPoint3D(0, 0, 0)
	if err != nil {
		t.Fatalf("build origin: %v", err)
	}
	size, err := shared.NewDimensions(1, 1, 1)
	if err != nil {
		t.Fatalf("build size: %v", err)
	}
	rect, err := shared.NewRect(origin, size)
	if err != nil {
		t.Fatalf("build rect: %v", err)
	}
	f, err := structure.NewFixedStructure("wall-1", "WH1", structure.Wall, rect, "North wall")
	if err != nil {
		t.Fatalf("build structure: %v", err)
	}
	return &creationFixtures{
		zone:         z,
		aisle:        a,
		secondAisle:  second,
		crossAisle:   c,
		structure:    f,
		locationType: placement.RehydrateLocationType("PalletRack", placement.Storage, shared.Capacity{}),
		rule:         placement.RehydratePlacementRule("rule-1", "PalletRack", placement.Deny, placement.ZonePredicate{}),
	}
}

// The memory doubles mirror the Postgres creation contract (ADR-0047):
// Create refuses to create over a held natural key.
func TestCreateRefusesAHeldNaturalKey(t *testing.T) {
	ctx := context.Background()
	f := mustCreationFixtures(t)
	zoneRepo := memory.NewZoneRepo()
	aisleRepo := memory.NewAisleRepo()
	crossAisleRepo := memory.NewCrossAisleRepo()
	structureRepo := memory.NewFixedStructureRepo()
	typeRepo := memory.NewLocationTypeRepo()
	ruleRepo := memory.NewPlacementRuleRepo()

	rows := []struct {
		name   string
		create func() error
	}{
		{"zone", func() error { return zoneRepo.Create(ctx, f.zone) }},
		{"aisle", func() error { return aisleRepo.Create(ctx, f.aisle) }},
		{"cross-aisle", func() error { return crossAisleRepo.Create(ctx, f.crossAisle) }},
		{"fixed structure", func() error { return structureRepo.Create(ctx, f.structure) }},
		{"location type", func() error { return typeRepo.Create(ctx, f.locationType) }},
		{"placement rule", func() error { return ruleRepo.Create(ctx, f.rule) }},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.create(); err != nil {
				t.Fatalf("first create: %v", err)
			}
			if err := tc.create(); !errors.Is(err, ports.ErrNaturalKeyTaken) {
				t.Fatalf("second create = %v, want ports.ErrNaturalKeyTaken (ADR-0047)", err)
			}
		})
	}
}

// Save keeps plain upsert semantics: the aisle's update path
// (SetAisleGeometry writes a rehydrated aisle) still overwrites by
// identity rather than refusing.
func TestAisleSaveKeepsTheUpsertUpdatePath(t *testing.T) {
	ctx := context.Background()
	f := mustCreationFixtures(t)
	aisles := memory.NewAisleRepo()
	if err := aisles.Create(ctx, f.aisle); err != nil {
		t.Fatalf("create aisle: %v", err)
	}
	if err := aisles.Save(ctx, f.secondAisle); err != nil {
		t.Fatalf("save over the created aisle: %v", err)
	}
	loaded, err := aisles.FindByID(ctx, f.aisle.ID())
	if err != nil || loaded == nil {
		t.Fatalf("reload aisle: %v %v", loaded, err)
	}
	if loaded.SequenceHint() != f.secondAisle.SequenceHint() {
		t.Fatalf("stored sequence hint = %d, want the saved %d", loaded.SequenceHint(), f.secondAisle.SequenceHint())
	}
}
