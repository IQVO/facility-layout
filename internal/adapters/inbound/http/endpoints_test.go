package http_test

import (
	"net/http"
	"testing"
)

func TestHealthz(t *testing.T) {
	res := newTestServer(t).do(http.MethodGet, "/healthz", nil).assertStatus(t, http.StatusOK)

	var body map[string]string
	res.decode(t, &body)
	if body["status"] != "ok" {
		t.Fatalf("unexpected health body %v", body)
	}
}

func TestSitesEndpoints(t *testing.T) {
	t.Run("POST /sites creates a site with a resolvable Location header", func(t *testing.T) {
		ts := newTestServer(t)
		res := ts.do(http.MethodPost, "/sites", map[string]any{"siteCode": "WH1", "name": "Fulfilment Centre One"}).
			assertStatus(t, http.StatusCreated)

		if got := res.headers.Get("Location"); got != "/sites/WH1" {
			t.Fatalf("expected Location /sites/WH1, got %q", got)
		}
		var site struct {
			SiteCode string `json:"siteCode"`
			Name     string `json:"name"`
			Status   string `json:"status"`
		}
		res.decode(t, &site)
		if site.SiteCode != "WH1" || site.Name != "Fulfilment Centre One" || site.Status != "Active" {
			t.Fatalf("unexpected site body %+v", site)
		}

		// The Location header really resolves.
		ts.do(http.MethodGet, res.headers.Get("Location"), nil).assertStatus(t, http.StatusOK)
	})

	t.Run("POST /sites rejects a duplicate with 409", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		ts.do(http.MethodPost, "/sites", map[string]any{"siteCode": "WH1", "name": "Another Name"}).
			assertProblem(t, http.StatusConflict, "duplicate-site-code")
	})

	t.Run("POST /sites rejects a malformed site code with 400", func(t *testing.T) {
		newTestServer(t).do(http.MethodPost, "/sites", map[string]any{"siteCode": "wh-1", "name": "Fulfilment Centre One"}).
			assertProblem(t, http.StatusBadRequest, "invalid-site-code")
	})

	t.Run("POST /sites rejects a malformed body with 400", func(t *testing.T) {
		newTestServer(t).doRaw(http.MethodPost, "/sites", "{not json").
			assertProblem(t, http.StatusBadRequest, "malformed-request-body")
	})

	t.Run("GET /sites lists sites", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		res := ts.do(http.MethodGet, "/sites", nil).assertStatus(t, http.StatusOK)

		var sites []struct {
			SiteCode string `json:"siteCode"`
		}
		res.decode(t, &sites)
		if len(sites) != 1 || sites[0].SiteCode != "WH1" {
			t.Fatalf("unexpected sites %+v", sites)
		}
	})

	t.Run("GET /sites/{siteCode} 404s for an unknown site", func(t *testing.T) {
		newTestServer(t).do(http.MethodGet, "/sites/NOPE", nil).
			assertProblem(t, http.StatusNotFound, "site-not-found")
	})
}

