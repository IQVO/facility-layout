package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
	"github.com/claudioed/facility-layout/internal/domain/slot"
	"github.com/claudioed/facility-layout/internal/domain/structure"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

// errBoom is the injected infrastructure failure. Every use case must
// propagate it verbatim rather than swallowing it or mistaking it for a
// domain rejection.
var errBoom = errors.New("boom: the datastore is unavailable")

// The fault-injecting decorators below wrap the in-memory adapters and fail
// exactly one operation, so each `if err != nil { return err }` branch in
// the application layer is exercised for real.

type faultySiteRepo struct {
	*memory.SiteRepo
	failFind, failSave, failList bool
}

func (r *faultySiteRepo) FindByCode(ctx context.Context, code string) (*site.Site, error) {
	if r.failFind {
		return nil, errBoom
	}
	return r.SiteRepo.FindByCode(ctx, code)
}

func (r *faultySiteRepo) Save(ctx context.Context, s *site.Site) error {
	if r.failSave {
		return errBoom
	}
	return r.SiteRepo.Save(ctx, s)
}

func (r *faultySiteRepo) List(ctx context.Context) ([]*site.Site, error) {
	if r.failList {
		return nil, errBoom
	}
	return r.SiteRepo.List(ctx)
}

type faultyZoneRepo struct {
	*memory.ZoneRepo
	failFind, failSave, failList bool
}

func (r *faultyZoneRepo) FindByID(ctx context.Context, id string) (*zone.Zone, error) {
	if r.failFind {
		return nil, errBoom
	}
	return r.ZoneRepo.FindByID(ctx, id)
}

func (r *faultyZoneRepo) Save(ctx context.Context, z *zone.Zone) error {
	if r.failSave {
		return errBoom
	}
	return r.ZoneRepo.Save(ctx, z)
}

func (r *faultyZoneRepo) ListBySite(ctx context.Context, siteCode string) ([]*zone.Zone, error) {
	if r.failList {
		return nil, errBoom
	}
	return r.ZoneRepo.ListBySite(ctx, siteCode)
}

type faultyAisleRepo struct {
	*memory.AisleRepo
	failFind, failSave, failList bool
}

func (r *faultyAisleRepo) FindByID(ctx context.Context, id string) (*aisle.Aisle, error) {
	if r.failFind {
		return nil, errBoom
	}
	return r.AisleRepo.FindByID(ctx, id)
}

func (r *faultyAisleRepo) Save(ctx context.Context, a *aisle.Aisle) error {
	if r.failSave {
		return errBoom
	}
	return r.AisleRepo.Save(ctx, a)
}

func (r *faultyAisleRepo) ListByZone(ctx context.Context, zoneID string) ([]*aisle.Aisle, error) {
	if r.failList {
		return nil, errBoom
	}
	return r.AisleRepo.ListByZone(ctx, zoneID)
}

type faultySlotRepo struct {
	*memory.SlotRepo
	failFind, failSave, failListByAisle, failListByZone bool
	// failFindOnCall, when positive, fails only the Nth FindByCode call
	// (1-based) — for use cases that look up two different codes, so the
	// second lookup's error branch is reachable independently of the first.
	failFindOnCall int
	findCalls      int
}

func (r *faultySlotRepo) FindByCode(ctx context.Context, code shared.LocationCode) (*slot.LocationSlot, error) {
	if r.failFind {
		return nil, errBoom
	}
	r.findCalls++
	if r.failFindOnCall > 0 && r.findCalls == r.failFindOnCall {
		return nil, errBoom
	}
	return r.SlotRepo.FindByCode(ctx, code)
}

func (r *faultySlotRepo) Save(ctx context.Context, s *slot.LocationSlot) error {
	if r.failSave {
		return errBoom
	}
	return r.SlotRepo.Save(ctx, s)
}

func (r *faultySlotRepo) ListByAisle(ctx context.Context, aisleID string) ([]*slot.LocationSlot, error) {
	if r.failListByAisle {
		return nil, errBoom
	}
	return r.SlotRepo.ListByAisle(ctx, aisleID)
}

func (r *faultySlotRepo) ListByZone(ctx context.Context, zoneID string) ([]*slot.LocationSlot, error) {
	if r.failListByZone {
		return nil, errBoom
	}
	return r.SlotRepo.ListByZone(ctx, zoneID)
}

type faultyLocationTypeRepo struct {
	*memory.LocationTypeRepo
	failFind, failSave, failList bool
}

func (r *faultyLocationTypeRepo) FindByName(ctx context.Context, name string) (*placement.LocationType, error) {
	if r.failFind {
		return nil, errBoom
	}
	return r.LocationTypeRepo.FindByName(ctx, name)
}

func (r *faultyLocationTypeRepo) Save(ctx context.Context, lt placement.LocationType) error {
	if r.failSave {
		return errBoom
	}
	return r.LocationTypeRepo.Save(ctx, lt)
}

