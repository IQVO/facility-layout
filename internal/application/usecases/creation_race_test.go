package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
	"github.com/claudioed/facility-layout/internal/domain/slot"
	"github.com/claudioed/facility-layout/internal/domain/structure"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

// The ADR 0047 contract at the application layer: when a creation write
// loses the natural-key race (the repository answers
// ports.ErrNaturalKeyTaken — Postgres reaches it through INSERT ... ON
// CONFLICT DO NOTHING or the version guard), EVERY register use case maps
// it to its documented duplicate refusal, exactly as the pre-write
// existence check already does sequentially.
//
// The racing wrappers below simulate the losing side of the race: reads
// see nothing (the winner has not committed yet), the creation write
// refuses.

type racingSiteRepo struct{ *memory.SiteRepo }

func (r *racingSiteRepo) Save(context.Context, *site.Site) error { return ports.ErrNaturalKeyTaken }

type racingZoneRepo struct{ *memory.ZoneRepo }

func (r *racingZoneRepo) Create(context.Context, *zone.Zone) error { return ports.ErrNaturalKeyTaken }

type racingAisleRepo struct{ *memory.AisleRepo }

func (r *racingAisleRepo) Create(context.Context, *aisle.Aisle) error {
	return ports.ErrNaturalKeyTaken
}

type racingCrossAisleRepo struct{ *memory.CrossAisleRepo }

func (r *racingCrossAisleRepo) Create(context.Context, *aisle.CrossAisle) error {
	return ports.ErrNaturalKeyTaken
}

type racingStructureRepo struct{ *memory.FixedStructureRepo }

func (r *racingStructureRepo) Create(context.Context, *structure.FixedStructure) error {
	return ports.ErrNaturalKeyTaken
}

type racingTypeRepo struct{ *memory.LocationTypeRepo }

func (r *racingTypeRepo) Create(context.Context, placement.LocationType) error {
	return ports.ErrNaturalKeyTaken
}

type racingRuleRepo struct{ *memory.PlacementRuleRepo }

func (r *racingRuleRepo) Create(context.Context, placement.PlacementRule) error {
	return ports.ErrNaturalKeyTaken
}

type racingSlotRepo struct{ *memory.SlotRepo }

func (r *racingSlotRepo) Save(context.Context, *slot.LocationSlot) error {
	return ports.ErrNaturalKeyTaken
}