func TestZonesEndpoints(t *testing.T) {
	t.Run("POST /sites/{siteCode}/zones creates a zone", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		res := ts.do(http.MethodPost, "/sites/WH1/zones", map[string]any{
			"areaCode": "STOR", "zoneCode": "FRZ", "temperatureClass": "Frozen", "hazmat": false,
		}).assertStatus(t, http.StatusCreated)

		if got := res.headers.Get("Location"); got != "/zones/WH1-STOR-FRZ" {
			t.Fatalf("expected Location /zones/WH1-STOR-FRZ, got %q", got)
		}
		var zone struct {
			ZoneID           string `json:"zoneId"`
			TemperatureClass string `json:"temperatureClass"`
		}
		res.decode(t, &zone)
		if zone.ZoneID != "WH1-STOR-FRZ" || zone.TemperatureClass != "Frozen" {
			t.Fatalf("unexpected zone body %+v", zone)
		}
		ts.do(http.MethodGet, "/zones/WH1-STOR-FRZ", nil).assertStatus(t, http.StatusOK)
	})

	t.Run("rejects an unknown parent site with 404", func(t *testing.T) {
		newTestServer(t).do(http.MethodPost, "/sites/NOPE/zones", map[string]any{
			"areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "Ambient",
		}).assertProblem(t, http.StatusNotFound, "site-not-found")
	})

	t.Run("rejects an unknown temperature class with 422", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		ts.do(http.MethodPost, "/sites/WH1/zones", map[string]any{
			"areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "Tepid",
		}).assertProblem(t, http.StatusUnprocessableEntity, "unknown-temperature-class")
	})

	t.Run("rejects a duplicate zone with 409", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		ts.seedZone("STOR", "AMB", "Ambient", false)
		ts.do(http.MethodPost, "/sites/WH1/zones", map[string]any{
			"areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "Ambient",
		}).assertProblem(t, http.StatusConflict, "duplicate-zone")
	})

	t.Run("GET /sites/{siteCode}/zones lists a site's zones", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		ts.seedZone("STOR", "AMB", "Ambient", false)
		res := ts.do(http.MethodGet, "/sites/WH1/zones", nil).assertStatus(t, http.StatusOK)

		var zones []struct {
			ZoneID string `json:"zoneId"`
		}
		res.decode(t, &zones)
		if len(zones) != 1 || zones[0].ZoneID != "WH1-STOR-AMB" {
			t.Fatalf("unexpected zones %+v", zones)
		}

		newTestServer(t).do(http.MethodGet, "/sites/NOPE/zones", nil).
			assertProblem(t, http.StatusNotFound, "site-not-found")
	})

	t.Run("GET /zones/{zoneId} 404s for an unknown zone", func(t *testing.T) {
		newTestServer(t).do(http.MethodGet, "/zones/WH1-STOR-NOPE", nil).
			assertProblem(t, http.StatusNotFound, "zone-not-found")
	})
}

func TestAislesEndpoints(t *testing.T) {
	t.Run("POST /zones/{zoneId}/aisles creates an aisle", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		ts.seedZone("STOR", "AMB", "Ambient", false)
		res := ts.do(http.MethodPost, "/zones/WH1-STOR-AMB/aisles", map[string]any{
			"aisleCode": "A07", "sequenceHint": 7, "direction": "TwoWay",
		}).assertStatus(t, http.StatusCreated)

		if got := res.headers.Get("Location"); got != "/zones/WH1-STOR-AMB/aisles/A07" {
			t.Fatalf("unexpected Location %q", got)
		}
		var aisle struct {
			AisleID      string `json:"aisleId"`
			SequenceHint int    `json:"sequenceHint"`
			Direction    string `json:"direction"`
		}
		res.decode(t, &aisle)
		if aisle.AisleID != "WH1-STOR-AMB-A07" || aisle.SequenceHint != 7 || aisle.Direction != "TwoWay" {
			t.Fatalf("unexpected aisle body %+v", aisle)
		}
		ts.do(http.MethodGet, res.headers.Get("Location"), nil).assertStatus(t, http.StatusOK)
	})

	t.Run("rejects an unknown parent zone with 404", func(t *testing.T) {
		newTestServer(t).do(http.MethodPost, "/zones/WH1-STOR-NOPE/aisles", map[string]any{
			"aisleCode": "A07", "sequenceHint": 7, "direction": "TwoWay",
		}).assertProblem(t, http.StatusNotFound, "zone-not-found")
	})

	t.Run("rejects an unknown direction with 422", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		ts.seedZone("STOR", "AMB", "Ambient", false)
		ts.do(http.MethodPost, "/zones/WH1-STOR-AMB/aisles", map[string]any{
			"aisleCode": "A07", "sequenceHint": 7, "direction": "Sideways",
		}).assertProblem(t, http.StatusUnprocessableEntity, "unknown-direction")
	})

	t.Run("rejects a negative sequence hint with 422", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		ts.seedZone("STOR", "AMB", "Ambient", false)
		ts.do(http.MethodPost, "/zones/WH1-STOR-AMB/aisles", map[string]any{
			"aisleCode": "A07", "sequenceHint": -1, "direction": "TwoWay",
		}).assertProblem(t, http.StatusUnprocessableEntity, "negative-sequence-hint")
	})

	t.Run("rejects a missing sequence hint with 400, not a silent 0", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		ts.seedZone("STOR", "AMB", "Ambient", false)
		ts.do(http.MethodPost, "/zones/WH1-STOR-AMB/aisles", map[string]any{
			"aisleCode": "A07", "direction": "TwoWay",
		}).assertProblem(t, http.StatusBadRequest, "missing-sequence-hint")
	})

	t.Run("rejects a duplicate aisle with 409", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		ts.seedZone("STOR", "AMB", "Ambient", false)
		ts.seedAisle("WH1-STOR-AMB", "A07", 7, "TwoWay")
		ts.do(http.MethodPost, "/zones/WH1-STOR-AMB/aisles", map[string]any{
			"aisleCode": "A07", "sequenceHint": 8, "direction": "OneWay",
		}).assertProblem(t, http.StatusConflict, "duplicate-aisle")
	})

	t.Run("GET /zones/{zoneId}/aisles lists in walk order", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		ts.seedZone("STOR", "AMB", "Ambient", false)
		ts.seedAisle("WH1-STOR-AMB", "A09", 9, "OneWay")
		ts.seedAisle("WH1-STOR-AMB", "A07", 7, "TwoWay")

		res := ts.do(http.MethodGet, "/zones/WH1-STOR-AMB/aisles", nil).assertStatus(t, http.StatusOK)
		var aisles []struct {
			AisleCode string `json:"aisleCode"`
		}
		res.decode(t, &aisles)
		if len(aisles) != 2 || aisles[0].AisleCode != "A07" {
			t.Fatalf("expected walk order, got %+v", aisles)
		}

		newTestServer(t).do(http.MethodGet, "/zones/WH1-STOR-NOPE/aisles", nil).
			assertProblem(t, http.StatusNotFound, "zone-not-found")
	})

	t.Run("GET one aisle 404s when unknown", func(t *testing.T) {
		newTestServer(t).do(http.MethodGet, "/zones/WH1-STOR-AMB/aisles/A99", nil).
			assertProblem(t, http.StatusNotFound, "aisle-not-found")
	})
}

