package http_test

import (
	"net/http"
	"testing"
)

// Known-valid GS1 GLNs (see internal/domain/shared/gln_test.go).
const (
	httpGLNSite  = "4006381333931"
	httpGLNZone  = "5412345000013"
	httpGLNSlot  = "4012345000009"
	httpGLNDock  = "9780201379624"
	httpBadCheck = "4006381333932"

	httpYardSpot = "WH1-YARD-NORTH-Y01-01-01-A"
	httpDockDoor = "WH1-YARD-NORTH-Y01-01-01-B"
)

// seedYard registers a yard zone with a Yard and a Dock location type.
func (ts *testServer) seedYard() {
	ts.t.Helper()
	ts.seedSite()
	ts.seedZone("YARD", "NORTH", "Ambient", false)
	ts.seedAisle("WH1-YARD-NORTH", "Y01", 1, "TwoWay")
	for name, role := range map[string]string{"YardSpot": "Yard", "DockDoor": "Dock"} {
		ts.do(http.MethodPost, "/location-types", map[string]any{"name": name, "role": role}).assertStatus(ts.t, http.StatusCreated)
	}
}

func field(t *testing.T, r response, name string) (any, bool) {
	t.Helper()
	var raw map[string]any
	r.decode(t, &raw)
	v, ok := raw[name]
	return v, ok
}

func TestSiteGLN(t *testing.T) {
	t.Run("valid GLN is accepted, echoed and returned by every read", func(t *testing.T) {
		ts := newTestServer(t)
		created := ts.do(http.MethodPost, "/sites", map[string]any{"siteCode": "WH1", "name": "One", "gln": httpGLNSite}).assertStatus(t, http.StatusCreated)
		if v, _ := field(t, created, "gln"); v != httpGLNSite {
			t.Fatalf("POST gln = %v", v)
		}
		got := ts.do(http.MethodGet, "/sites/WH1", nil).assertStatus(t, http.StatusOK)
		if v, _ := field(t, got, "gln"); v != httpGLNSite {
			t.Fatalf("GET gln = %v", v)
		}
		var page struct {
			Items []map[string]any `json:"items"`
		}
		ts.do(http.MethodGet, "/sites", nil).assertStatus(t, http.StatusOK).decode(t, &page)
		if len(page.Items) != 1 || page.Items[0]["gln"] != httpGLNSite {
			t.Fatalf("list = %v", page.Items)
		}
	})
	t.Run("a site without a GLN omits the field (old payload still works)", func(t *testing.T) {
		ts := newTestServer(t)
		created := ts.do(http.MethodPost, "/sites", map[string]any{"siteCode": "WH1", "name": "One"}).assertStatus(t, http.StatusCreated)
		if v, present := field(t, created, "gln"); present {
			t.Fatalf("gln must be omitted, got %v", v)
		}
	})
	t.Run("a wrong check digit is 422 invalid-gln", func(t *testing.T) {
		ts := newTestServer(t)
		ts.do(http.MethodPost, "/sites", map[string]any{"siteCode": "WH1", "name": "One", "gln": httpBadCheck}).assertProblem(t, http.StatusUnprocessableEntity, "invalid-gln")
		ts.do(http.MethodPost, "/sites", map[string]any{"siteCode": "WH1", "name": "One", "gln": "12345"}).assertProblem(t, http.StatusUnprocessableEntity, "invalid-gln")
	})
	t.Run("the same GLN on another site is 409 gln-already-assigned", func(t *testing.T) {
		ts := newTestServer(t)
		ts.do(http.MethodPost, "/sites", map[string]any{"siteCode": "WH1", "name": "One", "gln": httpGLNSite}).assertStatus(t, http.StatusCreated)
		ts.do(http.MethodPost, "/sites", map[string]any{"siteCode": "WH2", "name": "Two", "gln": httpGLNSite}).assertProblem(t, http.StatusConflict, "gln-already-assigned")
	})
}

func TestZoneGLN(t *testing.T) {
	t.Run("valid GLN is accepted and returned by POST, GET and the layout", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		created := ts.do(http.MethodPost, "/sites/WH1/zones", map[string]any{"areaCode": "YARD", "zoneCode": "NORTH", "temperatureClass": "Ambient", "gln": httpGLNZone}).assertStatus(t, http.StatusCreated)
		if v, _ := field(t, created, "gln"); v != httpGLNZone {
			t.Fatalf("POST gln = %v", v)
		}
		got := ts.do(http.MethodGet, "/zones/WH1-YARD-NORTH", nil).assertStatus(t, http.StatusOK)
		if v, _ := field(t, got, "gln"); v != httpGLNZone {
			t.Fatalf("GET gln = %v", v)
		}
		layout := ts.do(http.MethodGet, "/sites/WH1/layout", nil).assertStatus(t, http.StatusOK)
		var body struct {
			Zones []map[string]any `json:"zones"`
		}
		layout.decode(t, &body)
		if len(body.Zones) != 1 || body.Zones[0]["gln"] != httpGLNZone {
			t.Fatalf("layout zones = %+v", body.Zones)
		}
	})
	t.Run("a zone without a GLN omits the field", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		created := ts.do(http.MethodPost, "/sites/WH1/zones", map[string]any{"areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "Ambient"}).assertStatus(t, http.StatusCreated)
		if _, present := field(t, created, "gln"); present {
			t.Fatal("gln must be omitted")
		}
	})
	t.Run("invalid GLN is 422 and a site's GLN cannot be reused", func(t *testing.T) {
		ts := newTestServer(t)
		ts.do(http.MethodPost, "/sites", map[string]any{"siteCode": "WH1", "name": "One", "gln": httpGLNSite}).assertStatus(t, http.StatusCreated)
		zone := map[string]any{"areaCode": "YARD", "zoneCode": "NORTH", "temperatureClass": "Ambient"}
		zone["gln"] = httpBadCheck
		ts.do(http.MethodPost, "/sites/WH1/zones", zone).assertProblem(t, http.StatusUnprocessableEntity, "invalid-gln")
		zone["gln"] = httpGLNSite
		ts.do(http.MethodPost, "/sites/WH1/zones", zone).assertProblem(t, http.StatusConflict, "gln-already-assigned")
	})
}

