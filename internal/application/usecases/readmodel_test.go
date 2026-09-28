package usecases_test

import (
	"testing"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// seedDrawableSite builds a small but realistic WH1: two zones, three
// aisles registered out of walk order, and a handful of slots.
func seedDrawableSite(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)

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

	return h
}

func TestGetSiteLayout(t *testing.T) {
	t.Run("assembles the full nested drawable structure", testReadModelSiteLayoutStructure)
	t.Run("an empty site yields an empty but well-formed layout", testReadModelSiteLayoutEmpty)
	t.Run("rejects an unknown site", testReadModelSiteLayoutUnknown)
	t.Run("aisles sharing a sequence hint fall back to aisle code, and slots order by bay first", testReadModelSiteLayoutTieBreak)
	t.Run("publishes nothing: it is a pure read model", testReadModelSiteLayoutPublishesNothing)
}

func testReadModelSiteLayoutStructure(t *testing.T) {
	t.Helper()
	h := seedDrawableSite(t)

	layout, err := h.getSiteLayout.Execute(h.ctx(), "WH1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if layout.Site.Code() != "WH1" {
		t.Fatalf("unexpected site %q", layout.Site.Code())
	}
	if len(layout.Zones) != 2 {
		t.Fatalf("expected 2 zones, got %d", len(layout.Zones))
	}
	if layout.Zones[0].Zone.ID() != "WH1-RCV-AMB" || layout.Zones[1].Zone.ID() != "WH1-STOR-AMB" {
		t.Fatalf("expected zones ordered by id, got %s then %s", layout.Zones[0].Zone.ID(), layout.Zones[1].Zone.ID())
	}

	assertStorageZoneAisleOrder(t, layout)
}

// assertStorageZoneAisleOrder checks the STOR zone's aisle walk order and
// its slots' bay-first ordering.
func assertStorageZoneAisleOrder(t *testing.T, layout *usecases.SiteLayout) {
	t.Helper()
	storage := layout.Zones[1]
	if len(storage.Aisles) != 2 {
		t.Fatalf("expected 2 aisles in the storage zone, got %d", len(storage.Aisles))
	}
	// Walk order, not registration order.
	if storage.Aisles[0].Aisle.AisleCode() != "A07" || storage.Aisles[1].Aisle.AisleCode() != "A09" {
		t.Fatalf("expected aisles in SequenceHint order, got %s then %s",
			storage.Aisles[0].Aisle.AisleCode(), storage.Aisles[1].Aisle.AisleCode())
	}

	a07 := storage.Aisles[0]
	if len(a07.Slots) != 3 {
		t.Fatalf("expected 3 slots in A07, got %d", len(a07.Slots))
	}
	want := []string{"WH1-STOR-AMB-A07-03-01-A", "WH1-STOR-AMB-A07-03-02-A", "WH1-STOR-AMB-A07-03-02-B"}
	for i, code := range want {
		if a07.Slots[i].Code().String() != code {
			t.Fatalf("expected slot %d to be %q, got %q", i, code, a07.Slots[i].Code())
		}
	}
}

func testReadModelSiteLayoutEmpty(t *testing.T) {
	t.Helper()
	h := newHarness(t)
	h.mustRegisterSite("WH2", "Fulfilment Centre Two")

	layout, err := h.getSiteLayout.Execute(h.ctx(), "WH2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if layout.Zones == nil || len(layout.Zones) != 0 {
		t.Fatalf("expected an empty, non-nil zone list, got %v", layout.Zones)
	}
}

func testReadModelSiteLayoutUnknown(t *testing.T) {
	t.Helper()
	h := newHarness(t)
	_, err := h.getSiteLayout.Execute(h.ctx(), "NOPE")
	assertErrorIs(t, err, usecases.ErrSiteNotFound)
}

