package http_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestLocationSlotEndpoints(t *testing.T) {
	t.Run("POST /locations registers a slot", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()

		res := ts.do(http.MethodPost, "/locations", map[string]any{
			"locationCode": "WH1-STOR-AMB-A07-03-02-B", "locationType": "PalletRack",
		}).assertStatus(t, http.StatusCreated)

		if got := res.headers.Get("Location"); got != "/locations/WH1-STOR-AMB-A07-03-02-B" {
			t.Fatalf("unexpected Location %q", got)
		}

		var slot struct {
			LocationCode string `json:"locationCode"`
			ZoneID       string `json:"zoneId"`
			AisleID      string `json:"aisleId"`
			LocationType string `json:"locationType"`
			Status       string `json:"status"`
			Coordinates  struct {
				Site, Area, Zone, Aisle, Bay, Level, Position string
			} `json:"coordinates"`
			Capacity struct {
				MaxWeightKg float64 `json:"maxWeightKg"`
				MaxVolumeM3 float64 `json:"maxVolumeM3"`
			} `json:"capacity"`
		}
		res.decode(t, &slot)

		if slot.LocationCode != "WH1-STOR-AMB-A07-03-02-B" || slot.ZoneID != "WH1-STOR-AMB" || slot.AisleID != "WH1-STOR-AMB-A07" {
			t.Fatalf("unexpected slot body %+v", slot)
		}
		if slot.Coordinates.Bay != "03" || slot.Coordinates.Level != "02" || slot.Coordinates.Position != "B" {
			t.Fatalf("expected the exploded coordinates, got %+v", slot.Coordinates)
		}
		if slot.Capacity.MaxWeightKg != 1200 || slot.Capacity.MaxVolumeM3 != 2.4 {
			t.Fatalf("expected the location type default envelope, got %+v", slot.Capacity)
		}
		if slot.Status != "Active" {
			t.Fatalf("expected Active, got %q", slot.Status)
		}

		ts.do(http.MethodGet, res.headers.Get("Location"), nil).assertStatus(t, http.StatusOK)
	})

	t.Run("POST /locations honours a capacity override", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()

		res := ts.do(http.MethodPost, "/locations", map[string]any{
			"locationCode": "WH1-STOR-AMB-A07-03-02-B", "locationType": "PalletRack",
			"capacityOverride": map[string]any{"maxWeightKg": 400, "maxVolumeM3": 0.9},
		}).assertStatus(t, http.StatusCreated)

		var slot struct {
			Capacity struct {
				MaxWeightKg float64 `json:"maxWeightKg"`
			} `json:"capacity"`
		}
		res.decode(t, &slot)
		if slot.Capacity.MaxWeightKg != 400 {
			t.Fatalf("expected the override envelope, got %+v", slot.Capacity)
		}
	})

	t.Run("POST /locations rejects an invalid capacity override with 422", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.do(http.MethodPost, "/locations", map[string]any{
			"locationCode": "WH1-STOR-AMB-A07-03-02-B", "locationType": "PalletRack",
			"capacityOverride": map[string]any{"maxWeightKg": 400, "maxVolumeM3": 0},
		}).assertProblem(t, http.StatusUnprocessableEntity, "invalid-max-volume")
	})

	t.Run("POST /locations rejects a malformed location code with 400", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.do(http.MethodPost, "/locations", map[string]any{
			"locationCode": "WH1-STOR-AMB-A07", "locationType": "PalletRack",
		}).assertProblem(t, http.StatusBadRequest, "malformed-location-code")
	})

	t.Run("POST /locations rejects a duplicate location code with 409", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.seedSlot("WH1-STOR-AMB-A07-03-02-B", "PalletRack")
		ts.do(http.MethodPost, "/locations", map[string]any{
			"locationCode": "WH1-STOR-AMB-A07-03-02-B", "locationType": "PalletRack",
		}).assertProblem(t, http.StatusConflict, "duplicate-location-code")
	})

	t.Run("POST /locations rejects a broken chain of custody", func(t *testing.T) {
		tests := []struct {
			name       string
			code       string
			wantStatus int
			wantSlug   string
		}{
			{name: "unknown site", code: "WH9-STOR-AMB-A07-03-02-B", wantStatus: http.StatusNotFound, wantSlug: "site-not-found"},
			{name: "unknown zone", code: "WH1-STOR-FRZ-A07-03-02-B", wantStatus: http.StatusNotFound, wantSlug: "zone-not-found"},
			{name: "unknown aisle", code: "WH1-STOR-AMB-A99-03-02-B", wantStatus: http.StatusNotFound, wantSlug: "aisle-not-found"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				ts := newTestServer(t)
				ts.seedStorageAisle()
				ts.do(http.MethodPost, "/locations", map[string]any{
					"locationCode": tc.code, "locationType": "PalletRack",
				}).assertProblem(t, tc.wantStatus, tc.wantSlug)
			})
		}
	})

	t.Run("POST /locations rejects a placement-rule violation with 422 naming the rule", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		ts.seedZone("STOR", "FRZ", "Frozen", false)
		ts.seedAisle("WH1-STOR-FRZ", "A02", 2, "TwoWay")
		ts.seedLocationType("Shelf", 60, 0.4)
		ts.do(http.MethodPost, "/placement-rules", map[string]any{
			"ruleId": "RULE-FRZ-NO-SHELF", "locationType": "Shelf", "effect": "Deny",
			"zone": map[string]any{"temperatureClass": "Frozen"},
		}).assertStatus(t, http.StatusCreated)

		res := ts.do(http.MethodPost, "/locations", map[string]any{
			"locationCode": "WH1-STOR-FRZ-A02-01-01-A", "locationType": "Shelf",
		})
		res.assertProblem(t, http.StatusUnprocessableEntity, "placement-rule-violated")

		var problem struct {
			Detail string `json:"detail"`
		}
		res.decode(t, &problem)
		if !strings.Contains(problem.Detail, "RULE-FRZ-NO-SHELF") {
			t.Fatalf("the problem detail must name the violated rule, got %q", problem.Detail)
		}
	})

	t.Run("POST /locations rejects an unknown location type with 404", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.do(http.MethodPost, "/locations", map[string]any{
			"locationCode": "WH1-STOR-AMB-A07-03-02-B", "locationType": "Hovercraft",
		}).assertProblem(t, http.StatusNotFound, "location-type-not-found")
	})

	t.Run("POST /locations rejects a malformed body with 400", func(t *testing.T) {
		newTestServer(t).doRaw(http.MethodPost, "/locations", "[").
			assertProblem(t, http.StatusBadRequest, "malformed-request-body")
	})

	t.Run("GET /locations/{locationCode} reads and 404s", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.seedSlot("WH1-STOR-AMB-A07-03-02-B", "PalletRack")

		ts.do(http.MethodGet, "/locations/WH1-STOR-AMB-A07-03-02-B", nil).assertStatus(t, http.StatusOK)
		ts.do(http.MethodGet, "/locations/WH1-STOR-AMB-A07-99-99-Z", nil).
			assertProblem(t, http.StatusNotFound, "location-slot-not-found")
		ts.do(http.MethodGet, "/locations/NOT-A-CODE", nil).
			assertProblem(t, http.StatusBadRequest, "malformed-location-code")
	})
}

