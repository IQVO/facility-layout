package http_test

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"strings"
	"testing"
)

// seedDrawableSite builds a small but realistic WH1 through the REAL REST
// API: two zones, three aisles seeded out of walk order, five slots.
func seedDrawableSite(t *testing.T) *testServer {
	t.Helper()
	ts := newTestServer(t)

	ts.seedSite()
	ts.seedLocationType("PalletRack", 1200, 2.4)
	ts.seedLocationType("Shelf", 60, 0.4)

	ts.seedZone("STOR", "AMB", "Ambient", false)
	ts.seedZone("RCV", "AMB", "Ambient", false)

	ts.seedAisle("WH1-STOR-AMB", "A09", 9, "OneWay")
	ts.seedAisle("WH1-STOR-AMB", "A07", 7, "TwoWay")
	ts.seedAisle("WH1-RCV-AMB", "D01", 1, "TwoWay")

	ts.seedSlot("WH1-STOR-AMB-A07-03-02-B", "PalletRack")
	ts.seedSlot("WH1-STOR-AMB-A07-03-02-A", "PalletRack")
	ts.seedSlot("WH1-STOR-AMB-A07-03-01-A", "PalletRack")
	ts.seedSlot("WH1-STOR-AMB-A09-01-01-A", "Shelf")
	ts.seedSlot("WH1-RCV-AMB-D01-01-01-A", "Shelf")

	return ts
}

// layoutBody mirrors GET /sites/{siteCode}/layout's JSON contract.
type layoutBody struct {
	Site struct {
		SiteCode string `json:"siteCode"`
		Name     string `json:"name"`
	} `json:"site"`
	Totals struct {
		Zones  int `json:"zones"`
		Aisles int `json:"aisles"`
		Slots  int `json:"slots"`
	} `json:"totals"`
	Zones []struct {
		ZoneID           string `json:"zoneId"`
		AreaCode         string `json:"areaCode"`
		ZoneCode         string `json:"zoneCode"`
		TemperatureClass string `json:"temperatureClass"`
		Hazmat           bool   `json:"hazmat"`
		Status           string `json:"status"`
		Aisles           []struct {
			AisleID      string `json:"aisleId"`
			AisleCode    string `json:"aisleCode"`
			SequenceHint int    `json:"sequenceHint"`
			Direction    string `json:"direction"`
			Slots        []struct {
				LocationCode string `json:"locationCode"`
				LocationType string `json:"locationType"`
				Status       string `json:"status"`
				Coordinates  struct {
					Bay      string `json:"bay"`
					Level    string `json:"level"`
					Position string `json:"position"`
				} `json:"coordinates"`
			} `json:"slots"`
		} `json:"aisles"`
	} `json:"zones"`
}

func TestGetSiteLayoutEndpoint(t *testing.T) {
	t.Run("returns the full nested drawable structure", func(t *testing.T) {
		ts := seedDrawableSite(t)
		res := ts.do(http.MethodGet, "/sites/WH1/layout", nil).assertStatus(t, http.StatusOK)

		if ct := res.headers.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("expected application/json, got %q", ct)
		}

		var layout layoutBody
		res.decode(t, &layout)

		if layout.Site.SiteCode != "WH1" || layout.Site.Name != "Fulfilment Centre One" {
			t.Fatalf("unexpected site %+v", layout.Site)
		}
		if layout.Totals.Zones != 2 || layout.Totals.Aisles != 3 || layout.Totals.Slots != 5 {
			t.Fatalf("unexpected totals %+v", layout.Totals)
		}

		// Zones ordered by id: RCV before STOR.
		if len(layout.Zones) != 2 || layout.Zones[0].ZoneID != "WH1-RCV-AMB" || layout.Zones[1].ZoneID != "WH1-STOR-AMB" {
			t.Fatalf("expected zones ordered by id, got %+v", layout.Zones)
		}
		// Zone behaviour is inlined, not a separate lookup for the client.
		if layout.Zones[1].AreaCode != "STOR" || layout.Zones[1].TemperatureClass != "Ambient" || layout.Zones[1].Status != "Active" {
			t.Fatalf("unexpected zone attributes %+v", layout.Zones[1])
		}

		storage := layout.Zones[1]
		// Aisles ordered by walk order, not registration order.
		if len(storage.Aisles) != 2 || storage.Aisles[0].AisleCode != "A07" || storage.Aisles[1].AisleCode != "A09" {
			t.Fatalf("expected aisles in SequenceHint order, got %+v", storage.Aisles)
		}
		if storage.Aisles[0].SequenceHint != 7 || storage.Aisles[0].Direction != "TwoWay" {
			t.Fatalf("expected the travel metadata inlined, got %+v", storage.Aisles[0])
		}

		// Slots ordered bay -> level -> position, ready to paint.
		a07 := storage.Aisles[0]
		want := []string{"WH1-STOR-AMB-A07-03-01-A", "WH1-STOR-AMB-A07-03-02-A", "WH1-STOR-AMB-A07-03-02-B"}
		if len(a07.Slots) != len(want) {
			t.Fatalf("expected %d slots in A07, got %d", len(want), len(a07.Slots))
		}
		for i, code := range want {
			if a07.Slots[i].LocationCode != code {
				t.Fatalf("expected slot %d to be %q, got %q", i, code, a07.Slots[i].LocationCode)
			}
		}
		if a07.Slots[2].Coordinates.Bay != "03" || a07.Slots[2].Coordinates.Level != "02" || a07.Slots[2].Coordinates.Position != "B" {
			t.Fatalf("expected exploded coordinates, got %+v", a07.Slots[2].Coordinates)
		}
	})

	t.Run("an empty site renders as empty arrays, never null", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		res := ts.do(http.MethodGet, "/sites/WH1/layout", nil).assertStatus(t, http.StatusOK)

		var raw map[string]json.RawMessage
		res.decode(t, &raw)
		if string(raw["zones"]) != "[]" {
			t.Fatalf("expected zones to be [], got %s", raw["zones"])
		}
	})

	t.Run("404s for an unknown site", func(t *testing.T) {
		newTestServer(t).do(http.MethodGet, "/sites/NOPE/layout", nil).
			assertProblem(t, http.StatusNotFound, "site-not-found")
	})
}