func TestLocationTypesEndpoints(t *testing.T) {
	t.Run("POST /location-types creates a type", func(t *testing.T) {
		ts := newTestServer(t)
		res := ts.do(http.MethodPost, "/location-types", map[string]any{
			"name": "PalletRack", "defaultCapacity": map[string]any{"maxWeightKg": 1200, "maxVolumeM3": 2.4},
		}).assertStatus(t, http.StatusCreated)

		if got := res.headers.Get("Location"); got != "/location-types/PalletRack" {
			t.Fatalf("unexpected Location %q", got)
		}
		ts.do(http.MethodGet, res.headers.Get("Location"), nil).assertStatus(t, http.StatusOK)
	})

	t.Run("rejects a non-positive capacity with 422", func(t *testing.T) {
		newTestServer(t).do(http.MethodPost, "/location-types", map[string]any{
			"name": "PalletRack", "defaultCapacity": map[string]any{"maxWeightKg": 0, "maxVolumeM3": 2.4},
		}).assertProblem(t, http.StatusUnprocessableEntity, "invalid-max-weight")

		newTestServer(t).do(http.MethodPost, "/location-types", map[string]any{
			"name": "PalletRack", "defaultCapacity": map[string]any{"maxWeightKg": 1200, "maxVolumeM3": 0},
		}).assertProblem(t, http.StatusUnprocessableEntity, "invalid-max-volume")
	})

	t.Run("rejects an empty name with 400", func(t *testing.T) {
		newTestServer(t).do(http.MethodPost, "/location-types", map[string]any{
			"name": "", "defaultCapacity": map[string]any{"maxWeightKg": 1200, "maxVolumeM3": 2.4},
		}).assertProblem(t, http.StatusBadRequest, "invalid-location-type")
	})

	t.Run("rejects a duplicate with 409", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedLocationType("PalletRack", 1200, 2.4)
		ts.do(http.MethodPost, "/location-types", map[string]any{
			"name": "PalletRack", "defaultCapacity": map[string]any{"maxWeightKg": 900, "maxVolumeM3": 2},
		}).assertProblem(t, http.StatusConflict, "duplicate-location-type")
	})

	t.Run("GET /location-types lists types and 404s on an unknown one", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedLocationType("PalletRack", 1200, 2.4)

		res := ts.do(http.MethodGet, "/location-types", nil).assertStatus(t, http.StatusOK)
		var types []struct {
			Name            string `json:"name"`
			DefaultCapacity struct {
				MaxWeightKg float64 `json:"maxWeightKg"`
			} `json:"defaultCapacity"`
		}
		res.decode(t, &types)
		if len(types) != 1 || types[0].Name != "PalletRack" || types[0].DefaultCapacity.MaxWeightKg != 1200 {
			t.Fatalf("unexpected types %+v", types)
		}

		ts.do(http.MethodGet, "/location-types/Hovercraft", nil).
			assertProblem(t, http.StatusNotFound, "location-type-not-found")
	})
}

