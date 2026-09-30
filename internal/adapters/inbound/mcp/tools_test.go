package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// base is the deterministic clock every mcp test runs against.
var base = time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC)

// harness wires the read use cases the MCP adapter needs over in-memory
// repos, seeding structure through the REAL write use cases so the read
// models under test are built from genuinely-registered aggregates.
type harness struct {
	t *testing.T

	sites         *memory.SiteRepo
	zones         *memory.ZoneRepo
	aisles        *memory.AisleRepo
	slots         *memory.SlotRepo
	locationTypes *memory.LocationTypeRepo
	rules         *memory.PlacementRuleRepo
	crossAisles   *memory.CrossAisleRepo
	clock         *memory.FixedClock

	registerSite         *usecases.RegisterSite
	registerZone         *usecases.RegisterZone
	registerAisle        *usecases.RegisterAisle
	registerLocationType *usecases.RegisterLocationType
	registerSlot         *usecases.RegisterLocationSlot
	registerCrossAisle   *usecases.RegisterCrossAisle

	deps Deps
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	sites := memory.NewSiteRepo()
	zones := memory.NewZoneRepo()
	aisles := memory.NewAisleRepo()
	slots := memory.NewSlotRepo()
	locationTypes := memory.NewLocationTypeRepo()
	rules := memory.NewPlacementRuleRepo()
	crossAisles := memory.NewCrossAisleRepo()
	publisher := events.NewBufferedPublisher()
	clock := memory.NewFixedClock(base)

	h := &harness{
		t:             t,
		sites:         sites,
		zones:         zones,
		aisles:        aisles,
		slots:         slots,
		locationTypes: locationTypes,
		rules:         rules,
		crossAisles:   crossAisles,
		clock:         clock,

		registerSite:         &usecases.RegisterSite{Sites: sites, Events: publisher, Clock: clock},
		registerZone:         &usecases.RegisterZone{Sites: sites, Zones: zones, Events: publisher, Clock: clock},
		registerAisle:        &usecases.RegisterAisle{Zones: zones, Aisles: aisles, Events: publisher, Clock: clock},
		registerLocationType: &usecases.RegisterLocationType{LocationTypes: locationTypes, Events: publisher, Clock: clock},
		registerSlot: &usecases.RegisterLocationSlot{
			Sites: sites, Zones: zones, Aisles: aisles, Slots: slots,
			LocationTypes: locationTypes, Rules: rules, Events: publisher, Clock: clock,
		},
		registerCrossAisle: &usecases.RegisterCrossAisle{
			Zones: zones, Aisles: aisles, CrossAisles: crossAisles, Events: publisher, Clock: clock,
		},
	}
	h.deps = Deps{
		GetSiteLayout:       &usecases.GetSiteLayout{Sites: sites, Zones: zones, Aisles: aisles, Slots: slots},
		GetZoneGrid:         &usecases.GetZoneGrid{Zones: zones, Aisles: aisles, Slots: slots},
		ListSites:           &usecases.ListSites{Sites: sites},
		ListLocationsByRole: &usecases.ListLocationsByRole{Sites: sites, Zones: zones, Slots: slots},
		GetZoneTravelGraph: &usecases.GetZoneTravelGraph{
			Zones: zones, Aisles: aisles, Slots: slots, CrossAisles: crossAisles,
		},
		EstimateTravelDistance: &usecases.EstimateTravelDistance{
			Zones: zones, Aisles: aisles, Slots: slots, CrossAisles: crossAisles,
		},
	}
	return h
}

func (h *harness) ctx() context.Context { return context.Background() }

// seedDrawableSite builds a small but realistic WH1: two zones, three aisles
// registered out of walk order, and a handful of slots — mirrors the
// application-layer read-model test fixture.
func (h *harness) seedDrawableSite() {
	h.t.Helper()
	h.mustRegisterSite("WH1", "Fulfilment Centre One")
	h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
	h.mustRegisterLocationType(placement.Shelf, 60, 0.4)

	h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
	h.mustRegisterZone("WH1", "RCV", "AMB", shared.Ambient, false)

	// Registered deliberately out of walk order: A09 first, then A07.
	h.mustRegisterAisle("WH1-STOR-AMB", "A09", 9, shared.OneWay)
	h.mustRegisterAisle("WH1-STOR-AMB", "A07", 7, shared.TwoWay)
	h.mustRegisterAisle("WH1-RCV-AMB", "D01", 1, shared.TwoWay)

	h.mustRegisterSlot("WH1-STOR-AMB-A07-03-02-B", placement.PalletRack)
	h.mustRegisterSlot("WH1-STOR-AMB-A07-03-02-A", placement.PalletRack)
	h.mustRegisterSlot("WH1-STOR-AMB-A07-03-01-A", placement.PalletRack)
	h.mustRegisterSlot("WH1-STOR-AMB-A09-01-01-A", placement.Shelf)
	h.mustRegisterSlot("WH1-RCV-AMB-D01-01-01-A", placement.Shelf)
}