func TestLocationClassificationEndpoint(t *testing.T) {
	t.Run("returns hazmat=true for a hazmat zone", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		ts.seedZone("STOR", "HAZ", "Ambient", true)
		ts.seedAisle("WH1-STOR-HAZ", "A01", 1, "TwoWay")
		ts.seedLocationType("PalletRack", 1200, 2.4)
		ts.seedSlot("WH1-STOR-HAZ-A01-01-01-A", "PalletRack")

		res := ts.do(http.MethodGet, "/locations/WH1-STOR-HAZ-A01-01-01-A/classification", nil).assertStatus(t, http.StatusOK)

		var got struct {
			Hazmat           bool   `json:"hazmat"`
			TemperatureClass string `json:"temperatureClass"`
		}
		res.decode(t, &got)
		if !got.Hazmat || got.TemperatureClass != "Ambient" {
			t.Fatalf("unexpected classification %+v", got)
		}
	})

	t.Run("returns hazmat=false for a non-hazmat zone", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.seedSlot("WH1-STOR-AMB-A07-03-02-B", "PalletRack")

		res := ts.do(http.MethodGet, "/locations/WH1-STOR-AMB-A07-03-02-B/classification", nil).assertStatus(t, http.StatusOK)

		var got struct {
			Hazmat           bool   `json:"hazmat"`
			TemperatureClass string `json:"temperatureClass"`
		}
		res.decode(t, &got)
		if got.Hazmat || got.TemperatureClass != "Ambient" {
			t.Fatalf("unexpected classification %+v", got)
		}
	})

	t.Run("reports each temperature class", func(t *testing.T) {
		tests := []struct {
			zoneCode         string
			temperatureClass string
			locationCode     string
		}{
			{zoneCode: "AMB", temperatureClass: "Ambient", locationCode: "WH1-STOR-AMB-A01-01-01-A"},
			{zoneCode: "CHL", temperatureClass: "Chilled", locationCode: "WH1-STOR-CHL-A01-01-01-A"},
			{zoneCode: "FRZ", temperatureClass: "Frozen", locationCode: "WH1-STOR-FRZ-A01-01-01-A"},
		}
		for _, tc := range tests {
			t.Run(tc.temperatureClass, func(t *testing.T) {
				ts := newTestServer(t)
				ts.seedSite()
				ts.seedZone("STOR", tc.zoneCode, tc.temperatureClass, false)
				ts.seedAisle("WH1-STOR-"+tc.zoneCode, "A01", 1, "TwoWay")
				ts.seedLocationType("PalletRack", 1200, 2.4)
				ts.seedSlot(tc.locationCode, "PalletRack")

				res := ts.do(http.MethodGet, "/locations/"+tc.locationCode+"/classification", nil).assertStatus(t, http.StatusOK)

				var got struct {
					TemperatureClass string `json:"temperatureClass"`
				}
				res.decode(t, &got)
				if got.TemperatureClass != tc.temperatureClass {
					t.Fatalf("expected %q, got %q", tc.temperatureClass, got.TemperatureClass)
				}
			})
		}
	})

	t.Run("404s for an unknown location code", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()

		ts.do(http.MethodGet, "/locations/WH1-STOR-AMB-A07-99-99-Z/classification", nil).
			assertProblem(t, http.StatusNotFound, "location-slot-not-found")
	})

	t.Run("400s for a malformed location code", func(t *testing.T) {
		ts := newTestServer(t)

		ts.do(http.MethodGet, "/locations/NOT-A-CODE/classification", nil).
			assertProblem(t, http.StatusBadRequest, "malformed-location-code")
	})
}

