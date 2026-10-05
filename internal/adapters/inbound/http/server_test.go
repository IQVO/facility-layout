package http_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/facility-layout/internal/adapters/inbound/http"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/application/usecases"
)

// testServer is the real chi router over the in-memory adapters — the same
// wiring cmd/facility builds, minus Postgres.
type testServer struct {
	t       *testing.T
	handler http.Handler
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	return &testServer{t: t, handler: inboundhttp.NewRouter(newTestUseCases(), slog.New(slog.NewTextHandler(io.Discard, nil)))}
}

// newTestUseCases wires every use case over fresh in-memory adapters, so a
// test can build the router with its own RouterOptions (e.g. WithAuth).
func newTestUseCases() *inboundhttp.Server {
	sites := memory.NewSiteRepo()
	zones := memory.NewZoneRepo()
	aisles := memory.NewAisleRepo()
	slots := memory.NewSlotRepo()
	locationTypes := memory.NewLocationTypeRepo()
	rules := memory.NewPlacementRuleRepo()
	structures := memory.NewFixedStructureRepo()
	crossAisles := memory.NewCrossAisleRepo()
	publisher := events.NewBufferedPublisher()
	clock := memory.NewFixedClock(time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC))

	s := &inboundhttp.Server{
		RegisterSite:         &usecases.RegisterSite{Sites: sites, Events: publisher, Clock: clock},
		GetSite:              &usecases.GetSite{Sites: sites},
		ListSites:            &usecases.ListSites{Sites: sites},
		RegisterZone:         &usecases.RegisterZone{Sites: sites, Zones: zones, Events: publisher, Clock: clock},
		GetZone:              &usecases.GetZone{Zones: zones},
		ListZones:            &usecases.ListZones{Sites: sites, Zones: zones},
		RegisterAisle:        &usecases.RegisterAisle{Zones: zones, Aisles: aisles, Events: publisher, Clock: clock},
		GetAisle:             &usecases.GetAisle{Aisles: aisles},
		ListAisles:           &usecases.ListAisles{Zones: zones, Aisles: aisles},
		RegisterLocationType: &usecases.RegisterLocationType{LocationTypes: locationTypes, Events: publisher, Clock: clock},
		GetLocationType:      &usecases.GetLocationType{LocationTypes: locationTypes},
		ListLocationTypes:    &usecases.ListLocationTypes{LocationTypes: locationTypes},
		DefinePlacementRule:  &usecases.DefinePlacementRule{LocationTypes: locationTypes, Rules: rules, Events: publisher, Clock: clock},
		GetPlacementRule:     &usecases.GetPlacementRule{Rules: rules},
		ListPlacementRules:   &usecases.ListPlacementRules{Rules: rules},
		RegisterLocationSlot: &usecases.RegisterLocationSlot{
			Sites: sites, Zones: zones, Aisles: aisles, Slots: slots,
			LocationTypes: locationTypes, Rules: rules, Events: publisher, Clock: clock,
		},
		GetLocationSlot:           &usecases.GetLocationSlot{Slots: slots},
		GetLocationClassification: &usecases.GetLocationClassification{Slots: slots, Zones: zones},
		DecommissionLocationSlot:  &usecases.DecommissionLocationSlot{Slots: slots, Events: publisher, Clock: clock},
		ImportFacilityLayout: &usecases.ImportFacilityLayout{
			Sites: sites, Zones: zones, Aisles: aisles, Slots: slots,
			LocationTypes: locationTypes, Rules: rules, Events: publisher, Clock: clock,
		},
		GetSiteLayout: &usecases.GetSiteLayout{Sites: sites, Zones: zones, Aisles: aisles, Slots: slots, Structures: structures},
		GetZoneGrid:   &usecases.GetZoneGrid{Zones: zones, Aisles: aisles, Slots: slots},

		SetLocationGeometry:    &usecases.SetLocationGeometry{Slots: slots, Events: publisher, Clock: clock},
		SetAisleGeometry:       &usecases.SetAisleGeometry{Aisles: aisles, Events: publisher, Clock: clock},
		RegisterFixedStructure: &usecases.RegisterFixedStructure{Sites: sites, Structures: structures, Events: publisher, Clock: clock},
		ListFixedStructures:    &usecases.ListFixedStructures{Sites: sites, Structures: structures},

		RegisterCrossAisle: &usecases.RegisterCrossAisle{
			Zones: zones, Aisles: aisles, CrossAisles: crossAisles, Events: publisher, Clock: clock,
		},
		ListCrossAisles: &usecases.ListCrossAisles{
			Zones: zones, CrossAisles: crossAisles,
		},
		GetZoneTravelGraph: &usecases.GetZoneTravelGraph{
			Zones: zones, Aisles: aisles, Slots: slots, CrossAisles: crossAisles,
		},
		EstimateTravelDistance: &usecases.EstimateTravelDistance{
			Zones: zones, Aisles: aisles, Slots: slots, CrossAisles: crossAisles,
		},
	}

	return s
}