func TestLostCreationRaceIsTheDuplicateRefusal(t *testing.T) {
	code := mustCode(t, "WH1-STOR-AMB-A07-03-02-B")
	capacity := mustCapacity(t, 1200, 2.4)
	origin, errOrigin := shared.NewPoint3D(0, 0, 0)
	if errOrigin != nil {
		t.Fatalf("build origin: %v", errOrigin)
	}
	size, errSize := shared.NewDimensions(1, 1, 1)
	if errSize != nil {
		t.Fatalf("build size: %v", errSize)
	}
	footprint, errRect := shared.NewRect(origin, size)
	if errRect != nil {
		t.Fatalf("build footprint: %v", errRect)
	}
	predicate := mustPredicate(t, "HAZ", "", nil)

	tests := []struct {
		name string
		want error
		run  func(h *harness) error
	}{
		{
			name: "RegisterSite answers duplicate-site-code",
			want: usecases.ErrDuplicateSite,
			run: func(h *harness) error {
				uc := &usecases.RegisterSite{Sites: &racingSiteRepo{SiteRepo: h.sites}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1", "Fulfilment Centre One", site.RoleUnspecified)
				return err
			},
		},
		{
			name: "RegisterZone answers duplicate-zone",
			want: usecases.ErrDuplicateZone,
			run: func(h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				uc := &usecases.RegisterZone{Sites: h.sites, Zones: &racingZoneRepo{ZoneRepo: h.zones}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1", "STOR", "AMB", shared.Ambient, false, nil, nil)
				return err
			},
		},
		{
			name: "RegisterAisle answers duplicate-aisle",
			want: usecases.ErrDuplicateAisle,
			run: func(h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
				uc := &usecases.RegisterAisle{Zones: h.zones, Aisles: &racingAisleRepo{AisleRepo: h.aisles}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1-STOR-AMB", "A07", 7, shared.TwoWay)
				return err
			},
		},
		{
			name: "RegisterCrossAisle answers duplicate-cross-aisle",
			want: usecases.ErrDuplicateCrossAisle,
			run: func(h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
				h.mustRegisterAisle("WH1-STOR-AMB", "A01", 1, shared.TwoWay)
				h.mustRegisterAisle("WH1-STOR-AMB", "A02", 2, shared.TwoWay)
				uc := &usecases.RegisterCrossAisle{
					Zones: h.zones, Aisles: h.aisles,
					CrossAisles: &racingCrossAisleRepo{CrossAisleRepo: h.crossAisles},
					Events:      h.publisher, Clock: h.clock,
				}
				_, err := uc.Execute(h.ctx(), "WH1-STOR-AMB", "A01", "A02", "03")
				return err
			},
		},
		{
			name: "RegisterLocationType answers duplicate-location-type",
			want: usecases.ErrDuplicateLocationType,
			run: func(h *harness) error {
				uc := &usecases.RegisterLocationType{LocationTypes: &racingTypeRepo{LocationTypeRepo: h.locationTypes}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "PalletRack", placement.Storage, capacity)
				return err
			},
		},
		{
			name: "DefinePlacementRule answers duplicate-placement-rule",
			want: usecases.ErrDuplicatePlacementRule,
			run: func(h *harness) error {
				h.mustRegisterLocationType("PalletRack", 1200, 2.4)
				uc := &usecases.DefinePlacementRule{LocationTypes: h.locationTypes, Rules: &racingRuleRepo{PlacementRuleRepo: h.rules}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "rule-1", "PalletRack", placement.Deny, predicate)
				return err
			},
		},
		{
			name: "RegisterFixedStructure answers duplicate-fixed-structure",
			want: usecases.ErrDuplicateFixedStructure,
			run: func(h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				uc := &usecases.RegisterFixedStructure{Sites: h.sites, Structures: &racingStructureRepo{FixedStructureRepo: h.structures}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "wall-1", "WH1", structure.Wall, footprint, "North wall")
				return err
			},
		},
		{
			name: "RegisterLocationSlot answers duplicate-location-code",
			want: usecases.ErrDuplicateLocationCode,
			run: func(h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
				h.mustRegisterAisle("WH1-STOR-AMB", "A07", 7, shared.TwoWay)
				h.mustRegisterLocationType("PalletRack", 1200, 2.4)
				uc := &usecases.RegisterLocationSlot{
					Sites: h.sites, Zones: h.zones, Aisles: h.aisles,
					Slots:         &racingSlotRepo{SlotRepo: h.slots},
					LocationTypes: h.locationTypes, Rules: h.rules,
					Events: h.publisher, Clock: h.clock,
				}
				_, err := uc.Execute(h.ctx(), code, "PalletRack", shared.Capacity{}, "", nil)
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if err := tc.run(h); !isErr(err, tc.want) {
				t.Fatalf("err = %v, want %v (the lost creation race is the duplicate refusal, ADR 0047)", err, tc.want)
			}
		})
	}
}

// lateSiteRepo stages the import's losing side: the first read sees no
// site (the winner has not committed), the creation write refuses, and the
// re-read then finds the winner's row — committed.
type lateSiteRepo struct {
	*memory.SiteRepo
	raced *site.Site
	reads int
}

func (r *lateSiteRepo) FindByCode(ctx context.Context, code string) (*site.Site, error) {
	r.reads++
	if r.reads == 1 {
		return nil, nil
	}
	return r.raced, nil
}

func (r *lateSiteRepo) Save(context.Context, *site.Site) error { return ports.ErrNaturalKeyTaken }

// An import ENSURES the chain exists: a parent another request committed
// between the read and the insert is adopted (ADR 0047), so the row still
// imports instead of failing (or, before ADR 0047, overwriting the
// winner's row).
func TestImportAdoptsAParentThatLostItsCreationRace(t *testing.T) {
	h := newHarness(t)
	winner, err := site.NewSite("WH1", "Imported elsewhere")
	if err != nil {
		t.Fatalf("build winner: %v", err)
	}
	row := usecases.ImportRow{
		SiteCode: "WH1", AreaCode: "STOR", ZoneCode: "AMB",
		TemperatureClass: shared.Ambient, AisleCode: "A07", SequenceHint: 7,
		Bay: "03", Level: "02", Position: "B", LocationType: "PalletRack",
	}
	h.mustRegisterLocationType("PalletRack", 1200, 2.4)
	uc := &usecases.ImportFacilityLayout{
		Sites: &lateSiteRepo{SiteRepo: h.sites, raced: winner}, Zones: h.zones, Aisles: h.aisles,
		Slots: h.slots, LocationTypes: h.locationTypes, Rules: h.rules,
		Events: h.publisher, Clock: h.clock,
	}

	report, err := uc.Execute(h.ctx(), []usecases.ImportRow{row})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if report.RowsRejected != 0 || report.SlotsImported != 1 {
		t.Fatalf("rejected = %d, imported = %d, want 0/1 (the raced parent is adopted)", report.RowsRejected, report.SlotsImported)
	}
}

type aisleTakenSlotRepo struct{ *memory.SlotRepo }

func (r *aisleTakenSlotRepo) Save(context.Context, *slot.LocationSlot) error {
	return ports.ErrAutomatedStorageAisleTaken
}

// lostCreationRace maps only ports.ErrNaturalKeyTaken: any other Save error
// passes through, so the AS/RS-per-aisle index violation (ADR-0048) reaches
// the caller as ErrAutomatedStorageAlreadyInAisle, not as a duplicate code.
func TestLostCreationRacePassesOtherSaveErrorsThrough(t *testing.T) {
	h := newHarness(t)
	h.seedASRS()
	uc := &usecases.RegisterLocationSlot{
		Sites: h.sites, Zones: h.zones, Aisles: h.aisles,
		Slots:         &aisleTakenSlotRepo{SlotRepo: h.slots},
		LocationTypes: h.locationTypes, Rules: h.rules,
		Events: h.publisher, Clock: h.clock,
	}
	_, err := uc.Execute(h.ctx(), mustCode(t, asrsStore), placement.AutomatedStorage, shared.Capacity{}, "", nil)
	if !errors.Is(err, usecases.ErrAutomatedStorageAlreadyInAisle) || errors.Is(err, usecases.ErrDuplicateLocationCode) {
		t.Fatalf("err = %v, want the AS/RS-per-aisle refusal passed through", err)
	}
}