func TestDecommissionEndpoint(t *testing.T) {
	t.Run("returns 204 and flips the status", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.seedSlot("WH1-STOR-AMB-A07-03-02-B", "PalletRack")

		res := ts.do(http.MethodPost, "/locations/WH1-STOR-AMB-A07-03-02-B/decommissionings", nil).
			assertStatus(t, http.StatusNoContent)
		if len(res.body) != 0 {
			t.Fatalf("expected an empty 204 body, got %q", string(res.body))
		}

		read := ts.do(http.MethodGet, "/locations/WH1-STOR-AMB-A07-03-02-B", nil).assertStatus(t, http.StatusOK)
		var slot struct {
			Status string `json:"status"`
		}
		read.decode(t, &slot)
		if slot.Status != "Decommissioned" {
			t.Fatalf("expected Decommissioned, got %q", slot.Status)
		}
	})

	t.Run("is one-way: a second call is 409", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.seedSlot("WH1-STOR-AMB-A07-03-02-B", "PalletRack")

		ts.do(http.MethodPost, "/locations/WH1-STOR-AMB-A07-03-02-B/decommissionings", nil).assertStatus(t, http.StatusNoContent)
		ts.do(http.MethodPost, "/locations/WH1-STOR-AMB-A07-03-02-B/decommissionings", nil).
			assertProblem(t, http.StatusConflict, "already-decommissioned")
	})

	t.Run("404s for an unknown slot and 400s for a malformed code", func(t *testing.T) {
		ts := newTestServer(t)
		ts.do(http.MethodPost, "/locations/WH1-STOR-AMB-A07-03-02-B/decommissionings", nil).
			assertProblem(t, http.StatusNotFound, "location-slot-not-found")
		ts.do(http.MethodPost, "/locations/NOPE/decommissionings", nil).
			assertProblem(t, http.StatusBadRequest, "malformed-location-code")
	})
}