// gridBody mirrors GET /zones/{zoneId}/grid's JSON contract.
type gridBody struct {
	Zone struct {
		ZoneID           string `json:"zoneId"`
		TemperatureClass string `json:"temperatureClass"`
	} `json:"zone"`
	Columns []struct {
		AisleID      string `json:"aisleId"`
		AisleCode    string `json:"aisleCode"`
		Bay          string `json:"bay"`
		SequenceHint int    `json:"sequenceHint"`
	} `json:"columns"`
	Levels []string `json:"levels"`
	Rows   []struct {
		Level string `json:"level"`
		Cells []*struct {
			Positions []struct {
				LocationCode string `json:"locationCode"`
				Position     string `json:"position"`
				LocationType string `json:"locationType"`
				Status       string `json:"status"`
			} `json:"positions"`
		} `json:"cells"`
	} `json:"rows"`
}

func TestGetZoneGridEndpoint(t *testing.T) {
	t.Run("returns a level x (aisle,bay) matrix in walk order with null gaps", func(t *testing.T) {
		ts := seedDrawableSite(t)
		res := ts.do(http.MethodGet, "/zones/WH1-STOR-AMB/grid", nil).assertStatus(t, http.StatusOK)

		var grid gridBody
		res.decode(t, &grid)

		if grid.Zone.ZoneID != "WH1-STOR-AMB" || grid.Zone.TemperatureClass != "Ambient" {
			t.Fatalf("unexpected zone %+v", grid.Zone)
		}

		// Columns: A07/03 (hint 7) before A09/01 (hint 9).
		if len(grid.Columns) != 2 {
			t.Fatalf("expected 2 columns, got %+v", grid.Columns)
		}
		if grid.Columns[0].AisleCode != "A07" || grid.Columns[0].Bay != "03" || grid.Columns[0].SequenceHint != 7 {
			t.Fatalf("unexpected first column %+v", grid.Columns[0])
		}
		if grid.Columns[0].AisleID != "WH1-STOR-AMB-A07" {
			t.Fatalf("expected the column to carry its aisle id, got %q", grid.Columns[0].AisleID)
		}
		if grid.Columns[1].AisleCode != "A09" || grid.Columns[1].Bay != "01" {
			t.Fatalf("unexpected second column %+v", grid.Columns[1])
		}

		// Rows: one per level, each index-aligned with Columns.
		if len(grid.Levels) != 2 || grid.Levels[0] != "01" || grid.Levels[1] != "02" {
			t.Fatalf("unexpected levels %v", grid.Levels)
		}
		if len(grid.Rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(grid.Rows))
		}
		for _, row := range grid.Rows {
			if len(row.Cells) != len(grid.Columns) {
				t.Fatalf("row %q has %d cells but the grid has %d columns", row.Level, len(row.Cells), len(grid.Columns))
			}
		}

		level01, level02 := grid.Rows[0], grid.Rows[1]
		if level01.Cells[0] == nil || len(level01.Cells[0].Positions) != 1 ||
			level01.Cells[0].Positions[0].LocationCode != "WH1-STOR-AMB-A07-03-01-A" {
			t.Fatalf("unexpected cell (01, A07/03): %+v", level01.Cells[0])
		}
		if level01.Cells[1] == nil || level01.Cells[1].Positions[0].LocationType != "Shelf" {
			t.Fatalf("unexpected cell (01, A09/01): %+v", level01.Cells[1])
		}

		if level02.Cells[0] == nil || len(level02.Cells[0].Positions) != 2 {
			t.Fatalf("expected both positions at (02, A07/03): %+v", level02.Cells[0])
		}
		if level02.Cells[0].Positions[0].Position != "A" || level02.Cells[0].Positions[1].Position != "B" {
			t.Fatalf("expected positions ordered A then B, got %+v", level02.Cells[0].Positions)
		}
		// The gap really is JSON null, per CLAUDE.md's grid contract.
		if level02.Cells[1] != nil {
			t.Fatalf("expected a null gap at (02, A09/01), got %+v", level02.Cells[1])
		}
	})

	t.Run("the gap really serializes as null", func(t *testing.T) {
		ts := seedDrawableSite(t)
		res := ts.do(http.MethodGet, "/zones/WH1-STOR-AMB/grid", nil).assertStatus(t, http.StatusOK)
		if !strings.Contains(string(res.body), "null") {
			t.Fatalf("expected a literal null cell in the grid JSON, got %s", string(res.body))
		}
	})

	t.Run("a zone with no slots renders as empty arrays, never null", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		ts.seedZone("STOR", "AMB", "Ambient", false)
		ts.seedAisle("WH1-STOR-AMB", "A07", 7, "TwoWay")

		res := ts.do(http.MethodGet, "/zones/WH1-STOR-AMB/grid", nil).assertStatus(t, http.StatusOK)
		var raw map[string]json.RawMessage
		res.decode(t, &raw)
		for _, key := range []string{"columns", "levels", "rows"} {
			if string(raw[key]) != "[]" {
				t.Fatalf("expected %s to be [], got %s", key, raw[key])
			}
		}
	})

	t.Run("404s for an unknown zone", func(t *testing.T) {
		newTestServer(t).do(http.MethodGet, "/zones/WH1-STOR-NOPE/grid", nil).
			assertProblem(t, http.StatusNotFound, "zone-not-found")
	})
}

