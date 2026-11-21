//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
	"github.com/claudioed/facility-layout/internal/domain/slot"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

// TestTheIndexRefusesASecondActiveAutomatedStorageInAnAisle proves migration
// 0014's partial unique index against a live database (ADR-0048): the slot
// repository is driven directly, so the use case's in-memory "one AS/RS per
// aisle" check is bypassed on purpose and only the index stands between a
// second active AutomatedStorage slot and the aisle. A decommissioned slot
// no longer counts, so the aisle takes a new AS/RS afterwards.
func TestTheIndexRefusesASecondActiveAutomatedStorageInAnAisle(t *testing.T) {
	ctx, pool := newPool(t)
	seedAutomatedStorageAisle(ctx, t, pool)
	repo := postgres.NewSlotRepo(pool)
	first := newAutomatedStorageSlot(t, "WH1-AUTO-ASRS-ASRS1-00-00-S")
	if err := repo.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := newAutomatedStorageSlot(t, "WH1-AUTO-ASRS-ASRS1-00-00-T")
	if err := repo.Save(ctx, second); !errors.Is(err, ports.ErrAutomatedStorageAisleTaken) {
		t.Fatalf("second active slot: err = %v, want ErrAutomatedStorageAisleTaken", err)
	}
	stored, _ := repo.FindByCode(ctx, mustCode(t, "WH1-AUTO-ASRS-ASRS1-00-00-S"))
	if err := stored.Decommission(); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, newAutomatedStorageSlot(t, "WH1-AUTO-ASRS-ASRS1-00-00-T")); err != nil {
		t.Fatalf("after decommissioning the first: %v", err)
	}
}

// seedAutomatedStorageAisle registers site WH1, zone WH1-AUTO-ASRS, aisle
// WH1-AUTO-ASRS-ASRS1 and the AutomatedStorage location type (role Storage)
// through the repositories, exactly as handling_unit_integration_test.go
// seeds a PalletRack slot's chain.
func seedAutomatedStorageAisle(ctx context.Context, t *testing.T, pool *pgxPool) {
	t.Helper()
	s, _ := site.NewSite("WH1", "Fulfilment Centre One")
	if err := postgres.NewSiteRepo(pool).Save(ctx, s); err != nil {
		t.Fatalf("site: %v", err)
	}
	z, _ := zone.NewZone("WH1", "AUTO", "ASRS", shared.Ambient, false)
	if err := postgres.NewZoneRepo(pool).Save(ctx, z); err != nil {
		t.Fatalf("zone: %v", err)
	}
	a, _ := aisle.NewAisle("WH1-AUTO-ASRS", "ASRS1", 1, shared.TwoWay)
	if err := postgres.NewAisleRepo(pool).Save(ctx, a); err != nil {
		t.Fatalf("aisle: %v", err)
	}
	lt, err := placement.NewLocationType(placement.AutomatedStorage, placement.Storage, mustCapacity(t, 50000, 400))
	if err != nil {
		t.Fatalf("location type: %v", err)
	}
	if err := postgres.NewLocationTypeRepo(pool).Save(ctx, lt); err != nil {
		t.Fatalf("type: %v", err)
	}
}

// newAutomatedStorageSlot builds an active AutomatedStorage slot at raw in
// zone WH1-AUTO-ASRS, with the zero RuleSet the other integration tests use.
func newAutomatedStorageSlot(t *testing.T, raw string) *slot.LocationSlot {
	t.Helper()
	capacity := mustCapacity(t, 50000, 400)
	built, err := slot.NewLocationSlot(mustCode(t, raw),
		placement.RehydrateLocationType(placement.AutomatedStorage, placement.Storage, capacity),
		shared.Capacity{}, slot.FunctionalAttributes{},
		placement.ZoneAttributes{ZoneID: "WH1-AUTO-ASRS", ZoneCode: "ASRS", TemperatureClass: shared.Ambient},
		nil)
	if err != nil {
		t.Fatalf("slot %s: %v", raw, err)
	}
	return built
}
