package http_test

import (
	"net/http"
	"testing"
)

// TestListCrossAislesHTTP pins the read half of the cross-aisle resource
// (ADR-0017): GET /zones/{zoneId}/cross-aisles.
func TestListCrossAislesHTTP(t *testing.T) {
	t.Run("lists the zone's declared connections", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedThreeAisleZone()
		ts.do(http.MethodPost, "/zones/WH1-STOR-AMB/cross-aisles", map[string]any{
			"fromAisle": "A07", "toAisle": "A08", "atBay": "02",
		}).assertStatus(t, http.StatusCreated)
		ts.do(http.MethodPost, "/zones/WH1-STOR-AMB/cross-aisles", map[string]any{
			"fromAisle": "A08", "toAisle": "A09", "atBay": "01",
		}).assertStatus(t, http.StatusCreated)

		resp := ts.do(http.MethodGet, "/zones/WH1-STOR-AMB/cross-aisles", nil).assertStatus(t, http.StatusOK)

		var body []struct {
			ZoneID    string `json:"zoneId"`
			FromAisle string `json:"fromAisle"`
			ToAisle   string `json:"toAisle"`
			AtBay     string `json:"atBay"`
			Active    bool   `json:"active"`
		}
		resp.decode(t, &body)
		if len(body) != 2 {
			t.Fatalf("expected 2 cross-aisles, got %d", len(body))
		}
		if body[0].FromAisle != "A07" || body[0].ToAisle != "A08" || body[0].AtBay != "02" || !body[0].Active {
			t.Fatalf("unexpected first cross-aisle %+v", body[0])
		}
	})

	t.Run("returns an empty array, not null, for a zone with none", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedThreeAisleZone()

		resp := ts.do(http.MethodGet, "/zones/WH1-STOR-AMB/cross-aisles", nil).assertStatus(t, http.StatusOK)
		var body []struct {
			ZoneID string `json:"zoneId"`
		}
		resp.decode(t, &body)
		if body == nil || len(body) != 0 {
			t.Fatalf("expected a non-nil empty JSON array, got %s", string(resp.body))
		}
	})

	t.Run("404s on an unknown zone", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedThreeAisleZone()

		ts.do(http.MethodGet, "/zones/WH1-STOR-XXX/cross-aisles", nil).
			assertProblem(t, http.StatusNotFound, "zone-not-found")
	})
}

// TestZonePitchHTTP pins the ADR-0017 pitch contract on the wire: optional
// bayPitchM/levelPitchM on register, effective values always readable on
// the zone response.
func TestZonePitchHTTP(t *testing.T) {
	t.Run("defaults are reflected when no pitch is sent", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()

		resp := ts.do(http.MethodPost, "/sites/WH1/zones", map[string]any{
			"areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "Ambient", "hazmat": false,
		}).assertStatus(t, http.StatusCreated)

		var body struct {
			BayPitchM   float64 `json:"bayPitchM"`
			LevelPitchM float64 `json:"levelPitchM"`
		}
		resp.decode(t, &body)
		if body.BayPitchM != 1.2 || body.LevelPitchM != 1.5 {
			t.Fatalf("expected default pitches 1.2/1.5, got %v/%v", body.BayPitchM, body.LevelPitchM)
		}
	})

	t.Run("accepts both pitches and echoes the effective values", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()

		resp := ts.do(http.MethodPost, "/sites/WH1/zones", map[string]any{
			"areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "Ambient", "hazmat": false,
			"bayPitchM": 1.5, "levelPitchM": 2.0,
		}).assertStatus(t, http.StatusCreated)

		var body struct {
			BayPitchM   float64 `json:"bayPitchM"`
			LevelPitchM float64 `json:"levelPitchM"`
		}
		resp.decode(t, &body)
		if body.BayPitchM != 1.5 || body.LevelPitchM != 2.0 {
			t.Fatalf("expected 1.5/2.0, got %v/%v", body.BayPitchM, body.LevelPitchM)
		}
	})

	t.Run("422s on a one-sided pitch", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()

		ts.do(http.MethodPost, "/sites/WH1/zones", map[string]any{
			"areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "Ambient", "hazmat": false,
			"bayPitchM": 1.5,
		}).assertProblem(t, http.StatusUnprocessableEntity, "invalid-pitch")

		ts.do(http.MethodPost, "/sites/WH1/zones", map[string]any{
			"areaCode": "STOR", "zoneCode": "AMB2", "temperatureClass": "Ambient", "hazmat": false,
			"levelPitchM": 2.0,
		}).assertProblem(t, http.StatusUnprocessableEntity, "invalid-pitch")
	})

	t.Run("the effective pitches survive a read-back", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		ts.do(http.MethodPost, "/sites/WH1/zones", map[string]any{
			"areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "Ambient", "hazmat": false,
			"bayPitchM": 1.5, "levelPitchM": 2.0,
		}).assertStatus(t, http.StatusCreated)

		resp := ts.do(http.MethodGet, "/zones/WH1-STOR-AMB", nil).assertStatus(t, http.StatusOK)
		var body struct {
			BayPitchM   float64 `json:"bayPitchM"`
			LevelPitchM float64 `json:"levelPitchM"`
		}
		resp.decode(t, &body)
		if body.BayPitchM != 1.5 || body.LevelPitchM != 2.0 {
			t.Fatalf("expected 1.5/2.0 on read-back, got %v/%v", body.BayPitchM, body.LevelPitchM)
		}
	})
}

