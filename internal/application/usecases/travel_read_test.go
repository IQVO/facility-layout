package usecases_test

import (
	"math"
	"testing"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

func TestRegisterZonePitch(t *testing.T) {
	p := func(v float64) *float64 { return &v }

	t.Run("keeps the defaults when no pitch is supplied", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")

		z, err := h.registerZone.Execute(h.ctx(), "WH1", "STOR", "AMB", shared.Ambient, false, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if z.BayPitchM() != zone.DefaultBayPitchM || z.LevelPitchM() != zone.DefaultLevelPitchM {
			t.Fatalf("expected default pitches, got bay=%v level=%v", z.BayPitchM(), z.LevelPitchM())
		}
	})

	t.Run("accepts both pitches and reflects them on the zone", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")

		z, err := h.registerZone.Execute(h.ctx(), "WH1", "STOR", "AMB", shared.Ambient, false, p(1.5), p(2.0))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if z.BayPitchM() != 1.5 || z.LevelPitchM() != 2.0 {
			t.Fatalf("expected 1.5/2.0, got bay=%v level=%v", z.BayPitchM(), z.LevelPitchM())
		}
		h.assertPublished("ZoneRegistered")
	})

	t.Run("rejects a bay-only override with the domain pitch error", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")

		_, err := h.registerZone.Execute(h.ctx(), "WH1", "STOR", "AMB", shared.Ambient, false, p(1.5), nil)
		assertErrorIs(t, err, zone.ErrInvalidPitch)
		h.assertNotPublished("ZoneRegistered")
	})

	t.Run("rejects a level-only override with the domain pitch error", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")

		_, err := h.registerZone.Execute(h.ctx(), "WH1", "STOR", "AMB", shared.Ambient, false, nil, p(2.0))
		assertErrorIs(t, err, zone.ErrInvalidPitch)
	})

	t.Run("rejects a non-positive pitch with the domain pitch error", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")

		_, err := h.registerZone.Execute(h.ctx(), "WH1", "STOR", "AMB", shared.Ambient, false, p(0), p(2.0))
		assertErrorIs(t, err, zone.ErrInvalidPitch)
	})

	t.Run("uses the zone's bay pitch override in estimated same-aisle distances", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")
		if _, err := h.registerZone.Execute(h.ctx(), "WH1", "STOR", "AMB", shared.Ambient, false, p(2.4), p(1.8)); err != nil {
			t.Fatalf("register zone: %v", err)
		}
		h.mustRegisterAisle("WH1-STOR-AMB", "A07", 7, shared.TwoWay)
		h.mustRegisterLocationType("PalletRack", 1200, 2.4)
		h.mustRegisterSlot("WH1-STOR-AMB-A07-01-01-A", "PalletRack")
		h.mustRegisterSlot("WH1-STOR-AMB-A07-02-01-A", "PalletRack")
		h.mustRegisterSlot("WH1-STOR-AMB-A07-03-01-A", "PalletRack")

		d, err := h.estimateTravelDistance.Execute(h.ctx(),
			mustCode(t, "WH1-STOR-AMB-A07-01-01-A"), mustCode(t, "WH1-STOR-AMB-A07-03-01-A"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := 2 * 2.4; d.MetresM != want {
			t.Fatalf("expected %v m from the 2.4 m bay-pitch override, got %v", want, d.MetresM)
		}
		if !d.Estimated {
			t.Fatal("expected Estimated=true: no aisle geometry was set")
		}
	})
}