func (h *harness) mustRegisterSite(code, name string) {
	h.t.Helper()
	if _, err := h.registerSite.Execute(h.ctx(), code, name); err != nil {
		h.t.Fatalf("seeding site %q: %v", code, err)
	}
}

func (h *harness) mustRegisterZone(siteCode, areaCode, zoneCode string, tc shared.TemperatureClass, hazmat bool) {
	h.t.Helper()
	if _, err := h.registerZone.Execute(h.ctx(), siteCode, areaCode, zoneCode, tc, hazmat); err != nil {
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
	capacity, err := shared.NewCapacity(weight, volume)
	if err != nil {
		h.t.Fatalf("capacity: %v", err)
	}
	if _, err := h.registerLocationType.Execute(h.ctx(), name, placement.Storage, capacity); err != nil {
		h.t.Fatalf("seeding location type %q: %v", name, err)
	}
}

func (h *harness) mustRegisterSlot(raw, locationType string) {
	h.t.Helper()
	code, err := shared.ParseLocationCode(raw)
	if err != nil {
		h.t.Fatalf("parse code %q: %v", raw, err)
	}
	if _, err := h.registerSlot.Execute(h.ctx(), code, locationType, shared.Capacity{}, "", nil); err != nil {
		h.t.Fatalf("seeding slot %q: %v", raw, err)
	}
}

// mustRegisterDockLocationType registers a role-typed LocationType with no
// capacity envelope, for functional-location tests (ADR-0016).
func (h *harness) mustRegisterDockLocationType(name string, role placement.LocationRole) {
	h.t.Helper()
	if _, err := h.registerLocationType.Execute(h.ctx(), name, role, shared.Capacity{}); err != nil {
		h.t.Fatalf("seeding location type %q: %v", name, err)
	}
}

// mustRegisterFunctionalSlot registers a slot with role-conditional
// dockFlow/activities, for functional-location tests (ADR-0016).
func (h *harness) mustRegisterFunctionalSlot(raw, locationType, dockFlow string, activities []string) {
	h.t.Helper()
	code, err := shared.ParseLocationCode(raw)
	if err != nil {
		h.t.Fatalf("parse code %q: %v", raw, err)
	}
	if _, err := h.registerSlot.Execute(h.ctx(), code, locationType, shared.Capacity{}, dockFlow, activities); err != nil {
		h.t.Fatalf("seeding functional slot %q: %v", raw, err)
	}
}

func TestListSites(t *testing.T) {
	h := newHarness(t)

	// Empty map -> empty, non-nil list.
	empty, err := h.deps.listSites(h.ctx(), listSitesInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(empty.Sites) != 0 {
		t.Fatalf("expected no sites, got %+v", empty.Sites)
	}

	h.mustRegisterSite("WH1", "Fulfilment Centre One")
	h.mustRegisterSite("WH2", "Fulfilment Centre Two")

	out, err := h.deps.listSites(h.ctx(), listSitesInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Sites) != 2 {
		t.Fatalf("expected 2 sites, got %d", len(out.Sites))
	}
	// Repo returns them ordered by code.
	if out.Sites[0].Code != "WH1" || out.Sites[0].Name != "Fulfilment Centre One" {
		t.Fatalf("unexpected first site %+v", out.Sites[0])
	}
	if out.Sites[1].Code != "WH2" {
		t.Fatalf("unexpected second site %+v", out.Sites[1])
	}
}

// siteLayoutCase is one row of the getSiteLayout table: either an input the
// tool must reject (wantErr) or one whose DTO must satisfy assert.
type siteLayoutCase struct {
	name     string
	siteCode string
	wantErr  bool
	assert   func(t *testing.T, out siteLayoutDTO)
}

func TestGetSiteLayout(t *testing.T) {
	tests := []siteLayoutCase{
		{
			name:     "empty siteCode rejected",
			siteCode: "",
			wantErr:  true,
		},
		{
			name:     "unknown site rejected",
			siteCode: "NOPE",
			wantErr:  true,
		},
		{
			name:     "full nested drawable structure",
			siteCode: "WH1",
			assert:   assertMCPSiteLayout,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.seedDrawableSite()
			out, err := h.deps.getSiteLayout(h.ctx(), siteLayoutInput{SiteCode: tc.siteCode})
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.assert(t, out)
		})
	}
}

// assertMCPSiteLayout checks the nested drawable structure: site header,
// zone order, inlined zone behaviour, aisle walk order, and slot order.
func assertMCPSiteLayout(t *testing.T, out siteLayoutDTO) {
	t.Helper()
	if out.Site.Code != "WH1" || out.Site.Name != "Fulfilment Centre One" {
		t.Fatalf("unexpected site header %+v", out.Site)
	}
	if len(out.Zones) != 2 {
		t.Fatalf("expected 2 zones, got %d", len(out.Zones))
	}
	// Zones ordered by id: RCV before STOR.
	if out.Zones[0].ZoneID != "WH1-RCV-AMB" || out.Zones[1].ZoneID != "WH1-STOR-AMB" {
		t.Fatalf("zones out of order: %s then %s", out.Zones[0].ZoneID, out.Zones[1].ZoneID)
	}
	storage := out.Zones[1]
	if storage.TemperatureClass != string(shared.Ambient) || storage.Hazmat {
		t.Fatalf("unexpected zone attrs %+v", storage)
	}
	if len(storage.Aisles) != 2 {
		t.Fatalf("expected 2 aisles in STOR, got %d", len(storage.Aisles))
	}
	assertMCPSiteLayoutAisles(t, storage.Aisles)
}

// assertMCPSiteLayoutAisles checks the aisle walk order and the slot
// order inside the first (walk-order-first) aisle.
func assertMCPSiteLayoutAisles(t *testing.T, aisles []aisleLayoutDTO) {
	t.Helper()
	// Walk order (sequenceHint), not registration order: A07 (7) before A09 (9).
	if aisles[0].AisleCode != "A07" || aisles[1].AisleCode != "A09" {
		t.Fatalf("aisles not in walk order: %s then %s", aisles[0].AisleCode, aisles[1].AisleCode)
	}
	a07 := aisles[0]
	if a07.SequenceHint != 7 || a07.Direction != string(shared.TwoWay) {
		t.Fatalf("unexpected aisle meta %+v", a07)
	}
	want := []string{"WH1-STOR-AMB-A07-03-01-A", "WH1-STOR-AMB-A07-03-02-A", "WH1-STOR-AMB-A07-03-02-B"}
	if len(a07.SlotCodes) != len(want) {
		t.Fatalf("expected %d slot codes, got %v", len(want), a07.SlotCodes)
	}
	for i, code := range want {
		if a07.SlotCodes[i] != code {
			t.Fatalf("slot %d = %q, want %q", i, a07.SlotCodes[i], code)
		}
	}
}

// zoneGridCase is one row of the getZoneGrid table: either an input the tool
// must reject (wantErr) or one whose DTO must satisfy assert.
type zoneGridCase struct {
	name    string
	zoneID  string
	wantErr bool
	assert  func(t *testing.T, out zoneGridDTO)
}

func TestGetZoneGrid(t *testing.T) {
	tests := []zoneGridCase{
		{name: "empty zoneId rejected", zoneID: "", wantErr: true},
		{name: "unknown zone rejected", zoneID: "WH1-NOPE-XXX", wantErr: true},
		{
			name:   "grid for the storage zone",
			zoneID: "WH1-STOR-AMB",
			assert: assertMCPZoneGrid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.seedDrawableSite()
			out, err := h.deps.getZoneGrid(h.ctx(), zoneGridInput{ZoneID: tc.zoneID})
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.assert(t, out)
		})
	}
}

