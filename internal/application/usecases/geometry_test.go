package usecases_test

import (
	"testing"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/slot"
	"github.com/claudioed/facility-layout/internal/domain/structure"
)

func mustGeomPoint(t *testing.T, x, y, z float64) shared.Point3D {
	t.Helper()
	p, err := shared.NewPoint3D(x, y, z)
	if err != nil {
		t.Fatalf("point: %v", err)
	}
	return p
}

func mustGeomDimensions(t *testing.T, w, d, h float64) shared.Dimensions {
	t.Helper()
	dim, err := shared.NewDimensions(w, d, h)
	if err != nil {
		t.Fatalf("dimensions: %v", err)
	}
	return dim
}

func TestSetLocationGeometry(t *testing.T) {
	position := mustGeomPoint(t, 1, 2, 0)
	dimensions := mustGeomDimensions(t, 1.2, 0.9, 2)

	t.Run("sets geometry on an existing slot and publishes LocationGeometryUpdated", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		h.mustRegisterSlot("WH1-STOR-AMB-A07-03-02-B", "PalletRack")

		s, err := h.setLocationGeometry.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"), position, dimensions, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.Position() != position || s.Dimensions() != dimensions {
			t.Fatalf("unexpected geometry %+v/%+v", s.Position(), s.Dimensions())
		}
		h.assertPublished("LocationGeometryUpdated")
	})

	t.Run("also sets an explicit pick sequence when supplied", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		h.mustRegisterSlot("WH1-STOR-AMB-A07-03-02-B", "PalletRack")

		seq := 5
		s, err := h.setLocationGeometry.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"), position, dimensions, &seq)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.PickSequence() == nil || *s.PickSequence() != 5 {
			t.Fatalf("unexpected pick sequence %v", s.PickSequence())
		}
	})

	t.Run("rejects an unknown slot", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.setLocationGeometry.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"), position, dimensions, nil)
		assertErrorIs(t, err, usecases.ErrLocationSlotNotFound)
	})

	t.Run("propagates the domain rejection of a decommissioned slot", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		h.mustRegisterSlot("WH1-STOR-AMB-A07-03-02-B", "PalletRack")
		if err := h.decommissionSlot.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B")); err != nil {
			t.Fatalf("decommission: %v", err)
		}

		_, err := h.setLocationGeometry.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"), position, dimensions, nil)
		assertErrorIs(t, err, slot.ErrSlotDecommissioned)
	})

	t.Run("rejects a negative pick sequence", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		h.mustRegisterSlot("WH1-STOR-AMB-A07-03-02-B", "PalletRack")

		seq := -1
		_, err := h.setLocationGeometry.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"), position, dimensions, &seq)
		assertErrorIs(t, err, slot.ErrNegativePickSequence)
	})
}

func TestSetAisleGeometry(t *testing.T) {
	centreline, err := shared.NewSegment(mustGeomPoint(t, 0, 0, 0), mustGeomPoint(t, 10, 0, 0))
	if err != nil {
		t.Fatalf("centreline: %v", err)
	}

	t.Run("sets a centreline on an existing aisle and publishes AisleGeometryUpdated", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")
		h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
		h.mustRegisterAisle("WH1-STOR-AMB", "A07", 7, shared.TwoWay)

		a, err := h.setAisleGeometry.Execute(h.ctx(), "WH1-STOR-AMB-A07", centreline)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.Centreline() != centreline {
			t.Fatalf("unexpected centreline %+v", a.Centreline())
		}
		h.assertPublished("AisleGeometryUpdated")
	})

	t.Run("rejects an unknown aisle", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.setAisleGeometry.Execute(h.ctx(), "WH1-STOR-AMB-A07", centreline)
		assertErrorIs(t, err, usecases.ErrAisleNotFound)
	})

	t.Run("propagates the domain rejection of a decommissioned aisle", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")
		h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
		h.mustRegisterAisle("WH1-STOR-AMB", "A07", 7, shared.TwoWay)
		a, err := h.aisles.FindByID(h.ctx(), "WH1-STOR-AMB-A07")
		if err != nil || a == nil {
			t.Fatalf("seeding: %v", err)
		}
		if err := a.Decommission(); err != nil {
			t.Fatalf("decommission: %v", err)
		}
		if err := h.aisles.Save(h.ctx(), a); err != nil {
			t.Fatalf("save: %v", err)
		}

		_, err = h.setAisleGeometry.Execute(h.ctx(), "WH1-STOR-AMB-A07", centreline)
		assertErrorIs(t, err, aisle.ErrAisleDecommissioned)
	})
}

