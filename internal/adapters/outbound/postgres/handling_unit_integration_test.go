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

// TestPostgresSlotHandlingUnitRoundTrip proves migration 0011's nullable
// handling_unit column and its CHECK constraint against a live database
// (ADR-0040): a slot registered without one reads back with none, a declared
// value round-trips, and the database itself refuses a value outside
// EACH/CASE/PALLET.
func TestPostgresSlotHandlingUnitRoundTrip(t *testing.T) {
	ctx, pool := newPool(t)

	sites := postgres.NewSiteRepo(pool)
	zones := postgres.NewZoneRepo(pool)
	aisles := postgres.NewAisleRepo(pool)
	types := postgres.NewLocationTypeRepo(pool)
	slots := postgres.NewSlotRepo(pool)

	s, _ := site.NewSite("WH1", "Fulfilment Centre One")
	if err := sites.Save(ctx, s); err != nil {
		t.Fatalf("site: %v", err)
	}
	z, _ := zone.NewZone("WH1", "STOR", "AMB", shared.Ambient, false)
	if err := zones.Save(ctx, z); err != nil {
		t.Fatalf("zone: %v", err)
	}
	a, _ := aisle.NewAisle("WH1-STOR-AMB", "A07", 7, shared.TwoWay)
	if err := aisles.Save(ctx, a); err != nil {
		t.Fatalf("aisle: %v", err)
	}
	palletRack := mustLocationType(t, placement.PalletRack, 1200, 2.4)
	if err := types.Save(ctx, palletRack); err != nil {
		t.Fatalf("type: %v", err)
	}
	attrs := placement.ZoneAttributes{ZoneID: z.ID(), ZoneCode: z.ZoneCode(), TemperatureClass: z.TemperatureClass()}
	code := mustCode(t, "WH1-STOR-AMB-A07-03-02-B")
	built, err := slot.NewLocationSlot(code, palletRack, shared.Capacity{}, slot.FunctionalAttributes{}, attrs, nil)
	if err != nil {
		t.Fatalf("slot: %v", err)
	}
	if err := slots.Save(ctx, built); err != nil {
		t.Fatalf("save: %v", err)
	}

	t.Run("a freshly registered slot has no handling unit (NULL)", func(t *testing.T) {
		found, err := slots.FindByCode(ctx, code)
		if err != nil {
			t.Fatal(err)
		}
		if found.HandlingUnit() != "" {
			t.Fatalf("HandlingUnit = %q, want none", found.HandlingUnit())
		}
	})

	for _, unit := range []shared.HandlingUnit{shared.HandlingUnitEach, shared.HandlingUnitCase, shared.HandlingUnitPallet} {
		t.Run("declared "+string(unit)+" round-trips", func(t *testing.T) {
			found, err := slots.FindByCode(ctx, code)
			if err != nil {
				t.Fatal(err)
			}
			if err := found.DeclareHandlingUnit(unit); err != nil {
				t.Fatal(err)
			}
			if err := slots.Save(ctx, found); err != nil {
				t.Fatal(err)
			}
			reloaded, err := slots.FindByCode(ctx, code)
			if err != nil {
				t.Fatal(err)
			}
			if reloaded.HandlingUnit() != unit {
				t.Fatalf("HandlingUnit = %q, want %q", reloaded.HandlingUnit(), unit)
			}
		})
	}

	t.Run("the CHECK constraint refuses a value outside EACH/CASE/PALLET", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE location_slots SET handling_unit = 'TRUCK' WHERE code = $1`, code.String()); err == nil {
			t.Fatal("expected the location_slots_handling_unit_check constraint to reject 'TRUCK'")
		}
	})
}
