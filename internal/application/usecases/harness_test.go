package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// fixedNow is the deterministic clock every use case test runs against.
var fixedNow = time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC)

// harness is the whole application layer wired over the in-memory adapters:
// the same composition the composition root builds, minus Postgres.
type harness struct {
	t *testing.T

	sites         *memory.SiteRepo
	zones         *memory.ZoneRepo
	aisles        *memory.AisleRepo
	slots         *memory.SlotRepo
	locationTypes *memory.LocationTypeRepo
	rules         *memory.PlacementRuleRepo
	structures    *memory.FixedStructureRepo
	crossAisles   *memory.CrossAisleRepo
	publisher     *events.BufferedPublisher
	clock         *memory.FixedClock
	metrics       *recordingMetrics

	registerSite              *usecases.RegisterSite
	getSite                   *usecases.GetSite
	listSites                 *usecases.ListSites
	registerZone              *usecases.RegisterZone
	listZones                 *usecases.ListZones
	registerAisle             *usecases.RegisterAisle
	listAisles                *usecases.ListAisles
	registerLocationType      *usecases.RegisterLocationType
	listLocationTypes         *usecases.ListLocationTypes
	definePlacementRule       *usecases.DefinePlacementRule
	listPlacementRules        *usecases.ListPlacementRules
	registerSlot              *usecases.RegisterLocationSlot
	getSlot                   *usecases.GetLocationSlot
	getLocationClassification *usecases.GetLocationClassification
	decommissionSlot          *usecases.DecommissionLocationSlot
	importLayout              *usecases.ImportFacilityLayout
	getSiteLayout             *usecases.GetSiteLayout
	getZoneGrid               *usecases.GetZoneGrid
	setLocationGeometry       *usecases.SetLocationGeometry
	setAisleGeometry          *usecases.SetAisleGeometry
	registerFixedStructure    *usecases.RegisterFixedStructure
	listFixedStructures       *usecases.ListFixedStructures
	registerCrossAisle        *usecases.RegisterCrossAisle
	getZoneTravelGraph        *usecases.GetZoneTravelGraph
	estimateTravelDistance    *usecases.EstimateTravelDistance
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	h := &harness{
		t:             t,
		sites:         memory.NewSiteRepo(),
		zones:         memory.NewZoneRepo(),
		aisles:        memory.NewAisleRepo(),
		slots:         memory.NewSlotRepo(),
		locationTypes: memory.NewLocationTypeRepo(),
		rules:         memory.NewPlacementRuleRepo(),
		structures:    memory.NewFixedStructureRepo(),
		crossAisles:   memory.NewCrossAisleRepo(),
		publisher:     events.NewBufferedPublisher(),
		clock:         memory.NewFixedClock(fixedNow),
		metrics:       &recordingMetrics{},
	}

	h.registerSite = &usecases.RegisterSite{Sites: h.sites, Events: h.publisher, Clock: h.clock}
	h.getSite = &usecases.GetSite{Sites: h.sites}
	h.listSites = &usecases.ListSites{Sites: h.sites}
	h.registerZone = &usecases.RegisterZone{Sites: h.sites, Zones: h.zones, Events: h.publisher, Clock: h.clock}
	h.listZones = &usecases.ListZones{Sites: h.sites, Zones: h.zones}
	h.registerAisle = &usecases.RegisterAisle{Zones: h.zones, Aisles: h.aisles, Events: h.publisher, Clock: h.clock}
	h.listAisles = &usecases.ListAisles{Zones: h.zones, Aisles: h.aisles}
	h.registerLocationType = &usecases.RegisterLocationType{LocationTypes: h.locationTypes, Events: h.publisher, Clock: h.clock}
	h.listLocationTypes = &usecases.ListLocationTypes{LocationTypes: h.locationTypes}
	h.definePlacementRule = &usecases.DefinePlacementRule{LocationTypes: h.locationTypes, Rules: h.rules, Events: h.publisher, Clock: h.clock}
	h.listPlacementRules = &usecases.ListPlacementRules{Rules: h.rules}
	h.registerSlot = &usecases.RegisterLocationSlot{
		Sites: h.sites, Zones: h.zones, Aisles: h.aisles, Slots: h.slots,
		LocationTypes: h.locationTypes, Rules: h.rules, Events: h.publisher, Clock: h.clock,
		Metrics: h.metrics,
	}
	h.getSlot = &usecases.GetLocationSlot{Slots: h.slots}
	h.getLocationClassification = &usecases.GetLocationClassification{Slots: h.slots, Zones: h.zones}
	h.decommissionSlot = &usecases.DecommissionLocationSlot{Slots: h.slots, Events: h.publisher, Clock: h.clock}
	h.importLayout = &usecases.ImportFacilityLayout{
		Sites: h.sites, Zones: h.zones, Aisles: h.aisles, Slots: h.slots,
		LocationTypes: h.locationTypes, Rules: h.rules, Events: h.publisher, Clock: h.clock,
		Metrics: h.metrics,
	}
	h.getSiteLayout = &usecases.GetSiteLayout{Sites: h.sites, Zones: h.zones, Aisles: h.aisles, Slots: h.slots, Structures: h.structures}
	h.getZoneGrid = &usecases.GetZoneGrid{Zones: h.zones, Aisles: h.aisles, Slots: h.slots}
	h.setLocationGeometry = &usecases.SetLocationGeometry{Slots: h.slots, Events: h.publisher, Clock: h.clock}
	h.setAisleGeometry = &usecases.SetAisleGeometry{Aisles: h.aisles, Events: h.publisher, Clock: h.clock}
	h.registerFixedStructure = &usecases.RegisterFixedStructure{Sites: h.sites, Structures: h.structures, Events: h.publisher, Clock: h.clock}
	h.listFixedStructures = &usecases.ListFixedStructures{Sites: h.sites, Structures: h.structures}
	h.registerCrossAisle = &usecases.RegisterCrossAisle{
		Zones: h.zones, Aisles: h.aisles, CrossAisles: h.crossAisles, Events: h.publisher, Clock: h.clock,
	}
	h.getZoneTravelGraph = &usecases.GetZoneTravelGraph{
		Zones: h.zones, Aisles: h.aisles, Slots: h.slots, CrossAisles: h.crossAisles,
	}
	h.estimateTravelDistance = &usecases.EstimateTravelDistance{
		Zones: h.zones, Aisles: h.aisles, Slots: h.slots, CrossAisles: h.crossAisles,
	}

	return h
}