func TestRegisterFixedStructure(t *testing.T) {
	footprint := mustFootprintForUsecase(t)

	t.Run("registers a structure under an active site and publishes FixedStructureRegistered", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")

		f, err := h.registerFixedStructure.Execute(h.ctx(), "STR-1", "WH1", structure.Wall, footprint, "North wall")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if f.ID() != "STR-1" || f.SiteCode() != "WH1" {
			t.Fatalf("unexpected structure %+v", f)
		}
		h.assertPublished("FixedStructureRegistered")
	})

	t.Run("rejects an unknown site", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.registerFixedStructure.Execute(h.ctx(), "STR-1", "NOPE", structure.Wall, footprint, "North wall")
		assertErrorIs(t, err, usecases.ErrSiteNotFound)
	})

	t.Run("rejects a duplicate id", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")
		if _, err := h.registerFixedStructure.Execute(h.ctx(), "STR-1", "WH1", structure.Wall, footprint, "North wall"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, err := h.registerFixedStructure.Execute(h.ctx(), "STR-1", "WH1", structure.Column, footprint, "Different label")
		assertErrorIs(t, err, usecases.ErrDuplicateFixedStructure)
	})

	t.Run("rejects an invalid structure definition", func(t *testing.T) {
		tests := []struct {
			name    string
			kind    structure.Kind
			label   string
			wantErr error
		}{
			{name: "unknown kind", kind: "Escalator", label: "North wall", wantErr: structure.ErrUnknownKind},
			{name: "empty label", kind: structure.Wall, label: "", wantErr: structure.ErrEmptyLabel},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				h := newHarness(t)
				h.mustRegisterSite("WH1", "Fulfilment Centre One")
				_, err := h.registerFixedStructure.Execute(h.ctx(), "STR-1", "WH1", tc.kind, footprint, tc.label)
				assertErrorIs(t, err, tc.wantErr)
				h.assertNotPublished("FixedStructureRegistered")
			})
		}
	})
}

func TestListFixedStructures(t *testing.T) {
	footprint := mustFootprintForUsecase(t)

	t.Run("lists every structure at a site, ordered by id", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")
		if _, err := h.registerFixedStructure.Execute(h.ctx(), "STR-2", "WH1", structure.Wall, footprint, "East wall"); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if _, err := h.registerFixedStructure.Execute(h.ctx(), "STR-1", "WH1", structure.Column, footprint, "Column A"); err != nil {
			t.Fatalf("seed: %v", err)
		}

		out, err := h.listFixedStructures.Execute(h.ctx(), "WH1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 2 || out[0].ID() != "STR-1" {
			t.Fatalf("expected structures ordered by id, got %v", out)
		}
	})

	t.Run("rejects an unknown site", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.listFixedStructures.Execute(h.ctx(), "NOPE")
		assertErrorIs(t, err, usecases.ErrSiteNotFound)
	})

	t.Run("an empty site returns an empty, non-nil list", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")
		out, err := h.listFixedStructures.Execute(h.ctx(), "WH1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out == nil || len(out) != 0 {
			t.Fatalf("expected an empty, non-nil list, got %v", out)
		}
	})
}

func TestGetSiteLayoutIncludesFixedStructures(t *testing.T) {
	h := newHarness(t)
	h.seedAmbientAisle()
	h.mustRegisterSlot("WH1-STOR-AMB-A07-03-02-B", "PalletRack")
	footprint := mustFootprintForUsecase(t)
	if _, err := h.registerFixedStructure.Execute(h.ctx(), "STR-1", "WH1", structure.Wall, footprint, "North wall"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	layout, err := h.getSiteLayout.Execute(h.ctx(), "WH1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layout.FixedStructures) != 1 || layout.FixedStructures[0].ID() != "STR-1" {
		t.Fatalf("expected the registered structure in the layout, got %v", layout.FixedStructures)
	}
}

func mustFootprintForUsecase(t *testing.T) shared.Rect {
	t.Helper()
	origin := mustGeomPoint(t, 10, 20, 0)
	size := mustGeomDimensions(t, 1, 1, 3)
	rect, err := shared.NewRect(origin, size)
	if err != nil {
		t.Fatalf("rect: %v", err)
	}
	return rect
}
