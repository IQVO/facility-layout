package usecases_test

import (
	"testing"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/travel"
)

// seedThreeAisleZone builds WH1/STOR/AMB with three aisles (A07 TwoWay,
// A08 TwoWay, A09 OneWay) and registers one slot per bay 01..03 on each,
// so every aisle has three walkable bays.
func seedThreeAisleZone(h *harness) {
	h.t.Helper()
	h.seedAmbientAisle() // registers WH1, STOR/AMB, A07 (TwoWay), PalletRack
	h.mustRegisterAisle("WH1-STOR-AMB", "A08", 8, shared.TwoWay)
	h.mustRegisterAisle("WH1-STOR-AMB", "A09", 9, shared.OneWay)
	for _, aisleCode := range []string{"A07", "A08", "A09"} {
		for _, bay := range []string{"01", "02", "03"} {
			h.mustRegisterSlot("WH1-STOR-AMB-"+aisleCode+"-"+bay+"-01-A", "PalletRack")
		}
	}
}

func TestRegisterCrossAisle(t *testing.T) {
	t.Run("registers a connection between two existing aisles in the zone", func(t *testing.T) {
		h := newHarness(t)
		seedThreeAisleZone(h)

		c, err := h.registerCrossAisle.Execute(h.ctx(), "WH1-STOR-AMB", "A07", "A08", "02")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.ZoneID() != "WH1-STOR-AMB" || c.FromAisle() != "A07" || c.ToAisle() != "A08" || c.AtBay() != "02" {
			t.Fatalf("unexpected cross-aisle %+v", c)
		}
		h.assertPublished("CrossAisleRegistered")
	})

	t.Run("rejects an unknown zone", func(t *testing.T) {
		h := newHarness(t)
		seedThreeAisleZone(h)

		_, err := h.registerCrossAisle.Execute(h.ctx(), "WH1-STOR-XXX", "A07", "A08", "02")
		assertErrorIs(t, err, usecases.ErrZoneNotFound)
	})

	t.Run("rejects an aisle that does not belong to the zone", func(t *testing.T) {
		h := newHarness(t)
		seedThreeAisleZone(h)

		_, err := h.registerCrossAisle.Execute(h.ctx(), "WH1-STOR-AMB", "A07", "A99", "02")
		assertErrorIs(t, err, usecases.ErrCrossAisleAisleMismatch)
	})

	t.Run("rejects a same-aisle connection", func(t *testing.T) {
		h := newHarness(t)
		seedThreeAisleZone(h)

		_, err := h.registerCrossAisle.Execute(h.ctx(), "WH1-STOR-AMB", "A07", "A07", "02")
		assertErrorIs(t, err, aisle.ErrCrossAisleSameAisle)
	})

	t.Run("rejects a duplicate regardless of orientation", func(t *testing.T) {
		h := newHarness(t)
		seedThreeAisleZone(h)

		if _, err := h.registerCrossAisle.Execute(h.ctx(), "WH1-STOR-AMB", "A07", "A08", "02"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, err := h.registerCrossAisle.Execute(h.ctx(), "WH1-STOR-AMB", "A08", "A07", "02")
		assertErrorIs(t, err, usecases.ErrDuplicateCrossAisle)
	})
}

func TestGetZoneTravelGraph(t *testing.T) {
	h := newHarness(t)
	seedThreeAisleZone(h)
	if _, err := h.registerCrossAisle.Execute(h.ctx(), "WH1-STOR-AMB", "A07", "A08", "02"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	view, err := h.getZoneTravelGraph.Execute(h.ctx(), "WH1-STOR-AMB")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 3 aisles x 3 bays = 9 nodes.
	if len(view.Nodes) != 9 {
		t.Fatalf("expected 9 nodes, got %d: %+v", len(view.Nodes), view.Nodes)
	}
	if len(view.Edges) == 0 {
		t.Fatal("expected at least one edge")
	}

	t.Run("unknown zone", func(t *testing.T) {
		_, err := h.getZoneTravelGraph.Execute(h.ctx(), "WH1-STOR-XXX")
		assertErrorIs(t, err, usecases.ErrZoneNotFound)
	})
}

func TestEstimateTravelDistance(t *testing.T) {
	t.Run("computes a same-aisle distance using the zone's default pitch", func(t *testing.T) {
		h := newHarness(t)
		seedThreeAisleZone(h)

		d, err := h.estimateTravelDistance.Execute(h.ctx(),
			mustCode(t, "WH1-STOR-AMB-A07-01-01-A"), mustCode(t, "WH1-STOR-AMB-A07-03-01-A"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := 2 * 1.2 // 2 gaps at the DefaultBayPitchM
		if d.MetresM != want {
			t.Fatalf("expected %v, got %v", want, d.MetresM)
		}
		if !d.Estimated {
			t.Fatal("expected Estimated=true: no aisle geometry was ever set")
		}
	})

	t.Run("routes across a cross-aisle when a direct path does not exist", func(t *testing.T) {
		h := newHarness(t)
		seedThreeAisleZone(h)
		if _, err := h.registerCrossAisle.Execute(h.ctx(), "WH1-STOR-AMB", "A07", "A08", "02"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		d, err := h.estimateTravelDistance.Execute(h.ctx(),
			mustCode(t, "WH1-STOR-AMB-A07-01-01-A"), mustCode(t, "WH1-STOR-AMB-A08-03-01-A"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(d.Route) < 3 {
			t.Fatalf("expected a route crossing at least one cross-aisle, got %+v", d.Route)
		}
	})

	t.Run("refuses a cross-zone request", func(t *testing.T) {
		h := newHarness(t)
		seedThreeAisleZone(h)
		h.mustRegisterZone("WH1", "STOR", "FRZ", shared.Frozen, false)
		h.mustRegisterAisle("WH1-STOR-FRZ", "B01", 1, shared.TwoWay)
		h.mustRegisterSlot("WH1-STOR-FRZ-B01-01-01-A", "PalletRack")

		_, err := h.estimateTravelDistance.Execute(h.ctx(),
			mustCode(t, "WH1-STOR-AMB-A07-01-01-A"), mustCode(t, "WH1-STOR-FRZ-B01-01-01-A"))
		assertErrorIs(t, err, usecases.ErrNoRouteBetweenZones)
	})

	t.Run("errors when a location does not exist", func(t *testing.T) {
		h := newHarness(t)
		seedThreeAisleZone(h)

		_, err := h.estimateTravelDistance.Execute(h.ctx(),
			mustCode(t, "WH1-STOR-AMB-A99-01-01-A"), mustCode(t, "WH1-STOR-AMB-A07-01-01-A"))
		assertErrorIs(t, err, usecases.ErrLocationSlotNotFound)
	})

	t.Run("honours OneWay aisle direction", func(t *testing.T) {
		h := newHarness(t)
		seedThreeAisleZone(h)

		// A09 is OneWay; going from bay 03 back to bay 01 has no route
		// without a cross-aisle.
		_, err := h.estimateTravelDistance.Execute(h.ctx(),
			mustCode(t, "WH1-STOR-AMB-A09-03-01-A"), mustCode(t, "WH1-STOR-AMB-A09-01-01-A"))
		assertErrorIs(t, err, travel.ErrNoRoute)
	})

	t.Run("skips a decommissioned cross-aisle when building the graph", func(t *testing.T) {
		h := newHarness(t)
		seedThreeAisleZone(h)
		if _, err := h.registerCrossAisle.Execute(h.ctx(), "WH1-STOR-AMB", "A07", "A08", "02"); err != nil {
			t.Fatalf("seed cross-aisle: %v", err)
		}
		c, err := h.crossAisles.FindByAisles(h.ctx(), "WH1-STOR-AMB", "A07", "A08", "02")
		if err != nil || c == nil {
			t.Fatalf("seeding: cross-aisle not found (%v)", err)
		}
		if err := c.Decommission(); err != nil {
			t.Fatalf("decommission: %v", err)
		}
		if err := h.crossAisles.Save(h.ctx(), c); err != nil {
			t.Fatalf("save: %v", err)
		}

		// A09 is OneWay and A07/A08 connect only through the now-retired
		// cross-aisle, so no route exists once it is skipped.
		_, err = h.estimateTravelDistance.Execute(h.ctx(),
			mustCode(t, "WH1-STOR-AMB-A07-01-01-A"), mustCode(t, "WH1-STOR-AMB-A08-03-01-A"))
		assertErrorIs(t, err, travel.ErrNoRoute)
	})

	t.Run("uses real aisle centreline geometry when set", func(t *testing.T) {
		h := newHarness(t)
		seedThreeAisleZone(h)

		start := mustGeomPoint(t, 0, 0, 0)
		end := mustGeomPoint(t, 2.4, 0, 0)
		centreline, err := shared.NewSegment(start, end)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, err := h.setAisleGeometry.Execute(h.ctx(), "WH1-STOR-AMB-A07", centreline); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		d, err := h.estimateTravelDistance.Execute(h.ctx(),
			mustCode(t, "WH1-STOR-AMB-A07-01-01-A"), mustCode(t, "WH1-STOR-AMB-A07-03-01-A"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.MetresM != 2.4 {
			t.Fatalf("expected 2.4m from real centreline geometry, got %v", d.MetresM)
		}
		if d.Estimated {
			t.Fatal("expected Estimated=false: the route used only real geometry")
		}
	})
}