// assertMCPZoneGrid checks the grid's shape: zone header, walk-order hints on
// columns, level/row alignment, and that a known slot really appears.
func assertMCPZoneGrid(t *testing.T, out zoneGridDTO) {
	t.Helper()
	if out.ZoneID != "WH1-STOR-AMB" {
		t.Fatalf("unexpected zone id %q", out.ZoneID)
	}
	if len(out.Columns) == 0 {
		t.Fatal("expected at least one column")
	}
	// Columns carry each aisle's walk-order hint.
	for _, c := range out.Columns {
		if c.AisleCode == "A07" && c.SequenceHint != 7 {
			t.Fatalf("A07 column has wrong sequence hint %d", c.SequenceHint)
		}
	}
	if len(out.Levels) == 0 || len(out.Rows) == 0 {
		t.Fatal("expected levels and rows")
	}
	// Every row's cells are index-aligned with columns.
	for _, r := range out.Rows {
		if len(r.Cells) != len(out.Columns) {
			t.Fatalf("row %q has %d cells, want %d", r.Level, len(r.Cells), len(out.Columns))
		}
	}
	if !gridContainsSlot(out, "WH1-STOR-AMB-A07-03-02-B") {
		t.Fatal("expected slot WH1-STOR-AMB-A07-03-02-B somewhere in the grid")
	}
}