func (r *faultyLocationTypeRepo) List(ctx context.Context) ([]placement.LocationType, error) {
	if r.failList {
		return nil, errBoom
	}
	return r.LocationTypeRepo.List(ctx)
}

type faultyRuleRepo struct {
	*memory.PlacementRuleRepo
	failFind, failSave, failList bool
}

func (r *faultyRuleRepo) FindByID(ctx context.Context, id string) (*placement.PlacementRule, error) {
	if r.failFind {
		return nil, errBoom
	}
	return r.PlacementRuleRepo.FindByID(ctx, id)
}

func (r *faultyRuleRepo) Save(ctx context.Context, rule placement.PlacementRule) error {
	if r.failSave {
		return errBoom
	}
	return r.PlacementRuleRepo.Save(ctx, rule)
}

func (r *faultyRuleRepo) List(ctx context.Context) ([]placement.PlacementRule, error) {
	if r.failList {
		return nil, errBoom
	}
	return r.PlacementRuleRepo.List(ctx)
}

// faultyPublisher fails every publish, so the "the write landed but the
// event could not be published" branch is exercised.
type faultyPublisher struct{}

func (faultyPublisher) Publish(context.Context, shared.DomainEvent) error { return errBoom }

type faultyFixedStructureRepo struct {
	*memory.FixedStructureRepo
	failFind, failSave, failList bool
}

func (r *faultyFixedStructureRepo) FindByID(ctx context.Context, id string) (*structure.FixedStructure, error) {
	if r.failFind {
		return nil, errBoom
	}
	return r.FixedStructureRepo.FindByID(ctx, id)
}

func (r *faultyFixedStructureRepo) Save(ctx context.Context, f *structure.FixedStructure) error {
	if r.failSave {
		return errBoom
	}
	return r.FixedStructureRepo.Save(ctx, f)
}

func (r *faultyFixedStructureRepo) ListBySite(ctx context.Context, siteCode string) ([]*structure.FixedStructure, error) {
	if r.failList {
		return nil, errBoom
	}
	return r.FixedStructureRepo.ListBySite(ctx, siteCode)
}

type faultyCrossAisleRepo struct {
	*memory.CrossAisleRepo
	failFind, failSave, failList bool
}

func (r *faultyCrossAisleRepo) FindByAisles(ctx context.Context, zoneID, fromAisle, toAisle, atBay string) (*aisle.CrossAisle, error) {
	if r.failFind {
		return nil, errBoom
	}
	return r.CrossAisleRepo.FindByAisles(ctx, zoneID, fromAisle, toAisle, atBay)
}

func (r *faultyCrossAisleRepo) Save(ctx context.Context, c *aisle.CrossAisle) error {
	if r.failSave {
		return errBoom
	}
	return r.CrossAisleRepo.Save(ctx, c)
}

func (r *faultyCrossAisleRepo) ListByZone(ctx context.Context, zoneID string) ([]*aisle.CrossAisle, error) {
	if r.failList {
		return nil, errBoom
	}
	return r.CrossAisleRepo.ListByZone(ctx, zoneID)
}

// ---------------------------------------------------------------- tests ----

// infrastructureFailureCase couples a scenario name with the wiring + call
// that must surface errBoom.
type infrastructureFailureCase struct {
	name string
	// run wires the failing dependency into its use case and invokes it.
	run func(t *testing.T, h *harness) error
}