func TestListCrossAisles(t *testing.T) {
	t.Run("returns the zone's cross-aisles ordered from/to/bay", func(t *testing.T) {
		h := newHarness(t)
		seedThreeAisleZone(h)
		if _, err := h.registerCrossAisle.Execute(h.ctx(), "WH1-STOR-AMB", "A07", "A08", "02"); err != nil {
			t.Fatalf("seed cross-aisle 1: %v", err)
		}
		if _, err := h.registerCrossAisle.Execute(h.ctx(), "WH1-STOR-AMB", "A08", "A09", "01"); err != nil {
			t.Fatalf("seed cross-aisle 2: %v", err)
		}

		uc := &usecases.ListCrossAisles{Zones: h.zones, CrossAisles: h.crossAisles}
		connections, err := uc.Execute(h.ctx(), "WH1-STOR-AMB")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(connections) != 2 {
			t.Fatalf("expected 2 cross-aisles, got %d", len(connections))
		}
		if connections[0].FromAisle() != "A07" || connections[0].ToAisle() != "A08" || connections[0].AtBay() != "02" {
			t.Fatalf("unexpected first cross-aisle %+v", connections[0])
		}
		if connections[1].FromAisle() != "A08" || connections[1].ToAisle() != "A09" || connections[1].AtBay() != "01" {
			t.Fatalf("unexpected second cross-aisle %+v", connections[1])
		}
	})

	t.Run("returns an empty list for a zone with none declared", func(t *testing.T) {
		h := newHarness(t)
		seedThreeAisleZone(h)

		uc := &usecases.ListCrossAisles{Zones: h.zones, CrossAisles: h.crossAisles}
		connections, err := uc.Execute(h.ctx(), "WH1-STOR-AMB")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(connections) != 0 {
			t.Fatalf("expected 0 cross-aisles, got %d", len(connections))
		}
	})

	t.Run("rejects an unknown zone", func(t *testing.T) {
		h := newHarness(t)
		uc := &usecases.ListCrossAisles{Zones: h.zones, CrossAisles: h.crossAisles}
		_, err := uc.Execute(h.ctx(), "WH1-STOR-XXX")
		assertErrorIs(t, err, usecases.ErrZoneNotFound)
	})
}

func TestEstimateTravelDistanceCrossZone(t *testing.T) {
	// seedTwoZones builds WH1/STOR/AMB and WH1/STOR/FRZ, one aisle each,
	// one slot each — the smallest honest cross-zone fixture.
	seedTwoZones := func(h *harness) {
		h.t.Helper()
		h.seedAmbientAisle() // WH1, STOR/AMB (default pitches), A07, PalletRack
		h.mustRegisterSlot("WH1-STOR-AMB-A07-01-01-A", "PalletRack")
		h.mustRegisterZone("WH1", "STOR", "FRZ", shared.Frozen, false)
		h.mustRegisterAisle("WH1-STOR-FRZ", "B01", 1, shared.TwoWay)
		h.mustRegisterSlot("WH1-STOR-FRZ-B01-01-01-A", "PalletRack")
	}
	fromCode := "WH1-STOR-AMB-A07-01-01-A"
	toCode := "WH1-STOR-FRZ-B01-01-01-A"

	t.Run("estimates the beeline when both slots carry position geometry", func(t *testing.T) {
		h := newHarness(t)
		seedTwoZones(h)
		if _, err := h.setLocationGeometry.Execute(h.ctx(),
			mustCode(t, fromCode), mustGeomPoint(t, 0, 0, 0), mustGeomDimensions(t, 1, 1, 1), nil); err != nil {
			t.Fatalf("set from geometry: %v", err)
		}
		if _, err := h.setLocationGeometry.Execute(h.ctx(),
			mustCode(t, toCode), mustGeomPoint(t, 3, 4, 12), mustGeomDimensions(t, 1, 1, 1), nil); err != nil {
			t.Fatalf("set to geometry: %v", err)
		}

		d, err := h.estimateTravelDistance.Execute(h.ctx(), mustCode(t, fromCode), mustCode(t, toCode))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := 13.0; math.Abs(d.MetresM-want) > 1e-9 {
			t.Fatalf("expected beeline %v m (3-4-12), got %v", want, d.MetresM)
		}
		if !d.Estimated {
			t.Fatal("expected Estimated=true: a beeline is not a routed path")
		}
		if len(d.Route) != 2 || d.Route[0].AisleID != "WH1-STOR-AMB-A07" || d.Route[1].AisleID != "WH1-STOR-FRZ-B01" {
			t.Fatalf("expected the two endpoint waypoints, got %+v", d.Route)
		}
	})

	t.Run("still refuses when neither slot has geometry", func(t *testing.T) {
		h := newHarness(t)
		seedTwoZones(h)

		_, err := h.estimateTravelDistance.Execute(h.ctx(), mustCode(t, fromCode), mustCode(t, toCode))
		assertErrorIs(t, err, usecases.ErrNoRouteBetweenZones)
	})

	t.Run("refuses when only the far endpoint has geometry", func(t *testing.T) {
		h := newHarness(t)
		seedTwoZones(h)
		if _, err := h.setLocationGeometry.Execute(h.ctx(),
			mustCode(t, toCode), mustGeomPoint(t, 3, 4, 12), mustGeomDimensions(t, 1, 1, 1), nil); err != nil {
			t.Fatalf("set to geometry: %v", err)
		}

		_, err := h.estimateTravelDistance.Execute(h.ctx(), mustCode(t, fromCode), mustCode(t, toCode))
		assertErrorIs(t, err, usecases.ErrNoRouteBetweenZones)
	})
}