func (h *harness) ctx() context.Context { return context.Background() }

// seedAmbientAisle registers the canonical WH1 / STOR / AMB / A07 chain plus
// a PalletRack location type, the setup nearly every slot test needs.
func (h *harness) seedAmbientAisle() {
	h.t.Helper()
	h.mustRegisterSite("WH1", "Fulfilment Centre One")
	h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
	h.mustRegisterAisle("WH1-STOR-AMB", "A07", 7, shared.TwoWay)
	h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
}

func (h *harness) mustRegisterSite(code, name string) {
	h.t.Helper()
	if _, err := h.registerSite.Execute(h.ctx(), code, name); err != nil {
		h.t.Fatalf("seeding site %q: %v", code, err)
	}
}

func (h *harness) mustRegisterZone(siteCode, areaCode, zoneCode string, tc shared.TemperatureClass, hazmat bool) {
	h.t.Helper()
	if _, err := h.registerZone.Execute(h.ctx(), siteCode, areaCode, zoneCode, tc, hazmat, nil, nil); err != nil {
		h.t.Fatalf("seeding zone %s-%s-%s: %v", siteCode, areaCode, zoneCode, err)
	}
}

func (h *harness) mustRegisterAisle(zoneID, aisleCode string, sequenceHint int, direction shared.Direction) {
	h.t.Helper()
	if _, err := h.registerAisle.Execute(h.ctx(), zoneID, aisleCode, sequenceHint, direction); err != nil {
		h.t.Fatalf("seeding aisle %s-%s: %v", zoneID, aisleCode, err)
	}
}

func (h *harness) mustRegisterLocationType(name string, weight, volume float64) {
	h.t.Helper()
	if _, err := h.registerLocationType.Execute(h.ctx(), name, placement.Storage, mustCapacity(h.t, weight, volume)); err != nil {
		h.t.Fatalf("seeding location type %q: %v", name, err)
	}
}

func (h *harness) mustRegisterSlot(raw, locationType string) {
	h.t.Helper()
	if _, err := h.registerSlot.Execute(h.ctx(), mustCode(h.t, raw), locationType, shared.Capacity{}, "", nil); err != nil {
		h.t.Fatalf("seeding slot %q: %v", raw, err)
	}
}

// publishedEvents returns the CloudEvents type of every event published so far.
func (h *harness) publishedEventNames() []string {
	h.t.Helper()
	published := h.publisher.Events()
	names := make([]string, 0, len(published))
	for _, e := range published {
		names = append(names, e.EventName())
	}
	return names
}

func (h *harness) assertPublished(name string) {
	h.t.Helper()
	for _, published := range h.publishedEventNames() {
		if published == name {
			return
		}
	}
	h.t.Fatalf("expected domain event %q to be published, got %v", name, h.publishedEventNames())
}

func (h *harness) assertNotPublished(name string) {
	h.t.Helper()
	for _, published := range h.publishedEventNames() {
		if published == name {
			h.t.Fatalf("expected domain event %q NOT to be published, got %v", name, h.publishedEventNames())
		}
	}
}

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

func mustPredicate(t *testing.T, zoneCode string, tc shared.TemperatureClass, hazmat *bool) placement.ZonePredicate {
	t.Helper()
	p, err := placement.NewZonePredicate(zoneCode, tc, hazmat)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return p
}

func assertErrorIs(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("expected error %v, got %v", want, err)
	}
}

// recordingMetrics is the test double for ports.LocationMetrics: it just
// remembers the outcome attribute of every registration attempt, so a test
// can assert what the service would have counted.
type recordingMetrics struct {
	outcomes []string
}

func (m *recordingMetrics) LocationSlotRegistered(_ context.Context, outcome string) {
	m.outcomes = append(m.outcomes, outcome)
}