func testReadModelSiteLayoutTieBreak(t *testing.T) {
	t.Helper()
	h := newHarness(t)
	h.mustRegisterSite("WH1", "Fulfilment Centre One")
	h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
	h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)

	// Three aisles all sharing hint 5, registered in non-alphabetical
	// order: the layout must order them by code so the order is total.
	h.mustRegisterAisle("WH1-STOR-AMB", "A09", 5, shared.TwoWay)
	h.mustRegisterAisle("WH1-STOR-AMB", "A07", 5, shared.TwoWay)
	h.mustRegisterAisle("WH1-STOR-AMB", "A08", 5, shared.TwoWay)

	// Slots across bays, registered deliberately out of order.
	h.mustRegisterSlot("WH1-STOR-AMB-A07-03-01-A", placement.PalletRack)
	h.mustRegisterSlot("WH1-STOR-AMB-A07-01-02-A", placement.PalletRack)
	h.mustRegisterSlot("WH1-STOR-AMB-A07-02-01-A", placement.PalletRack)

	layout, err := h.getSiteLayout.Execute(h.ctx(), "WH1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aisles := layout.Zones[0].Aisles
	for i, want := range []string{"A07", "A08", "A09"} {
		if aisles[i].Aisle.AisleCode() != want {
			t.Fatalf("expected aisle %d to be %q on the hint tie, got %q", i, want, aisles[i].Aisle.AisleCode())
		}
	}

	want := []string{"WH1-STOR-AMB-A07-01-02-A", "WH1-STOR-AMB-A07-02-01-A", "WH1-STOR-AMB-A07-03-01-A"}
	a07 := aisles[0].Slots
	if len(a07) != len(want) {
		t.Fatalf("expected %d slots, got %d", len(want), len(a07))
	}
	for i, code := range want {
		if a07[i].Code().String() != code {
			t.Fatalf("expected slot %d to be %q, got %q", i, code, a07[i].Code().String())
		}
	}
}

func testReadModelSiteLayoutPublishesNothing(t *testing.T) {
	t.Helper()
	h := seedDrawableSite(t)
	before := len(h.publishedEventNames())
	if _, err := h.getSiteLayout.Execute(h.ctx(), "WH1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if after := len(h.publishedEventNames()); after != before {
		t.Fatalf("a read model must publish nothing: %d -> %d events", before, after)
	}
}

func TestGetZoneGrid(t *testing.T) {
	t.Run("shapes the zone as a level x (aisle,bay) matrix in walk order", testReadModelZoneGridMatrix)
	t.Run("a zone with no slots yields an empty but well-formed grid", testReadModelZoneGridEmpty)
	t.Run("rejects an unknown zone", testReadModelZoneGridUnknown)
	t.Run("publishes nothing: it is a pure read model", testReadModelZoneGridPublishesNothing)
}

func testReadModelZoneGridMatrix(t *testing.T) {
	t.Helper()
	h := seedDrawableSite(t)

	grid, err := h.getZoneGrid.Execute(h.ctx(), "WH1-STOR-AMB")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertGridZoneHeader(t, grid)
	assertGridColumns(t, grid)
	assertGridRowShape(t, grid)
	assertGridCells(t, grid)
}

func assertGridZoneHeader(t *testing.T, grid *usecases.ZoneGrid) {
	t.Helper()
	if grid.Zone.ID() != "WH1-STOR-AMB" {
		t.Fatalf("unexpected zone %q", grid.Zone.ID())
	}
}

// assertGridColumns checks the walk-ordered (aisle,bay) columns.
func assertGridColumns(t *testing.T, grid *usecases.ZoneGrid) {
	t.Helper()
	// Columns: A07/03 (hint 7) comes before A09/01 (hint 9).
	if len(grid.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %+v", grid.Columns)
	}
	if grid.Columns[0].AisleCode != "A07" || grid.Columns[0].Bay != "03" || grid.Columns[0].SequenceHint != 7 {
		t.Fatalf("unexpected first column %+v", grid.Columns[0])
	}
	if grid.Columns[1].AisleCode != "A09" || grid.Columns[1].Bay != "01" {
		t.Fatalf("unexpected second column %+v", grid.Columns[1])
	}
}

// assertGridRowShape checks the level rows and their column alignment.
func assertGridRowShape(t *testing.T, grid *usecases.ZoneGrid) {
	t.Helper()
	// Rows: level 01 then level 02.
	if len(grid.Levels) != 2 || grid.Levels[0] != "01" || grid.Levels[1] != "02" {
		t.Fatalf("unexpected levels %v", grid.Levels)
	}
	if len(grid.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(grid.Rows))
	}
	for _, r := range grid.Rows {
		if len(r.Cells) != len(grid.Columns) {
			t.Fatalf("row %q has %d cells but the grid has %d columns", r.Level, len(r.Cells), len(grid.Columns))
		}
	}
}