func TestSlotGLNAndSpotKind(t *testing.T) {
	t.Run("a yard spot takes a GLN and a spotKind and every read returns them", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedYard()
		created := ts.do(http.MethodPost, "/locations", map[string]any{
			"locationCode": httpYardSpot, "locationType": "YardSpot", "gln": httpGLNSlot, "spotKind": "REEFER_PLUG",
		}).assertStatus(t, http.StatusCreated)
		for _, r := range []response{created, ts.do(http.MethodGet, "/locations/"+httpYardSpot, nil).assertStatus(t, http.StatusOK)} {
			if v, _ := field(t, r, "gln"); v != httpGLNSlot {
				t.Fatalf("gln = %v", v)
			}
			if v, _ := field(t, r, "spotKind"); v != "REEFER_PLUG" {
				t.Fatalf("spotKind = %v", v)
			}
		}
		var page struct {
			Items []map[string]any `json:"items"`
		}
		ts.do(http.MethodGet, "/sites/WH1/locations?role=Yard", nil).assertStatus(t, http.StatusOK).decode(t, &page)
		if len(page.Items) != 1 || page.Items[0]["spotKind"] != "REEFER_PLUG" || page.Items[0]["gln"] != httpGLNSlot {
			t.Fatalf("role listing = %v", page.Items)
		}
	})
	t.Run("a dock door takes a GLN", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedYard()
		created := ts.do(http.MethodPost, "/locations", map[string]any{
			"locationCode": httpDockDoor, "locationType": "DockDoor", "dockFlow": "Inbound", "gln": httpGLNDock,
		}).assertStatus(t, http.StatusCreated)
		if v, _ := field(t, created, "gln"); v != httpGLNDock {
			t.Fatalf("gln = %v", v)
		}
		if _, present := field(t, created, "spotKind"); present {
			t.Fatal("spotKind must be omitted on a dock door")
		}
	})
}

func TestSlotSpotKindRefusals(t *testing.T) {
	t.Run("spotKind on a dock door is 422 spot-kind-not-allowed", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedYard()
		ts.do(http.MethodPost, "/locations", map[string]any{
			"locationCode": httpDockDoor, "locationType": "DockDoor", "dockFlow": "Inbound", "spotKind": "PARKING",
		}).assertProblem(t, http.StatusUnprocessableEntity, "spot-kind-not-allowed")
	})
	t.Run("spotKind on a storage bin is 422 spot-kind-not-allowed", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.do(http.MethodPost, "/locations", map[string]any{
			"locationCode": "WH1-STOR-AMB-A07-03-02-B", "locationType": "PalletRack", "spotKind": "HOLDING",
		}).assertProblem(t, http.StatusUnprocessableEntity, "spot-kind-not-allowed")
	})
	t.Run("an unknown spotKind is 422 unknown-spot-kind", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedYard()
		for _, bad := range []string{"parking", "DOCK", "TRUCK_BAY"} {
			ts.do(http.MethodPost, "/locations", map[string]any{"locationCode": httpYardSpot, "locationType": "YardSpot", "spotKind": bad}).assertProblem(t, http.StatusUnprocessableEntity, "unknown-spot-kind")
		}
	})
	t.Run("every spot kind of the enum is accepted on a yard spot", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedYard()
		for i, kind := range []string{"PARKING", "HOLDING", "REEFER_PLUG", "HAZMAT", "STAGING_LANE"} {
			code := "WH1-YARD-NORTH-Y01-01-0" + string(rune('1'+i)) + "-A"
			ts.do(http.MethodPost, "/locations", map[string]any{"locationCode": code, "locationType": "YardSpot", "spotKind": kind}).assertStatus(t, http.StatusCreated)
		}
	})
}

func TestSlotGLNRefusalsAndOldPayload(t *testing.T) {
	t.Run("invalid and duplicate GLNs are refused", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedYard()
		ts.do(http.MethodPost, "/locations", map[string]any{"locationCode": httpYardSpot, "locationType": "YardSpot", "gln": httpBadCheck}).assertProblem(t, http.StatusUnprocessableEntity, "invalid-gln")
		ts.do(http.MethodPost, "/locations", map[string]any{"locationCode": httpYardSpot, "locationType": "YardSpot", "gln": httpGLNSlot}).assertStatus(t, http.StatusCreated)
		ts.do(http.MethodPost, "/locations", map[string]any{"locationCode": "WH1-YARD-NORTH-Y01-01-02-A", "locationType": "YardSpot", "gln": httpGLNSlot}).assertProblem(t, http.StatusConflict, "gln-already-assigned")
	})
	t.Run("an old payload without either field still works and omits both", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		created := ts.do(http.MethodPost, "/locations", map[string]any{"locationCode": "WH1-STOR-AMB-A07-03-02-B", "locationType": "PalletRack"}).assertStatus(t, http.StatusCreated)
		for _, name := range []string{"gln", "spotKind"} {
			if _, present := field(t, created, name); present {
				t.Fatalf("%s must be omitted", name)
			}
		}
	})
}