func TestImportEndpoint(t *testing.T) {
	importRow := func(area, zone, tc, aisle string, hint int, bay, level, position, locationType string) map[string]any {
		return map[string]any{
			"siteCode": "WH1", "siteName": "Fulfilment Centre One",
			"areaCode": area, "zoneCode": zone, "temperatureClass": tc, "hazmat": false,
			"aisleCode": aisle, "sequenceHint": hint, "direction": "TwoWay",
			"bay": bay, "level": level, "position": position, "locationType": locationType,
		}
	}

	t.Run("bulk-loads a layout and reports every row", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedLocationType("PalletRack", 1200, 2.4)

		res := ts.do(http.MethodPost, "/location-imports", []map[string]any{
			importRow("STOR", "AMB", "Ambient", "A07", 7, "03", "01", "A", "PalletRack"),
			importRow("STOR", "AMB", "Ambient", "A07", 7, "03", "02", "B", "PalletRack"),
			importRow("STOR", "FRZ", "Frozen", "A02", 2, "01", "01", "A", "PalletRack"),
		}).assertStatus(t, http.StatusOK)

		var report struct {
			RowsSubmitted int `json:"rowsSubmitted"`
			SlotsImported int `json:"slotsImported"`
			RowsRejected  int `json:"rowsRejected"`
			Results       []struct {
				Index        int    `json:"index"`
				LocationCode string `json:"locationCode"`
				Succeeded    bool   `json:"succeeded"`
			} `json:"results"`
		}
		res.decode(t, &report)

		if report.RowsSubmitted != 3 || report.SlotsImported != 3 || report.RowsRejected != 0 {
			t.Fatalf("unexpected report %+v", report)
		}
		if len(report.Results) != 3 || report.Results[2].LocationCode != "WH1-STOR-FRZ-A02-01-01-A" {
			t.Fatalf("unexpected results %+v", report.Results)
		}

		// The whole structure really exists afterwards.
		ts.do(http.MethodGet, "/sites/WH1", nil).assertStatus(t, http.StatusOK)
		ts.do(http.MethodGet, "/zones/WH1-STOR-FRZ", nil).assertStatus(t, http.StatusOK)
		ts.do(http.MethodGet, "/locations/WH1-STOR-AMB-A07-03-02-B", nil).assertStatus(t, http.StatusOK)
	})

	t.Run("reports partial success with per-row errors", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedLocationType("PalletRack", 1200, 2.4)

		bad := importRow("STOR", "AMB", "Ambient", "A07", 7, "03", "02", "b", "PalletRack")
		unknownType := importRow("STOR", "AMB", "Ambient", "A07", 7, "03", "03", "A", "Hovercraft")

		res := ts.do(http.MethodPost, "/location-imports", []map[string]any{
			importRow("STOR", "AMB", "Ambient", "A07", 7, "03", "01", "A", "PalletRack"),
			bad,
			unknownType,
		}).assertStatus(t, http.StatusOK)

		var report struct {
			SlotsImported int `json:"slotsImported"`
			RowsRejected  int `json:"rowsRejected"`
			Results       []struct {
				Succeeded bool   `json:"succeeded"`
				Error     string `json:"error"`
			} `json:"results"`
		}
		res.decode(t, &report)

		if report.SlotsImported != 1 || report.RowsRejected != 2 {
			t.Fatalf("unexpected report %+v", report)
		}
		if report.Results[0].Error != "" || !report.Results[0].Succeeded {
			t.Fatalf("row 0 should have succeeded: %+v", report.Results[0])
		}
		if !strings.Contains(report.Results[1].Error, "uppercase") {
			t.Fatalf("row 1 should name the segment problem: %q", report.Results[1].Error)
		}
		if !strings.Contains(report.Results[2].Error, "location type not found") {
			t.Fatalf("row 2 should name the missing location type: %q", report.Results[2].Error)
		}
	})

	t.Run("applies a per-row capacity override", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedLocationType("PalletRack", 1200, 2.4)

		row := importRow("STOR", "AMB", "Ambient", "A07", 7, "03", "01", "A", "PalletRack")
		row["capacityOverride"] = map[string]any{"maxWeightKg": 750, "maxVolumeM3": 1.8}

		ts.do(http.MethodPost, "/location-imports", []map[string]any{row}).assertStatus(t, http.StatusOK)

		read := ts.do(http.MethodGet, "/locations/WH1-STOR-AMB-A07-03-01-A", nil).assertStatus(t, http.StatusOK)
		var slot struct {
			Capacity struct {
				MaxWeightKg float64 `json:"maxWeightKg"`
			} `json:"capacity"`
		}
		read.decode(t, &slot)
		if slot.Capacity.MaxWeightKg != 750 {
			t.Fatalf("expected the row's override envelope, got %+v", slot.Capacity)
		}
	})

	t.Run("rejects an empty import with 400", func(t *testing.T) {
		newTestServer(t).do(http.MethodPost, "/location-imports", []map[string]any{}).
			assertProblem(t, http.StatusBadRequest, "empty-import")
	})

	t.Run("rejects a malformed body with 400", func(t *testing.T) {
		newTestServer(t).doRaw(http.MethodPost, "/location-imports", "{}").
			assertProblem(t, http.StatusBadRequest, "malformed-request-body")
	})
}