// gridContainsSlot reports whether any cell of the grid carries code.
func gridContainsSlot(out zoneGridDTO, code string) bool {
	for _, r := range out.Rows {
		for _, cell := range r.Cells {
			for _, got := range cell.SlotCodes {
				if got == code {
					return true
				}
			}
		}
	}
	return false
}

func TestListFunctionalLocations(t *testing.T) {
	t.Run("empty siteCode rejected", testFunctionalLocationsEmptySite)
	t.Run("unknown role rejected", testFunctionalLocationsUnknownRole)
	t.Run("unknown site rejected", testFunctionalLocationsUnknownSite)
	t.Run("returns only the matching role's locations, ordered by coordinate", testFunctionalLocationsRoleFilter)
	t.Run("a role with no matching locations returns an empty, non-nil list", testFunctionalLocationsEmptyRole)
}

// seedFunctionalHarness builds a site with two dock doors and one storage
// slot, the fixture every functional-location case needs.
func seedFunctionalHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.mustRegisterSite("WH1", "Fulfilment Centre One")
	h.mustRegisterZone("WH1", "DOCK", "OB", shared.Ambient, false)
	h.mustRegisterAisle("WH1-DOCK-OB", "D01", 1, shared.TwoWay)
	h.mustRegisterDockLocationType("DockDoor", placement.Dock)
	h.mustRegisterFunctionalSlot("WH1-DOCK-OB-D01-01-01-A", "DockDoor", "Outbound", nil)
	h.mustRegisterFunctionalSlot("WH1-DOCK-OB-D01-01-02-A", "DockDoor", "Inbound", nil)

	h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
	h.mustRegisterAisle("WH1-STOR-AMB", "A07", 7, shared.TwoWay)
	h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
	h.mustRegisterSlot("WH1-STOR-AMB-A07-03-02-B", placement.PalletRack)
	return h
}