func TestPlacementRulesEndpoints(t *testing.T) {
	seed := func(t *testing.T) *testServer {
		t.Helper()
		ts := newTestServer(t)
		ts.seedLocationType("PalletRack", 1200, 2.4)
		return ts
	}

	t.Run("POST /placement-rules defines a rule", func(t *testing.T) {
		ts := seed(t)
		res := ts.do(http.MethodPost, "/placement-rules", map[string]any{
			"ruleId": "RULE-HAZ-ONLY-RACK", "locationType": "PalletRack", "effect": "Allow",
			"zone": map[string]any{"zoneCode": "HAZ"},
		}).assertStatus(t, http.StatusCreated)

		if got := res.headers.Get("Location"); got != "/placement-rules/RULE-HAZ-ONLY-RACK" {
			t.Fatalf("unexpected Location %q", got)
		}
		var rule struct {
			RuleID      string `json:"ruleId"`
			Effect      string `json:"effect"`
			Description string `json:"description"`
			Zone        struct {
				ZoneCode string `json:"zoneCode"`
			} `json:"zone"`
		}
		res.decode(t, &rule)
		if rule.RuleID != "RULE-HAZ-ONLY-RACK" || rule.Effect != "Allow" || rule.Zone.ZoneCode != "HAZ" {
			t.Fatalf("unexpected rule body %+v", rule)
		}
		if rule.Description == "" {
			t.Fatal("expected a human-readable rule description")
		}
		ts.do(http.MethodGet, res.headers.Get("Location"), nil).assertStatus(t, http.StatusOK)
	})

	t.Run("rejects an unknown effect with 422", func(t *testing.T) {
		seed(t).do(http.MethodPost, "/placement-rules", map[string]any{
			"ruleId": "RULE-1", "locationType": "PalletRack", "effect": "Maybe",
			"zone": map[string]any{"zoneCode": "HAZ"},
		}).assertProblem(t, http.StatusUnprocessableEntity, "unknown-placement-effect")
	})

	t.Run("rejects a predicate that constrains nothing with 422", func(t *testing.T) {
		seed(t).do(http.MethodPost, "/placement-rules", map[string]any{
			"ruleId": "RULE-1", "locationType": "PalletRack", "effect": "Allow", "zone": map[string]any{},
		}).assertProblem(t, http.StatusUnprocessableEntity, "empty-zone-predicate")
	})

	t.Run("rejects an unknown location type with 404", func(t *testing.T) {
		newTestServer(t).do(http.MethodPost, "/placement-rules", map[string]any{
			"ruleId": "RULE-1", "locationType": "Hovercraft", "effect": "Allow",
			"zone": map[string]any{"zoneCode": "HAZ"},
		}).assertProblem(t, http.StatusNotFound, "location-type-not-found")
	})

	t.Run("rejects a duplicate rule id with 409", func(t *testing.T) {
		ts := seed(t)
		body := map[string]any{
			"ruleId": "RULE-1", "locationType": "PalletRack", "effect": "Allow",
			"zone": map[string]any{"zoneCode": "HAZ"},
		}
		ts.do(http.MethodPost, "/placement-rules", body).assertStatus(t, http.StatusCreated)
		ts.do(http.MethodPost, "/placement-rules", body).assertProblem(t, http.StatusConflict, "duplicate-placement-rule")
	})

	t.Run("GET /placement-rules lists rules and 404s on an unknown one", func(t *testing.T) {
		ts := seed(t)
		ts.do(http.MethodPost, "/placement-rules", map[string]any{
			"ruleId": "RULE-1", "locationType": "PalletRack", "effect": "Deny",
			"zone": map[string]any{"temperatureClass": "Frozen"},
		}).assertStatus(t, http.StatusCreated)

		res := ts.do(http.MethodGet, "/placement-rules", nil).assertStatus(t, http.StatusOK)
		var rules []struct {
			RuleID string `json:"ruleId"`
			Zone   struct {
				TemperatureClass string `json:"temperatureClass"`
			} `json:"zone"`
		}
		res.decode(t, &rules)
		if len(rules) != 1 || rules[0].Zone.TemperatureClass != "Frozen" {
			t.Fatalf("unexpected rules %+v", rules)
		}

		ts.do(http.MethodGet, "/placement-rules/RULE-NOPE", nil).
			assertProblem(t, http.StatusNotFound, "placement-rule-not-found")
	})
}