// TestEstimateTravelDistanceCrossZoneHTTP pins the geometry-backed
// cross-zone estimate (ADR-0017): a beeline when BOTH endpoints carry
// position geometry, 422 otherwise.
func TestEstimateTravelDistanceCrossZoneHTTP(t *testing.T) {
	seedTwoZones := func(ts *testServer) {
		ts.seedStorageAisle() // WH1 / STOR / AMB / A07 / PalletRack
		ts.seedSlot("WH1-STOR-AMB-A07-01-01-A", "PalletRack")
		ts.seedZone("STOR", "FRZ", "Frozen", false)
		ts.seedAisle("WH1-STOR-FRZ", "B01", 1, "TwoWay")
		ts.seedSlot("WH1-STOR-FRZ-B01-01-01-A", "PalletRack")
	}
	setGeometry := func(ts *testServer, code string, x, y, z float64) {
		ts.t.Helper()
		ts.do(http.MethodPut, "/locations/"+code+"/geometry", map[string]any{
			"position":     map[string]any{"xM": x, "yM": y, "zM": z},
			"dimensions":   map[string]any{"widthM": 1, "depthM": 1, "heightM": 1},
			"pickSequence": nil,
		}).assertStatus(ts.t, http.StatusOK)
	}

	t.Run("estimates the beeline when both slots have geometry", func(t *testing.T) {
		ts := newTestServer(t)
		seedTwoZones(ts)
		setGeometry(ts, "WH1-STOR-AMB-A07-01-01-A", 0, 0, 0)
		setGeometry(ts, "WH1-STOR-FRZ-B01-01-01-A", 3, 4, 0)

		resp := ts.do(http.MethodGet, "/distance?from=WH1-STOR-AMB-A07-01-01-A&to=WH1-STOR-FRZ-B01-01-01-A", nil).
			assertStatus(t, http.StatusOK)

		var body struct {
			MetresM   float64 `json:"metresM"`
			Estimated bool    `json:"estimated"`
		}
		resp.decode(t, &body)
		if body.MetresM != 5.0 {
			t.Fatalf("expected the 3-4-5 beeline (5m), got %v", body.MetresM)
		}
		if !body.Estimated {
			t.Fatal("expected estimated=true: a beeline is not a routed path")
		}
	})

	t.Run("still 422s without geometry on both endpoints", func(t *testing.T) {
		ts := newTestServer(t)
		seedTwoZones(ts)
		setGeometry(ts, "WH1-STOR-FRZ-B01-01-01-A", 3, 4, 0)

		ts.do(http.MethodGet, "/distance?from=WH1-STOR-AMB-A07-01-01-A&to=WH1-STOR-FRZ-B01-01-01-A", nil).
			assertProblem(t, http.StatusUnprocessableEntity, "no-route-between-zones")
	})
}