// assertGridCells checks the populated cells and the A09 gap at level 02.
func assertGridCells(t *testing.T, grid *usecases.ZoneGrid) {
	t.Helper()
	// Level 01 x A07/03 holds exactly one slot; level 01 x A09/01 one too.
	level01 := grid.Rows[0]
	if len(level01.Cells[0].Slots) != 1 || level01.Cells[0].Slots[0].Code().String() != "WH1-STOR-AMB-A07-03-01-A" {
		t.Fatalf("unexpected cell (01, A07/03): %+v", level01.Cells[0].Slots)
	}
	if len(level01.Cells[1].Slots) != 1 {
		t.Fatalf("unexpected cell (01, A09/01): %+v", level01.Cells[1].Slots)
	}

	// Level 02 x A07/03 holds both positions, ordered A then B; the
	// A09 column is a gap at that level.
	level02 := grid.Rows[1]
	if len(level02.Cells[0].Slots) != 2 {
		t.Fatalf("expected 2 positions at (02, A07/03), got %+v", level02.Cells[0].Slots)
	}
	if level02.Cells[0].Slots[0].Code().Position() != "A" || level02.Cells[0].Slots[1].Code().Position() != "B" {
		t.Fatalf("expected positions ordered A then B, got %+v", level02.Cells[0].Slots)
	}
	if len(level02.Cells[1].Slots) != 0 {
		t.Fatalf("expected a gap at (02, A09/01), got %+v", level02.Cells[1].Slots)
	}
}

func testReadModelZoneGridEmpty(t *testing.T) {
	t.Helper()
	h := newHarness(t)
	h.mustRegisterSite("WH1", "Fulfilment Centre One")
	h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
	h.mustRegisterAisle("WH1-STOR-AMB", "A07", 7, shared.TwoWay)

	grid, err := h.getZoneGrid.Execute(h.ctx(), "WH1-STOR-AMB")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(grid.Columns) != 0 || len(grid.Rows) != 0 || len(grid.Levels) != 0 {
		t.Fatalf("expected an empty grid, got %+v", grid)
	}
	if grid.Columns == nil || grid.Rows == nil {
		t.Fatal("expected empty, non-nil slices so the JSON renders as [] not null")
	}
}

func testReadModelZoneGridUnknown(t *testing.T) {
	t.Helper()
	h := newHarness(t)
	_, err := h.getZoneGrid.Execute(h.ctx(), "WH1-STOR-NOPE")
	assertErrorIs(t, err, usecases.ErrZoneNotFound)
}