func TestGetSiteLayoutAsSVG(t *testing.T) {
	t.Run("renders a valid, viewable SVG floor plan", func(t *testing.T) {
		ts := seedDrawableSite(t)
		res := ts.do(http.MethodGet, "/sites/WH1/layout?format=svg", nil).assertStatus(t, http.StatusOK)

		if ct := res.headers.Get("Content-Type"); ct != "image/svg+xml" {
			t.Fatalf("expected image/svg+xml, got %q", ct)
		}

		document := string(res.body)
		if !strings.HasPrefix(document, `<svg xmlns="http://www.w3.org/2000/svg"`) {
			t.Fatalf("expected an SVG root element, got %.80q", document)
		}
		if !strings.HasSuffix(strings.TrimSpace(document), "</svg>") {
			t.Fatalf("expected the document to close with </svg>, got %.80q", document[len(document)-80:])
		}

		// It must be well-formed XML, not just string-shaped like SVG.
		decoder := xml.NewDecoder(strings.NewReader(document))
		for {
			_, err := decoder.Token()
			if err != nil {
				if err.Error() == "EOF" {
					break
				}
				t.Fatalf("the SVG is not well-formed XML: %v", err)
			}
		}

		// Every zone and every slot is actually drawn.
		for _, want := range []string{"WH1-STOR-AMB", "WH1-RCV-AMB", "WH1-STOR-AMB-A07-03-02-B", "aisle A07"} {
			if !strings.Contains(document, want) {
				t.Fatalf("expected the SVG to mention %q", want)
			}
		}
	})

	t.Run("colours frozen and hazmat zones distinctly", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		ts.seedZone("STOR", "FRZ", "Frozen", false)
		ts.seedZone("STOR", "HAZ", "Ambient", true)
		ts.seedZone("STOR", "CHL", "Chilled", false)
		ts.seedAisle("WH1-STOR-FRZ", "A02", 2, "TwoWay")
		ts.seedLocationType("PalletRack", 1200, 2.4)
		ts.seedSlot("WH1-STOR-FRZ-A02-01-01-A", "PalletRack")
		ts.do(http.MethodPost, "/locations/WH1-STOR-FRZ-A02-01-01-A/decommission", nil).assertStatus(t, http.StatusNoContent)

		res := ts.do(http.MethodGet, "/sites/WH1/layout?format=svg", nil).assertStatus(t, http.StatusOK)
		document := string(res.body)

		for _, want := range []string{"#e0f0ff", "#fff3c4", "#e3f9e5", "HAZMAT", "#e12d39"} {
			if !strings.Contains(document, want) {
				t.Fatalf("expected the SVG to contain %q", want)
			}
		}
	})

	t.Run("404s for an unknown site before rendering anything", func(t *testing.T) {
		newTestServer(t).do(http.MethodGet, "/sites/NOPE/layout?format=svg", nil).
			assertProblem(t, http.StatusNotFound, "site-not-found")
	})

	t.Run("rejects a format outside the documented enum with 400", func(t *testing.T) {
		seedDrawableSite(t).do(http.MethodGet, "/sites/WH1/layout?format=AAA", nil).
			assertProblem(t, http.StatusBadRequest, "invalid-layout-format")
	})
}
