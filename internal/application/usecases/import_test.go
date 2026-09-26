package usecases_test

import (
	"strings"
	"testing"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/slot"
)

// row builds an ImportRow for the canonical WH1 storage layout.
func row(areaCode, zoneCode string, tc shared.TemperatureClass, hazmat bool, aisleCode string, hint int, bay, level, position, locationType string) usecases.ImportRow {
	return usecases.ImportRow{
		SiteCode:         "WH1",
		SiteName:         "Fulfilment Centre One",
		AreaCode:         areaCode,
		ZoneCode:         zoneCode,
		TemperatureClass: tc,
		Hazmat:           hazmat,
		AisleCode:        aisleCode,
		SequenceHint:     hint,
		Direction:        shared.TwoWay,
		Bay:              bay,
		Level:            level,
		Position:         position,
		LocationType:     locationType,
	}
}

func TestImportFacilityLayout(t *testing.T) {
	t.Run("creates the whole structure from scratch", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)

		report, err := h.importLayout.Execute(h.ctx(), []usecases.ImportRow{
			row("STOR", "AMB", shared.Ambient, false, "A07", 7, "03", "02", "B", placement.PalletRack),
			row("STOR", "AMB", shared.Ambient, false, "A07", 7, "03", "02", "C", placement.PalletRack),
			row("STOR", "FRZ", shared.Frozen, false, "A02", 2, "01", "01", "A", placement.PalletRack),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if report.RowsSubmitted != 3 || report.SlotsImported != 3 || report.RowsRejected != 0 {
			t.Fatalf("unexpected report %+v", report)
		}
		for _, result := range report.Results {
			if !result.Succeeded {
				t.Fatalf("row %d unexpectedly failed: %s", result.Index, result.Error)
			}
		}

		// The site, both zones and both aisles were created on first sight.
		if s, _ := h.sites.FindByCode(h.ctx(), "WH1"); s == nil {
			t.Fatal("expected the import to create site WH1")
		}
		if z, _ := h.zones.FindByID(h.ctx(), "WH1-STOR-FRZ"); z == nil || z.TemperatureClass() != shared.Frozen {
			t.Fatalf("expected the import to create the frozen zone, got %v", z)
		}
		if a, _ := h.aisles.FindByID(h.ctx(), "WH1-STOR-AMB-A07"); a == nil || a.SequenceHint() != 7 {
			t.Fatalf("expected the import to create aisle A07 with its walk-order hint, got %v", a)
		}

		h.assertPublished("SiteRegistered")
		h.assertPublished("ZoneRegistered")
		h.assertPublished("AisleRegistered")
		h.assertPublished("LocationSlotRegistered")
		h.assertPublished("FacilityLayoutImported")

		// FacilityLayoutImported fires exactly once, per CLAUDE.md.
		imported := 0
		for _, name := range h.publishedEventNames() {
			if name == "FacilityLayoutImported" {
				imported++
			}
		}
		if imported != 1 {
			t.Fatalf("expected exactly one FacilityLayoutImported, got %d", imported)
		}
	})

	t.Run("reports partial success rather than aborting on the first bad row", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
		h.mustRegisterLocationType(placement.Shelf, 60, 0.4)
		mustDefineRule(t, h, "RULE-FRZ-NO-SHELF", placement.Shelf, placement.Deny, mustPredicate(t, "", shared.Frozen, nil))

		good1 := row("STOR", "AMB", shared.Ambient, false, "A07", 7, "03", "02", "B", placement.PalletRack)
		malformed := row("STOR", "AMB", shared.Ambient, false, "A07", 7, "03", "02", "b", placement.PalletRack)
		unknownType := row("STOR", "AMB", shared.Ambient, false, "A07", 7, "03", "02", "D", "Hovercraft")
		ruleViolation := row("STOR", "FRZ", shared.Frozen, false, "A02", 2, "01", "01", "A", placement.Shelf)
		good2 := row("STOR", "AMB", shared.Ambient, false, "A07", 7, "04", "01", "A", placement.PalletRack)

		report, err := h.importLayout.Execute(h.ctx(), []usecases.ImportRow{good1, malformed, unknownType, ruleViolation, good2})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if report.RowsSubmitted != 5 || report.SlotsImported != 2 || report.RowsRejected != 3 {
			t.Fatalf("unexpected report counts %+v", report)
		}

		wants := map[int]string{
			1: shared.ErrInvalidLocationSegment.Error(),
			2: usecases.ErrLocationTypeNotFound.Error(),
			3: placement.ErrPlacementRuleViolated.Error(),
		}
		for index, want := range wants {
			result := report.Results[index]
			if result.Succeeded {
				t.Fatalf("row %d should have been rejected", index)
			}
			if !strings.Contains(result.Error, want) {
				t.Fatalf("row %d: expected an error mentioning %q, got %q", index, want, result.Error)
			}
		}
		for _, index := range []int{0, 4} {
			if !report.Results[index].Succeeded {
				t.Fatalf("row %d should have succeeded, got %q", index, report.Results[index].Error)
			}
		}

		// The good rows really did land.
		if s, _ := h.slots.FindByCode(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-04-01-A")); s == nil {
			t.Fatal("expected the last good row to have been imported")
		}
	})

	t.Run("rejects an empty import", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.importLayout.Execute(h.ctx(), nil)
		assertErrorIs(t, err, usecases.ErrEmptyImport)
	})

	t.Run("reuses existing structure without redefining it", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()

		// The row claims a different temperature class for an existing zone
		// and a different walk-order hint for an existing aisle; the import
		// must reuse the existing structure, never silently mutate it.
		r := row("STOR", "AMB", shared.Frozen, true, "A07", 99, "03", "02", "B", placement.PalletRack)
		report, err := h.importLayout.Execute(h.ctx(), []usecases.ImportRow{r})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if report.SlotsImported != 1 {
			t.Fatalf("unexpected report %+v", report)
		}

		z, _ := h.zones.FindByID(h.ctx(), "WH1-STOR-AMB")
		if z.TemperatureClass() != shared.Ambient || z.Hazmat() {
			t.Fatalf("the import must not redefine an existing zone, got %+v", z)
		}
		a, _ := h.aisles.FindByID(h.ctx(), "WH1-STOR-AMB-A07")
		if a.SequenceHint() != 7 {
			t.Fatalf("the import must not redefine an existing aisle, got hint %d", a.SequenceHint())
		}
	})

	t.Run("rejects rows whose existing parents are not active", func(t *testing.T) {
		tests := []struct {
			name    string
			setup   func(t *testing.T, h *harness)
			wantErr string
		}{
			{
				name: "decommissioned site",
				setup: func(t *testing.T, h *harness) {
					h.seedAmbientAisle()
					decommissionSite(t, h, "WH1")
				},
				wantErr: usecases.ErrSiteNotActive.Error(),
			},
			{
				name: "decommissioned zone",
				setup: func(t *testing.T, h *harness) {
					h.seedAmbientAisle()
					decommissionZone(t, h, "WH1-STOR-AMB")
				},
				wantErr: usecases.ErrZoneNotActive.Error(),
			},
			{
				name: "decommissioned aisle",
				setup: func(t *testing.T, h *harness) {
					h.seedAmbientAisle()
					decommissionAisle(t, h, "WH1-STOR-AMB-A07")
				},
				wantErr: usecases.ErrAisleNotActive.Error(),
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				h := newHarness(t)
				tc.setup(t, h)

				report, err := h.importLayout.Execute(h.ctx(), []usecases.ImportRow{
					row("STOR", "AMB", shared.Ambient, false, "A07", 7, "03", "02", "B", placement.PalletRack),
				})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if report.RowsRejected != 1 || report.Results[0].Error != tc.wantErr {
					t.Fatalf("expected the row rejected with %q, got %+v", tc.wantErr, report.Results[0])
				}
				if report.Results[0].LocationCode != "WH1-STOR-AMB-A07-03-02-B" {
					t.Fatalf("the report must name the row's location code, got %q", report.Results[0].LocationCode)
				}
			})
		}
	})

	t.Run("applies a per-row capacity override and validates it", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)

		override := row("STOR", "AMB", shared.Ambient, false, "A07", 7, "03", "02", "B", placement.PalletRack)
		override.MaxWeightKg = 500
		override.MaxVolumeM3 = 1.1

		invalid := row("STOR", "AMB", shared.Ambient, false, "A07", 7, "03", "02", "C", placement.PalletRack)
		invalid.MaxWeightKg = 500 // volume left at zero: an incomplete envelope

		report, err := h.importLayout.Execute(h.ctx(), []usecases.ImportRow{override, invalid})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if report.SlotsImported != 1 || report.RowsRejected != 1 {
			t.Fatalf("unexpected report %+v", report)
		}
		if !strings.Contains(report.Results[1].Error, shared.ErrInvalidMaxVolume.Error()) {
			t.Fatalf("expected an invalid-volume rejection, got %q", report.Results[1].Error)
		}

		s, _ := h.slots.FindByCode(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"))
		if s.Capacity().MaxWeightKg() != 500 || s.Capacity().MaxVolumeM3() != 1.1 {
			t.Fatalf("expected the row's override envelope, got %v", s.Capacity())
		}
	})

	t.Run("defaults the site name and aisle direction when a row omits them", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)

		r := row("STOR", "AMB", shared.Ambient, false, "A07", 7, "03", "02", "B", placement.PalletRack)
		r.SiteName = ""
		r.Direction = ""

		if _, err := h.importLayout.Execute(h.ctx(), []usecases.ImportRow{r}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		s, _ := h.sites.FindByCode(h.ctx(), "WH1")
		if s.Name() != "WH1" {
			t.Fatalf("expected the site code as the fallback name, got %q", s.Name())
		}
		a, _ := h.aisles.FindByID(h.ctx(), "WH1-STOR-AMB-A07")
		if a.Direction() != shared.TwoWay {
			t.Fatalf("expected TwoWay as the fallback direction, got %q", a.Direction())
		}
	})

	t.Run("rejects rows whose structural definition is itself invalid", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)

		badTemperature := row("STOR", "AMB", "Tepid", false, "A07", 7, "03", "02", "B", placement.PalletRack)
		badSequence := row("STOR", "CHL", shared.Chilled, false, "A08", -1, "03", "02", "B", placement.PalletRack)

		report, err := h.importLayout.Execute(h.ctx(), []usecases.ImportRow{badTemperature, badSequence})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if report.RowsRejected != 2 {
			t.Fatalf("expected both rows rejected, got %+v", report)
		}
		if !strings.Contains(report.Results[0].Error, shared.ErrUnknownTemperatureClass.Error()) {
			t.Fatalf("unexpected row 0 error %q", report.Results[0].Error)
		}
		if !strings.Contains(report.Results[1].Error, "sequence hint") {
			t.Fatalf("unexpected row 1 error %q", report.Results[1].Error)
		}
	})

	t.Run("applies a row's full geometry columns to the imported slot", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)

		withGeometry := row("STOR", "AMB", shared.Ambient, false, "A07", 7, "03", "02", "B", placement.PalletRack)
		x, y, z := 12.5, 3.0, 0.0
		w, d, ht := 1.2, 0.9, 2.0
		seq := 41
		withGeometry.XM, withGeometry.YM, withGeometry.ZM = &x, &y, &z
		withGeometry.WidthM, withGeometry.DepthM, withGeometry.HeightM = &w, &d, &ht
		withGeometry.PickSequence = &seq

		report, err := h.importLayout.Execute(h.ctx(), []usecases.ImportRow{withGeometry})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if report.SlotsImported != 1 {
			t.Fatalf("expected the row imported, got %+v", report)
		}

		s, _ := h.slots.FindByCode(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"))
		if s.Position().XM() != x || s.Position().YM() != y || s.Position().ZM() != z {
			t.Fatalf("expected the row's position, got %+v", s.Position())
		}
		if s.Dimensions().WidthM() != w || s.Dimensions().DepthM() != d || s.Dimensions().HeightM() != ht {
			t.Fatalf("expected the row's dimensions, got %+v", s.Dimensions())
		}
		if s.PickSequence() == nil || *s.PickSequence() != seq {
			t.Fatalf("expected pick sequence %d, got %v", seq, s.PickSequence())
		}
		h.assertPublished("LocationGeometryUpdated")
	})

	t.Run("rejects rows whose geometry columns are invalid or incomplete", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)

		missingDimensions := row("STOR", "AMB", shared.Ambient, false, "A07", 7, "03", "02", "B", placement.PalletRack)
		x, y, z := 1.0, 2.0, 0.0
		w, ht := 1.2, 2.0
		missingDimensions.XM, missingDimensions.YM, missingDimensions.ZM = &x, &y, &z
		missingDimensions.WidthM, missingDimensions.HeightM = &w, &ht // depth omitted: not all-or-nothing

		negativeZ := row("STOR", "AMB", shared.Ambient, false, "A07", 7, "03", "02", "C", placement.PalletRack)
		zNeg := -1.0
		negativeZ.XM, negativeZ.ZM = &x, &zNeg
		negativeZ.WidthM = &w

		badSequence := row("STOR", "AMB", shared.Ambient, false, "A07", 7, "03", "02", "D", placement.PalletRack)
		dFull := 0.9
		negSeq := -3
		badSequence.XM, badSequence.YM, badSequence.ZM = &x, &y, &z
		badSequence.WidthM, badSequence.DepthM, badSequence.HeightM = &w, &dFull, &ht
		badSequence.PickSequence = &negSeq

		report, err := h.importLayout.Execute(h.ctx(), []usecases.ImportRow{missingDimensions, negativeZ, badSequence})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if report.RowsRejected != 3 {
			t.Fatalf("expected all three rows rejected, got %+v", report)
		}
		wants := []string{
			shared.ErrInvalidDimensions.Error(),
			shared.ErrInvalidZ.Error(),
			slot.ErrNegativePickSequence.Error(),
		}
		for i, want := range wants {
			if !strings.Contains(report.Results[i].Error, want) {
				t.Fatalf("row %d: expected an error mentioning %q, got %q", i, want, report.Results[i].Error)
			}
		}
	})
}