func testReadModelZoneGridPublishesNothing(t *testing.T) {
	t.Helper()
	h := seedDrawableSite(t)
	before := len(h.publishedEventNames())
	if _, err := h.getZoneGrid.Execute(h.ctx(), "WH1-STOR-AMB"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if after := len(h.publishedEventNames()); after != before {
		t.Fatalf("a read model must publish nothing: %d -> %d events", before, after)
	}
}

func TestSingleResourceReads(t *testing.T) {
	h := seedDrawableSite(t)
	mustDefineRule(t, h, "RULE-HAZ-ONLY-RACK", placement.PalletRack, placement.Allow, mustPredicate(t, "HAZ", "", nil))

	getZone := &usecases.GetZone{Zones: h.zones}
	getAisle := &usecases.GetAisle{Aisles: h.aisles}
	getType := &usecases.GetLocationType{LocationTypes: h.locationTypes}
	getRule := &usecases.GetPlacementRule{Rules: h.rules}

	z, err := getZone.Execute(h.ctx(), "WH1-STOR-AMB")
	if err != nil || z.ZoneCode() != "AMB" {
		t.Fatalf("unexpected zone read: %v / %v", z, err)
	}
	if _, err := getZone.Execute(h.ctx(), "WH1-STOR-NOPE"); !isErr(err, usecases.ErrZoneNotFound) {
		t.Fatalf("expected ErrZoneNotFound, got %v", err)
	}

	a, err := getAisle.Execute(h.ctx(), "WH1-STOR-AMB-A07")
	if err != nil || a.SequenceHint() != 7 {
		t.Fatalf("unexpected aisle read: %v / %v", a, err)
	}
	if _, err := getAisle.Execute(h.ctx(), "WH1-STOR-AMB-A99"); !isErr(err, usecases.ErrAisleNotFound) {
		t.Fatalf("expected ErrAisleNotFound, got %v", err)
	}

	lt, err := getType.Execute(h.ctx(), placement.PalletRack)
	if err != nil || lt.DefaultCapacity().MaxWeightKg() != 1200 {
		t.Fatalf("unexpected location type read: %v / %v", lt, err)
	}
	if _, err := getType.Execute(h.ctx(), "Hovercraft"); !isErr(err, usecases.ErrLocationTypeNotFound) {
		t.Fatalf("expected ErrLocationTypeNotFound, got %v", err)
	}

	rule, err := getRule.Execute(h.ctx(), "RULE-HAZ-ONLY-RACK")
	if err != nil || rule.Effect() != placement.Allow {
		t.Fatalf("unexpected rule read: %v / %v", rule, err)
	}
	if _, err := getRule.Execute(h.ctx(), "RULE-NOPE"); !isErr(err, usecases.ErrPlacementRuleNotFound) {
		t.Fatalf("expected ErrPlacementRuleNotFound, got %v", err)
	}
}

func TestSingleResourceReadsPropagateFailures(t *testing.T) {
	h := newHarness(t)

	if _, err := (&usecases.GetZone{Zones: &faultyZoneRepo{ZoneRepo: h.zones, failFind: true}}).Execute(h.ctx(), "WH1-STOR-AMB"); !isErr(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
	if _, err := (&usecases.GetAisle{Aisles: &faultyAisleRepo{AisleRepo: h.aisles, failFind: true}}).Execute(h.ctx(), "WH1-STOR-AMB-A07"); !isErr(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
	if _, err := (&usecases.GetLocationType{LocationTypes: &faultyLocationTypeRepo{LocationTypeRepo: h.locationTypes, failFind: true}}).Execute(h.ctx(), placement.PalletRack); !isErr(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
	if _, err := (&usecases.GetPlacementRule{Rules: &faultyRuleRepo{PlacementRuleRepo: h.rules, failFind: true}}).Execute(h.ctx(), "RULE-1"); !isErr(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
}

// mustRegisterFunctionalSlot registers a slot carrying a dock flow, which
// the plain mustRegisterSlot helper (no functional attributes) cannot do.
func mustRegisterFunctionalSlot(t *testing.T, h *harness, raw, locationType, dockFlow string) {
	t.Helper()
	if _, err := h.registerSlot.Execute(h.ctx(), mustCode(h.t, raw), locationType, shared.Capacity{}, dockFlow, nil); err != nil {
		h.t.Fatalf("seeding slot %q: %v", raw, err)
	}
}

func TestListLocationsByRole(t *testing.T) {
	t.Run("returns only the site's slots with that role, zone -> aisle -> bay ordered", func(t *testing.T) {
		h := newHarness(t)
		seedFunctionalRoles(t, h)
		mustRegisterFunctionalSlot(t, h, "WH1-STOR-AMB-A07-01-01-A", "DockDoor", "Inbound")
		mustRegisterFunctionalSlot(t, h, "WH1-STOR-AMB-A07-03-01-A", "DockDoor", "Inbound")
		h.mustRegisterSlot("WH1-STOR-AMB-A07-02-01-A", "PalletRack")
		uc := &usecases.ListLocationsByRole{Sites: h.sites, Zones: h.zones, Slots: h.slots}

		out, err := uc.Execute(h.ctx(), "WH1", placement.Dock)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"WH1-STOR-AMB-A07-01-01-A", "WH1-STOR-AMB-A07-03-01-A"}
		if len(out) != len(want) {
			t.Fatalf("expected %d dock slots, got %v", len(want), out)
		}
		for i, code := range want {
			if out[i].Code().String() != code {
				t.Fatalf("expected slot %d to be %q, got %q", i, code, out[i].Code().String())
			}
		}
	})

	t.Run("a role with no slots yields an empty, non-nil list", func(t *testing.T) {
		h := newHarness(t)
		seedFunctionalRoles(t, h)
		uc := &usecases.ListLocationsByRole{Sites: h.sites, Zones: h.zones, Slots: h.slots}

		out, err := uc.Execute(h.ctx(), "WH1", placement.Dock)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out == nil || len(out) != 0 {
			t.Fatalf("expected an empty, non-nil list, got %v", out)
		}
	})

	t.Run("rejects an unknown site", func(t *testing.T) {
		h := newHarness(t)
		uc := &usecases.ListLocationsByRole{Sites: h.sites, Zones: h.zones, Slots: h.slots}

		_, err := uc.Execute(h.ctx(), "NOPE", placement.Dock)
		assertErrorIs(t, err, usecases.ErrSiteNotFound)
	})
}