func testFunctionalLocationsEmptySite(t *testing.T) {
	t.Helper()
	h := newHarness(t)
	if _, err := h.deps.listFunctionalLocations(h.ctx(), listFunctionalLocationsInput{Role: "Dock"}); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func testFunctionalLocationsUnknownRole(t *testing.T) {
	t.Helper()
	h := seedFunctionalHarness(t)
	if _, err := h.deps.listFunctionalLocations(h.ctx(), listFunctionalLocationsInput{SiteCode: "WH1", Role: "Warehouse"}); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func testFunctionalLocationsUnknownSite(t *testing.T) {
	t.Helper()
	h := seedFunctionalHarness(t)
	if _, err := h.deps.listFunctionalLocations(h.ctx(), listFunctionalLocationsInput{SiteCode: "NOPE", Role: "Dock"}); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func testFunctionalLocationsRoleFilter(t *testing.T) {
	t.Helper()
	h := seedFunctionalHarness(t)
	out, err := h.deps.listFunctionalLocations(h.ctx(), listFunctionalLocationsInput{SiteCode: "WH1", Role: "Dock"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Locations) != 2 {
		t.Fatalf("expected 2 dock locations, got %d: %+v", len(out.Locations), out.Locations)
	}
	if out.Locations[0].LocationCode != "WH1-DOCK-OB-D01-01-01-A" || out.Locations[0].DockFlow != "Outbound" {
		t.Fatalf("unexpected first location %+v", out.Locations[0])
	}
	if out.Locations[1].LocationCode != "WH1-DOCK-OB-D01-01-02-A" || out.Locations[1].DockFlow != "Inbound" {
		t.Fatalf("unexpected second location %+v", out.Locations[1])
	}
	for _, loc := range out.Locations {
		if loc.Role != "Dock" {
			t.Fatalf("expected role Dock, got %q", loc.Role)
		}
		if len(loc.Activities) != 0 {
			t.Fatalf("expected no activities on a Dock location, got %v", loc.Activities)
		}
	}
}

func testFunctionalLocationsEmptyRole(t *testing.T) {
	t.Helper()
	h := seedFunctionalHarness(t)
	out, err := h.deps.listFunctionalLocations(h.ctx(), listFunctionalLocationsInput{SiteCode: "WH1", Role: "Yard"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Locations == nil || len(out.Locations) != 0 {
		t.Fatalf("expected an empty, non-nil list, got %+v", out.Locations)
	}
}

func TestGetZoneTravelGraphTool(t *testing.T) {
	h := newHarness(t)
	h.mustRegisterSite("WH1", "Fulfilment Centre One")
	h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
	h.mustRegisterAisle("WH1-STOR-AMB", "A07", 7, shared.TwoWay)
	h.mustRegisterAisle("WH1-STOR-AMB", "A08", 8, shared.TwoWay)
	h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
	h.mustRegisterSlot("WH1-STOR-AMB-A07-01-01-A", placement.PalletRack)
	h.mustRegisterSlot("WH1-STOR-AMB-A07-02-01-A", placement.PalletRack)
	h.mustRegisterSlot("WH1-STOR-AMB-A08-01-01-A", placement.PalletRack)
	if _, err := h.registerCrossAisle.Execute(h.ctx(), "WH1-STOR-AMB", "A07", "A08", "01"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Run("empty zoneId rejected", func(t *testing.T) {
		if _, err := h.deps.getZoneTravelGraph(h.ctx(), zoneTravelGraphInput{}); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("unknown zone rejected", func(t *testing.T) {
		if _, err := h.deps.getZoneTravelGraph(h.ctx(), zoneTravelGraphInput{ZoneID: "WH1-NOPE-XXX"}); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("returns every node and edge", func(t *testing.T) {
		out, err := h.deps.getZoneTravelGraph(h.ctx(), zoneTravelGraphInput{ZoneID: "WH1-STOR-AMB"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out.Nodes) != 3 {
			t.Fatalf("expected 3 nodes, got %d: %+v", len(out.Nodes), out.Nodes)
		}
		if len(out.Edges) == 0 {
			t.Fatal("expected at least one edge")
		}
	})
}

func TestEstimateTravelDistanceTool(t *testing.T) {
	h := newHarness(t)
	h.mustRegisterSite("WH1", "Fulfilment Centre One")
	h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
	h.mustRegisterAisle("WH1-STOR-AMB", "A07", 7, shared.TwoWay)
	h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
	h.mustRegisterSlot("WH1-STOR-AMB-A07-01-01-A", placement.PalletRack)
	h.mustRegisterSlot("WH1-STOR-AMB-A07-02-01-A", placement.PalletRack)

	t.Run("empty from rejected", func(t *testing.T) {
		if _, err := h.deps.estimateTravelDistance(h.ctx(), estimateTravelDistanceInput{To: "WH1-STOR-AMB-A07-02-01-A"}); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("empty to rejected", func(t *testing.T) {
		if _, err := h.deps.estimateTravelDistance(h.ctx(), estimateTravelDistanceInput{From: "WH1-STOR-AMB-A07-01-01-A"}); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("malformed code rejected", func(t *testing.T) {
		if _, err := h.deps.estimateTravelDistance(h.ctx(), estimateTravelDistanceInput{From: "not-a-code", To: "WH1-STOR-AMB-A07-02-01-A"}); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("computes the shortest distance", func(t *testing.T) {
		out, err := h.deps.estimateTravelDistance(h.ctx(), estimateTravelDistanceInput{
			From: "WH1-STOR-AMB-A07-01-01-A", To: "WH1-STOR-AMB-A07-02-01-A",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.MetresM != 1.2 {
			t.Fatalf("expected 1.2m (1 gap at the default bay pitch), got %v", out.MetresM)
		}
		if !out.Estimated {
			t.Fatal("expected Estimated=true: no aisle geometry was ever set")
		}
		if len(out.Route) != 2 {
			t.Fatalf("expected a 2-waypoint route, got %d", len(out.Route))
		}
	})
}