type response struct {
	status  int
	body    []byte
	headers http.Header
}

func (ts *testServer) do(method, path string, payload any) response {
	ts.t.Helper()

	var body io.Reader = http.NoBody
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			ts.t.Fatalf("unexpected error: %v", err)
		}
		body = bytes.NewReader(encoded)
	}

	req := httptest.NewRequest(method, path, body)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	ts.handler.ServeHTTP(rec, req)

	return response{status: rec.Code, body: rec.Body.Bytes(), headers: rec.Header()}
}

// doRaw posts a literal body, for the malformed-JSON path.
func (ts *testServer) doRaw(method, path, body string) response {
	ts.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ts.handler.ServeHTTP(rec, req)
	return response{status: rec.Code, body: rec.Body.Bytes(), headers: rec.Header()}
}

func (r response) decode(t *testing.T, dest any) {
	t.Helper()
	if err := json.Unmarshal(r.body, dest); err != nil {
		t.Fatalf("response is not valid JSON (%v): %s", err, string(r.body))
	}
}

func (r response) assertStatus(t *testing.T, want int) response {
	t.Helper()
	if r.status != want {
		t.Fatalf("expected status %d, got %d: %s", want, r.status, string(r.body))
	}
	return r
}

// assertProblem asserts the RFC 7807 contract: the right content type, a
// type URI whose final segment identifies the category, and a status field
// that agrees with the HTTP status.
func (r response) assertProblem(t *testing.T, wantStatus int, wantSlug string) {
	t.Helper()
	r.assertStatus(t, wantStatus)

	if ct := r.headers.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("expected Content-Type application/problem+json, got %q", ct)
	}
	var problem struct {
		Type     string `json:"type"`
		Title    string `json:"title"`
		Status   int    `json:"status"`
		Detail   string `json:"detail"`
		Instance string `json:"instance"`
	}
	r.decode(t, &problem)

	if got := problem.Type[strings.LastIndex(problem.Type, "/")+1:]; got != wantSlug {
		t.Fatalf("expected problem type %q, got %q (from %q)", wantSlug, got, problem.Type)
	}
	if problem.Status != wantStatus {
		t.Fatalf("problem body status %d does not match HTTP status %d", problem.Status, wantStatus)
	}
	if problem.Title == "" || problem.Detail == "" || problem.Instance == "" {
		t.Fatalf("RFC 7807 body is missing fields: %+v", problem)
	}
}

// ------------------------------------------------------------- fixtures ----

func (ts *testServer) seedSite() {
	ts.t.Helper()
	ts.do(http.MethodPost, "/sites", map[string]any{"siteCode": "WH1", "name": "Fulfilment Centre One"}).assertStatus(ts.t, http.StatusCreated)
}

func (ts *testServer) seedZone(areaCode, zoneCode, temperatureClass string, hazmat bool) {
	ts.t.Helper()
	ts.do(http.MethodPost, "/sites/WH1/zones", map[string]any{
		"areaCode": areaCode, "zoneCode": zoneCode, "temperatureClass": temperatureClass, "hazmat": hazmat,
	}).assertStatus(ts.t, http.StatusCreated)
}

func (ts *testServer) seedAisle(zoneID, aisleCode string, hint int, direction string) {
	ts.t.Helper()
	ts.do(http.MethodPost, "/zones/"+zoneID+"/aisles", map[string]any{
		"aisleCode": aisleCode, "sequenceHint": hint, "direction": direction,
	}).assertStatus(ts.t, http.StatusCreated)
}

func (ts *testServer) seedLocationType(name string, weight, volume float64) {
	ts.t.Helper()
	ts.do(http.MethodPost, "/location-types", map[string]any{
		"name": name, "defaultCapacity": map[string]any{"maxWeightKg": weight, "maxVolumeM3": volume},
	}).assertStatus(ts.t, http.StatusCreated)
}

func (ts *testServer) seedSlot(code, locationType string) {
	ts.t.Helper()
	ts.do(http.MethodPost, "/locations", map[string]any{
		"locationCode": code, "locationType": locationType,
	}).assertStatus(ts.t, http.StatusCreated)
}

// seedStorageAisle is the canonical chain used by most tests.
func (ts *testServer) seedStorageAisle() {
	ts.t.Helper()
	ts.seedSite()
	ts.seedZone("STOR", "AMB", "Ambient", false)
	ts.seedAisle("WH1-STOR-AMB", "A07", 7, "TwoWay")
	ts.seedLocationType("PalletRack", 1200, 2.4)
}