func TestUseCasesPropagateInfrastructureFailures(t *testing.T) {
	code := mustCode(t, "WH1-STOR-AMB-A07-03-02-B")
	capacity := mustCapacity(t, 1200, 2.4)
	predicate := mustPredicate(t, "HAZ", "", nil)

	tests := []infrastructureFailureCase{
		{
			name: "RegisterSite: site lookup fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.RegisterSite{Sites: &faultySiteRepo{SiteRepo: h.sites, failFind: true}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1", "Fulfilment Centre One")
				return err
			},
		},
		{
			name: "RegisterSite: save fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.RegisterSite{Sites: &faultySiteRepo{SiteRepo: h.sites, failSave: true}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1", "Fulfilment Centre One")
				return err
			},
		},
		{
			name: "RegisterSite: publish fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.RegisterSite{Sites: h.sites, Events: faultyPublisher{}, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1", "Fulfilment Centre One")
				return err
			},
		},
		{
			name: "GetSite: lookup fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.GetSite{Sites: &faultySiteRepo{SiteRepo: h.sites, failFind: true}}
				_, err := uc.Execute(h.ctx(), "WH1")
				return err
			},
		},
		{
			name: "ListSites: list fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.ListSites{Sites: &faultySiteRepo{SiteRepo: h.sites, failList: true}}
				_, err := uc.Execute(h.ctx())
				return err
			},
		},
		{
			name: "RegisterZone: parent lookup fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.RegisterZone{Sites: &faultySiteRepo{SiteRepo: h.sites, failFind: true}, Zones: h.zones, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1", "STOR", "AMB", shared.Ambient, false)
				return err
			},
		},
		{
			name: "RegisterZone: duplicate check fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				uc := &usecases.RegisterZone{Sites: h.sites, Zones: &faultyZoneRepo{ZoneRepo: h.zones, failFind: true}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1", "STOR", "AMB", shared.Ambient, false)
				return err
			},
		},
		{
			name: "RegisterZone: save fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				uc := &usecases.RegisterZone{Sites: h.sites, Zones: &faultyZoneRepo{ZoneRepo: h.zones, failSave: true}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1", "STOR", "AMB", shared.Ambient, false)
				return err
			},
		},
		{
			name: "RegisterZone: publish fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				uc := &usecases.RegisterZone{Sites: h.sites, Zones: h.zones, Events: faultyPublisher{}, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1", "STOR", "AMB", shared.Ambient, false)
				return err
			},
		},
		{
			name: "ListZones: parent lookup fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.ListZones{Sites: &faultySiteRepo{SiteRepo: h.sites, failFind: true}, Zones: h.zones}
				_, err := uc.Execute(h.ctx(), "WH1")
				return err
			},
		},
		{
			name: "ListZones: list fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				uc := &usecases.ListZones{Sites: h.sites, Zones: &faultyZoneRepo{ZoneRepo: h.zones, failList: true}}
				_, err := uc.Execute(h.ctx(), "WH1")
				return err
			},
		},
		{
			name: "RegisterAisle: parent lookup fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.RegisterAisle{Zones: &faultyZoneRepo{ZoneRepo: h.zones, failFind: true}, Aisles: h.aisles, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1-STOR-AMB", "A07", 7, shared.TwoWay)
				return err
			},
		},
		{
			name: "RegisterAisle: duplicate check fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
				uc := &usecases.RegisterAisle{Zones: h.zones, Aisles: &faultyAisleRepo{AisleRepo: h.aisles, failFind: true}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1-STOR-AMB", "A07", 7, shared.TwoWay)
				return err
			},
		},
		{
			name: "RegisterAisle: save fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
				uc := &usecases.RegisterAisle{Zones: h.zones, Aisles: &faultyAisleRepo{AisleRepo: h.aisles, failSave: true}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1-STOR-AMB", "A07", 7, shared.TwoWay)
				return err
			},
		},
		{
			name: "RegisterAisle: publish fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
				uc := &usecases.RegisterAisle{Zones: h.zones, Aisles: h.aisles, Events: faultyPublisher{}, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1-STOR-AMB", "A07", 7, shared.TwoWay)
				return err
			},
		},
		{
			name: "ListAisles: parent lookup fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.ListAisles{Zones: &faultyZoneRepo{ZoneRepo: h.zones, failFind: true}, Aisles: h.aisles}
				_, err := uc.Execute(h.ctx(), "WH1-STOR-AMB")
				return err
			},
		},
		{
			name: "ListAisles: list fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
				uc := &usecases.ListAisles{Zones: h.zones, Aisles: &faultyAisleRepo{AisleRepo: h.aisles, failList: true}}
				_, err := uc.Execute(h.ctx(), "WH1-STOR-AMB")
				return err
			},
		},
		{
			name: "RegisterLocationType: duplicate check fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.RegisterLocationType{LocationTypes: &faultyLocationTypeRepo{LocationTypeRepo: h.locationTypes, failFind: true}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), placement.PalletRack, placement.Storage, capacity)
				return err
			},
		},
		{
			name: "RegisterLocationType: save fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.RegisterLocationType{LocationTypes: &faultyLocationTypeRepo{LocationTypeRepo: h.locationTypes, failSave: true}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), placement.PalletRack, placement.Storage, capacity)
				return err
			},
		},
		{
			name: "RegisterLocationType: publish fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.RegisterLocationType{LocationTypes: h.locationTypes, Events: faultyPublisher{}, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), placement.PalletRack, placement.Storage, capacity)
				return err
			},
		},
		{
			name: "ListLocationTypes: list fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.ListLocationTypes{LocationTypes: &faultyLocationTypeRepo{LocationTypeRepo: h.locationTypes, failList: true}}
				_, err := uc.Execute(h.ctx())
				return err
			},
		},
		{
			name: "DefinePlacementRule: location type lookup fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.DefinePlacementRule{LocationTypes: &faultyLocationTypeRepo{LocationTypeRepo: h.locationTypes, failFind: true}, Rules: h.rules, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "RULE-1", placement.PalletRack, placement.Allow, predicate)
				return err
			},
		},
		{
			name: "DefinePlacementRule: duplicate check fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
				uc := &usecases.DefinePlacementRule{LocationTypes: h.locationTypes, Rules: &faultyRuleRepo{PlacementRuleRepo: h.rules, failFind: true}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "RULE-1", placement.PalletRack, placement.Allow, predicate)
				return err
			},
		},
		{
			name: "DefinePlacementRule: save fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
				uc := &usecases.DefinePlacementRule{LocationTypes: h.locationTypes, Rules: &faultyRuleRepo{PlacementRuleRepo: h.rules, failSave: true}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "RULE-1", placement.PalletRack, placement.Allow, predicate)
				return err
			},
		},
		{
			name: "DefinePlacementRule: publish fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
				uc := &usecases.DefinePlacementRule{LocationTypes: h.locationTypes, Rules: h.rules, Events: faultyPublisher{}, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "RULE-1", placement.PalletRack, placement.Allow, predicate)
				return err
			},
		},
		{
			name: "ListPlacementRules: list fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.ListPlacementRules{Rules: &faultyRuleRepo{PlacementRuleRepo: h.rules, failList: true}}
				_, err := uc.Execute(h.ctx())
				return err
			},
		},
		{
			name: "RegisterLocationSlot: duplicate check fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				uc := slotUseCase(h, func(uc *usecases.RegisterLocationSlot) {
					uc.Slots = &faultySlotRepo{SlotRepo: h.slots, failFind: true}
				})
				_, err := uc.Execute(h.ctx(), code, placement.PalletRack, shared.Capacity{}, "", nil)
				return err
			},
		},
		{
			name: "RegisterLocationSlot: site lookup fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				uc := slotUseCase(h, func(uc *usecases.RegisterLocationSlot) {
					uc.Sites = &faultySiteRepo{SiteRepo: h.sites, failFind: true}
				})
				_, err := uc.Execute(h.ctx(), code, placement.PalletRack, shared.Capacity{}, "", nil)
				return err
			},
		},
		{
			name: "RegisterLocationSlot: zone lookup fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				uc := slotUseCase(h, func(uc *usecases.RegisterLocationSlot) {
					uc.Zones = &faultyZoneRepo{ZoneRepo: h.zones, failFind: true}
				})
				_, err := uc.Execute(h.ctx(), code, placement.PalletRack, shared.Capacity{}, "", nil)
				return err
			},
		},
		{
			name: "RegisterLocationSlot: aisle lookup fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				uc := slotUseCase(h, func(uc *usecases.RegisterLocationSlot) {
					uc.Aisles = &faultyAisleRepo{AisleRepo: h.aisles, failFind: true}
				})
				_, err := uc.Execute(h.ctx(), code, placement.PalletRack, shared.Capacity{}, "", nil)
				return err
			},
		},
		{
			name: "RegisterLocationSlot: location type lookup fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				uc := slotUseCase(h, func(uc *usecases.RegisterLocationSlot) {
					uc.LocationTypes = &faultyLocationTypeRepo{LocationTypeRepo: h.locationTypes, failFind: true}
				})
				_, err := uc.Execute(h.ctx(), code, placement.PalletRack, shared.Capacity{}, "", nil)
				return err
			},
		},
		{
			name: "RegisterLocationSlot: rule set load fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				uc := slotUseCase(h, func(uc *usecases.RegisterLocationSlot) {
					uc.Rules = &faultyRuleRepo{PlacementRuleRepo: h.rules, failList: true}
				})
				_, err := uc.Execute(h.ctx(), code, placement.PalletRack, shared.Capacity{}, "", nil)
				return err
			},
		},
		{
			name: "RegisterLocationSlot: save fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				uc := slotUseCase(h, func(uc *usecases.RegisterLocationSlot) {
					uc.Slots = &faultySlotRepo{SlotRepo: h.slots, failSave: true}
				})
				_, err := uc.Execute(h.ctx(), code, placement.PalletRack, shared.Capacity{}, "", nil)
				return err
			},
		},
		{
			name: "RegisterLocationSlot: publish fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				uc := slotUseCase(h, func(uc *usecases.RegisterLocationSlot) { uc.Events = faultyPublisher{} })
				_, err := uc.Execute(h.ctx(), code, placement.PalletRack, shared.Capacity{}, "", nil)
				return err
			},
		},
		{
			name: "GetLocationSlot: lookup fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.GetLocationSlot{Slots: &faultySlotRepo{SlotRepo: h.slots, failFind: true}}
				_, err := uc.Execute(h.ctx(), code)
				return err
			},
		},
		{
			name: "GetLocationClassification: slot lookup fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.GetLocationClassification{Slots: &faultySlotRepo{SlotRepo: h.slots, failFind: true}, Zones: h.zones}
				_, err := uc.Execute(h.ctx(), code)
				return err
			},
		},
		{
			name: "GetLocationClassification: zone lookup fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				h.mustRegisterSlot(code.String(), placement.PalletRack)
				uc := &usecases.GetLocationClassification{Slots: h.slots, Zones: &faultyZoneRepo{ZoneRepo: h.zones, failFind: true}}
				_, err := uc.Execute(h.ctx(), code)
				return err
			},
		},
		{
			name: "DecommissionLocationSlot: lookup fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.DecommissionLocationSlot{Slots: &faultySlotRepo{SlotRepo: h.slots, failFind: true}, Events: h.publisher, Clock: h.clock}
				return uc.Execute(h.ctx(), code)
			},
		},
		{
			name: "DecommissionLocationSlot: save fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				h.mustRegisterSlot(code.String(), placement.PalletRack)
				uc := &usecases.DecommissionLocationSlot{Slots: &faultySlotRepo{SlotRepo: h.slots, failSave: true}, Events: h.publisher, Clock: h.clock}
				return uc.Execute(h.ctx(), code)
			},
		},
		{
			name: "DecommissionLocationSlot: publish fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				h.mustRegisterSlot(code.String(), placement.PalletRack)
				uc := &usecases.DecommissionLocationSlot{Slots: h.slots, Events: faultyPublisher{}, Clock: h.clock}
				return uc.Execute(h.ctx(), code)
			},
		},
		{
			name: "GetSiteLayout: site lookup fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.GetSiteLayout{Sites: &faultySiteRepo{SiteRepo: h.sites, failFind: true}, Zones: h.zones, Aisles: h.aisles, Slots: h.slots}
				_, err := uc.Execute(h.ctx(), "WH1")
				return err
			},
		},
		{
			name: "GetSiteLayout: zone list fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				uc := &usecases.GetSiteLayout{Sites: h.sites, Zones: &faultyZoneRepo{ZoneRepo: h.zones, failList: true}, Aisles: h.aisles, Slots: h.slots}
				_, err := uc.Execute(h.ctx(), "WH1")
				return err
			},
		},
		{
			name: "GetSiteLayout: aisle list fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				uc := &usecases.GetSiteLayout{Sites: h.sites, Zones: h.zones, Aisles: &faultyAisleRepo{AisleRepo: h.aisles, failList: true}, Slots: h.slots}
				_, err := uc.Execute(h.ctx(), "WH1")
				return err
			},
		},
		{
			name: "GetSiteLayout: slot list fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				uc := &usecases.GetSiteLayout{Sites: h.sites, Zones: h.zones, Aisles: h.aisles, Slots: &faultySlotRepo{SlotRepo: h.slots, failListByAisle: true}}
				_, err := uc.Execute(h.ctx(), "WH1")
				return err
			},
		},
		{
			name: "GetZoneGrid: zone lookup fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.GetZoneGrid{Zones: &faultyZoneRepo{ZoneRepo: h.zones, failFind: true}, Aisles: h.aisles, Slots: h.slots}
				_, err := uc.Execute(h.ctx(), "WH1-STOR-AMB")
				return err
			},
		},
		{
			name: "GetZoneGrid: aisle list fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				uc := &usecases.GetZoneGrid{Zones: h.zones, Aisles: &faultyAisleRepo{AisleRepo: h.aisles, failList: true}, Slots: h.slots}
				_, err := uc.Execute(h.ctx(), "WH1-STOR-AMB")
				return err
			},
		},
		{
			name: "GetZoneGrid: slot list fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				uc := &usecases.GetZoneGrid{Zones: h.zones, Aisles: h.aisles, Slots: &faultySlotRepo{SlotRepo: h.slots, failListByZone: true}}
				_, err := uc.Execute(h.ctx(), "WH1-STOR-AMB")
				return err
			},
		},
		{
			name: "ImportFacilityLayout: site lookup fails",
			run: func(_ *testing.T, h *harness) error {
				return runImportWithFault(h, func(uc *usecases.ImportFacilityLayout) {
					uc.Sites = &faultySiteRepo{SiteRepo: h.sites, failFind: true}
				})
			},
		},
		{
			name: "ImportFacilityLayout: zone lookup fails",
			run: func(_ *testing.T, h *harness) error {
				return runImportWithFault(h, func(uc *usecases.ImportFacilityLayout) {
					uc.Zones = &faultyZoneRepo{ZoneRepo: h.zones, failFind: true}
				})
			},
		},
		{
			name: "ImportFacilityLayout: aisle lookup fails",
			run: func(_ *testing.T, h *harness) error {
				return runImportWithFault(h, func(uc *usecases.ImportFacilityLayout) {
					uc.Aisles = &faultyAisleRepo{AisleRepo: h.aisles, failFind: true}
				})
			},
		},
		{
			name: "ImportFacilityLayout: site save fails",
			run: func(_ *testing.T, h *harness) error {
				return runImportWithFault(h, func(uc *usecases.ImportFacilityLayout) {
					uc.Sites = &faultySiteRepo{SiteRepo: h.sites, failSave: true}
				})
			},
		},
		{
			name: "ImportFacilityLayout: zone save fails",
			run: func(_ *testing.T, h *harness) error {
				return runImportWithFault(h, func(uc *usecases.ImportFacilityLayout) {
					uc.Zones = &faultyZoneRepo{ZoneRepo: h.zones, failSave: true}
				})
			},
		},
		{
			name: "ImportFacilityLayout: aisle save fails",
			run: func(_ *testing.T, h *harness) error {
				return runImportWithFault(h, func(uc *usecases.ImportFacilityLayout) {
					uc.Aisles = &faultyAisleRepo{AisleRepo: h.aisles, failSave: true}
				})
			},
		},
		{
			name: "ImportFacilityLayout: final event publish fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				uc := importUseCase(h, func(uc *usecases.ImportFacilityLayout) { uc.Events = faultyPublisher{} })
				_, err := uc.Execute(h.ctx(), []usecases.ImportRow{row("STOR", "AMB", shared.Ambient, false, "A07", 7, "03", "02", "B", placement.PalletRack)})
				return err
			},
		},
		{
			name: "SetLocationGeometry: lookup fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.SetLocationGeometry{Slots: &faultySlotRepo{SlotRepo: h.slots, failFind: true}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), code, mustGeomPoint(h.t, 1, 2, 0), mustGeomDimensions(h.t, 1, 1, 1), nil)
				return err
			},
		},
		{
			name: "SetLocationGeometry: save fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				h.mustRegisterSlot(code.String(), placement.PalletRack)
				uc := &usecases.SetLocationGeometry{Slots: &faultySlotRepo{SlotRepo: h.slots, failSave: true}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), code, mustGeomPoint(h.t, 1, 2, 0), mustGeomDimensions(h.t, 1, 1, 1), nil)
				return err
			},
		},
		{
			name: "SetLocationGeometry: publish fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				h.mustRegisterSlot(code.String(), placement.PalletRack)
				uc := &usecases.SetLocationGeometry{Slots: h.slots, Events: faultyPublisher{}, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), code, mustGeomPoint(h.t, 1, 2, 0), mustGeomDimensions(h.t, 1, 1, 1), nil)
				return err
			},
		},
		{
			name: "SetAisleGeometry: lookup fails",
			run: func(_ *testing.T, h *harness) error {
				return runSetAisleGeometryWithFault(h, func(uc *usecases.SetAisleGeometry) {
					uc.Aisles = &faultyAisleRepo{AisleRepo: h.aisles, failFind: true}
				})
			},
		},
		{
			name: "SetAisleGeometry: save fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				return runSetAisleGeometryWithFault(h, func(uc *usecases.SetAisleGeometry) {
					uc.Aisles = &faultyAisleRepo{AisleRepo: h.aisles, failSave: true}
				})
			},
		},
		{
			name: "SetAisleGeometry: publish fails",
			run: func(_ *testing.T, h *harness) error {
				h.seedAmbientAisle()
				return runSetAisleGeometryWithFault(h, func(uc *usecases.SetAisleGeometry) {
					uc.Events = faultyPublisher{}
				})
			},
		},
		{
			name: "RegisterFixedStructure: site lookup fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.RegisterFixedStructure{Sites: &faultySiteRepo{SiteRepo: h.sites, failFind: true}, Structures: h.structures, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "STR-1", "WH1", structure.Wall, mustFootprintForUsecase(h.t), "North wall")
				return err
			},
		},
		{
			name: "RegisterFixedStructure: duplicate check fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				uc := &usecases.RegisterFixedStructure{Sites: h.sites, Structures: &faultyFixedStructureRepo{FixedStructureRepo: h.structures, failFind: true}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "STR-1", "WH1", structure.Wall, mustFootprintForUsecase(h.t), "North wall")
				return err
			},
		},
		{
			name: "RegisterFixedStructure: save fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				uc := &usecases.RegisterFixedStructure{Sites: h.sites, Structures: &faultyFixedStructureRepo{FixedStructureRepo: h.structures, failSave: true}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "STR-1", "WH1", structure.Wall, mustFootprintForUsecase(h.t), "North wall")
				return err
			},
		},
		{
			name: "RegisterFixedStructure: publish fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				uc := &usecases.RegisterFixedStructure{Sites: h.sites, Structures: h.structures, Events: faultyPublisher{}, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "STR-1", "WH1", structure.Wall, mustFootprintForUsecase(h.t), "North wall")
				return err
			},
		},
		{
			name: "ListFixedStructures: site lookup fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.ListFixedStructures{Sites: &faultySiteRepo{SiteRepo: h.sites, failFind: true}, Structures: h.structures}
				_, err := uc.Execute(h.ctx(), "WH1")
				return err
			},
		},
		{
			name: "ListFixedStructures: structure list fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				uc := &usecases.ListFixedStructures{Sites: h.sites, Structures: &faultyFixedStructureRepo{FixedStructureRepo: h.structures, failList: true}}
				_, err := uc.Execute(h.ctx(), "WH1")
				return err
			},
		},
		{
			name: "RegisterCrossAisle: zone lookup fails",
			run: func(_ *testing.T, h *harness) error {
				seedThreeAisleZone(h)
				uc := &usecases.RegisterCrossAisle{Zones: &faultyZoneRepo{ZoneRepo: h.zones, failFind: true}, Aisles: h.aisles, CrossAisles: h.crossAisles, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1-STOR-AMB", "A07", "A08", "02")
				return err
			},
		},
		{
			name: "RegisterCrossAisle: aisle list fails",
			run: func(_ *testing.T, h *harness) error {
				seedThreeAisleZone(h)
				uc := &usecases.RegisterCrossAisle{Zones: h.zones, Aisles: &faultyAisleRepo{AisleRepo: h.aisles, failList: true}, CrossAisles: h.crossAisles, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1-STOR-AMB", "A07", "A08", "02")
				return err
			},
		},
		{
			name: "RegisterCrossAisle: duplicate check fails",
			run: func(_ *testing.T, h *harness) error {
				seedThreeAisleZone(h)
				uc := &usecases.RegisterCrossAisle{Zones: h.zones, Aisles: h.aisles, CrossAisles: &faultyCrossAisleRepo{CrossAisleRepo: h.crossAisles, failFind: true}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1-STOR-AMB", "A07", "A08", "02")
				return err
			},
		},
		{
			name: "RegisterCrossAisle: save fails",
			run: func(_ *testing.T, h *harness) error {
				seedThreeAisleZone(h)
				uc := &usecases.RegisterCrossAisle{Zones: h.zones, Aisles: h.aisles, CrossAisles: &faultyCrossAisleRepo{CrossAisleRepo: h.crossAisles, failSave: true}, Events: h.publisher, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1-STOR-AMB", "A07", "A08", "02")
				return err
			},
		},
		{
			name: "RegisterCrossAisle: publish fails",
			run: func(_ *testing.T, h *harness) error {
				seedThreeAisleZone(h)
				uc := &usecases.RegisterCrossAisle{Zones: h.zones, Aisles: h.aisles, CrossAisles: h.crossAisles, Events: faultyPublisher{}, Clock: h.clock}
				_, err := uc.Execute(h.ctx(), "WH1-STOR-AMB", "A07", "A08", "02")
				return err
			},
		},
		{
			name: "ListLocationsByRole: site lookup fails",
			run: func(_ *testing.T, h *harness) error {
				uc := &usecases.ListLocationsByRole{Sites: &faultySiteRepo{SiteRepo: h.sites, failFind: true}, Zones: h.zones, Slots: h.slots}
				_, err := uc.Execute(h.ctx(), "WH1", placement.Storage)
				return err
			},
		},
		{
			name: "ListLocationsByRole: zone list fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				uc := &usecases.ListLocationsByRole{Sites: h.sites, Zones: &faultyZoneRepo{ZoneRepo: h.zones, failList: true}, Slots: h.slots}
				_, err := uc.Execute(h.ctx(), "WH1", placement.Storage)
				return err
			},
		},
		{
			name: "ListLocationsByRole: slot list fails",
			run: func(_ *testing.T, h *harness) error {
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
				uc := &usecases.ListLocationsByRole{Sites: h.sites, Zones: h.zones, Slots: &faultySlotRepo{SlotRepo: h.slots, failListByZone: true}}
				_, err := uc.Execute(h.ctx(), "WH1", placement.Storage)
				return err
			},
		},
		{
			name: "EstimateTravelDistance: from-slot lookup fails",
			run: func(_ *testing.T, h *harness) error {
				seedThreeAisleZone(h)
				uc := &usecases.EstimateTravelDistance{Zones: h.zones, Aisles: h.aisles, Slots: &faultySlotRepo{SlotRepo: h.slots, failFind: true}, CrossAisles: h.crossAisles}
				_, err := uc.Execute(h.ctx(), mustCode(h.t, "WH1-STOR-AMB-A07-01-01-A"), mustCode(h.t, "WH1-STOR-AMB-A07-03-01-A"))
				return err
			},
		},
		{
			name: "EstimateTravelDistance: to-slot lookup fails",
			run: func(_ *testing.T, h *harness) error {
				seedThreeAisleZone(h)
				uc := &usecases.EstimateTravelDistance{Zones: h.zones, Aisles: h.aisles, Slots: &faultySlotRepo{SlotRepo: h.slots, failFindOnCall: 2}, CrossAisles: h.crossAisles}
				_, err := uc.Execute(h.ctx(), mustCode(h.t, "WH1-STOR-AMB-A07-01-01-A"), mustCode(h.t, "WH1-STOR-AMB-A07-03-01-A"))
				return err
			},
		},
		{
			name: "EstimateTravelDistance: zone lookup fails",
			run: func(_ *testing.T, h *harness) error {
				seedThreeAisleZone(h)
				uc := &usecases.EstimateTravelDistance{Zones: &faultyZoneRepo{ZoneRepo: h.zones, failFind: true}, Aisles: h.aisles, Slots: h.slots, CrossAisles: h.crossAisles}
				_, err := uc.Execute(h.ctx(), mustCode(h.t, "WH1-STOR-AMB-A07-01-01-A"), mustCode(h.t, "WH1-STOR-AMB-A07-03-01-A"))
				return err
			},
		},
		{
			name: "EstimateTravelDistance: aisle list fails",
			run: func(_ *testing.T, h *harness) error {
				seedThreeAisleZone(h)
				uc := &usecases.EstimateTravelDistance{Zones: h.zones, Aisles: &faultyAisleRepo{AisleRepo: h.aisles, failList: true}, Slots: h.slots, CrossAisles: h.crossAisles}
				_, err := uc.Execute(h.ctx(), mustCode(h.t, "WH1-STOR-AMB-A07-01-01-A"), mustCode(h.t, "WH1-STOR-AMB-A07-03-01-A"))
				return err
			},
		},
		{
			name: "EstimateTravelDistance: slot list fails",
			run: func(_ *testing.T, h *harness) error {
				seedThreeAisleZone(h)
				uc := &usecases.EstimateTravelDistance{Zones: h.zones, Aisles: h.aisles, Slots: &faultySlotRepo{SlotRepo: h.slots, failListByZone: true}, CrossAisles: h.crossAisles}
				_, err := uc.Execute(h.ctx(), mustCode(h.t, "WH1-STOR-AMB-A07-01-01-A"), mustCode(h.t, "WH1-STOR-AMB-A07-03-01-A"))
				return err
			},
		},
		{
			name: "EstimateTravelDistance: cross-aisle list fails",
			run: func(_ *testing.T, h *harness) error {
				seedThreeAisleZone(h)
				uc := &usecases.EstimateTravelDistance{Zones: h.zones, Aisles: h.aisles, Slots: h.slots, CrossAisles: &faultyCrossAisleRepo{CrossAisleRepo: h.crossAisles, failList: true}}
				_, err := uc.Execute(h.ctx(), mustCode(h.t, "WH1-STOR-AMB-A07-01-01-A"), mustCode(h.t, "WH1-STOR-AMB-A07-03-01-A"))
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			err := tc.run(t, h)
			if err == nil {
				t.Fatal("expected the infrastructure failure to propagate, got nil")
			}
			if !errors.Is(err, errBoom) && err.Error() != errBoom.Error() {
				t.Fatalf("expected the failure to surface as %v, got %v", errBoom, err)
			}
		})
	}
}

// runImportWithFault runs a one-row import with one dependency faulted, so a
// row-level failure surfaces through report.Results[0] rather than Execute's
// own error. The PalletRack location type the row needs is registered first.
func runImportWithFault(h *harness, inject func(*usecases.ImportFacilityLayout)) error {
	h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
	uc := importUseCase(h, inject)
	report, err := uc.Execute(h.ctx(), []usecases.ImportRow{row("STOR", "AMB", shared.Ambient, false, "A07", 7, "03", "02", "B", placement.PalletRack)})
	if err != nil {
		return err
	}
	return errors.New(report.Results[0].Error)
}

// runSetAisleGeometryWithFault sets a valid A07 centreline with one
// dependency faulted, so the fault surfaces from Execute.
func runSetAisleGeometryWithFault(h *harness, inject func(*usecases.SetAisleGeometry)) error {
	centreline, err := shared.NewSegment(mustGeomPoint(h.t, 0, 0, 0), mustGeomPoint(h.t, 10, 0, 0))
	if err != nil {
		return err
	}
	uc := &usecases.SetAisleGeometry{Aisles: h.aisles, Events: h.publisher, Clock: h.clock}
	inject(uc)
	_, err = uc.Execute(h.ctx(), "WH1-STOR-AMB-A07", centreline)
	return err
}

// slotUseCase builds a fully wired RegisterLocationSlot over the harness's
// adapters, then applies the caller's fault injection.
func slotUseCase(h *harness, inject func(*usecases.RegisterLocationSlot)) *usecases.RegisterLocationSlot {
	uc := &usecases.RegisterLocationSlot{
		Sites: h.sites, Zones: h.zones, Aisles: h.aisles, Slots: h.slots,
		LocationTypes: h.locationTypes, Rules: h.rules, Events: h.publisher, Clock: h.clock,
	}
	inject(uc)
	return uc
}

// importUseCase does the same for ImportFacilityLayout.
func importUseCase(h *harness, inject func(*usecases.ImportFacilityLayout)) *usecases.ImportFacilityLayout {
	uc := &usecases.ImportFacilityLayout{
		Sites: h.sites, Zones: h.zones, Aisles: h.aisles, Slots: h.slots,
		LocationTypes: h.locationTypes, Rules: h.rules, Events: h.publisher, Clock: h.clock,
	}
	inject(uc)
	return uc
}
