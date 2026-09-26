// Package main_test hosts the godog (Cucumber for Go) acceptance suite. It
// drives the REAL chi router over HTTP — the same wiring the service uses in
// production, but with the in-memory outbound adapters — so every scenario in
// features/*.feature is a true black-box test of the REST API.
package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	inboundhttp "github.com/claudioed/facility-layout/internal/adapters/inbound/http"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/application/usecases"
)

// TestFeatures runs every Gherkin feature under features/ against a freshly
// wired HTTP server.
func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features"},
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

// world is the per-scenario state: a running server over fresh in-memory
// repositories, plus whatever the last HTTP call returned.
type world struct {
	server    *httptest.Server
	aisles    *memory.AisleRepo
	publisher *events.BufferedPublisher

	status  int
	body    []byte
	headers http.Header
}

// start builds the composition root the way cmd/facility does, but with the
// memory adapters and a fixed clock, and exposes it over a real TCP listener.
func (w *world) start() {
	sites := memory.NewSiteRepo()
	zones := memory.NewZoneRepo()
	aisles := memory.NewAisleRepo()
	slots := memory.NewSlotRepo()
	locationTypes := memory.NewLocationTypeRepo()
	rules := memory.NewPlacementRuleRepo()
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
		GetSiteLayout: &usecases.GetSiteLayout{Sites: sites, Zones: zones, Aisles: aisles, Slots: slots},
		GetZoneGrid:   &usecases.GetZoneGrid{Zones: zones, Aisles: aisles, Slots: slots},
	}

	w.server = httptest.NewServer(inboundhttp.NewRouter(s, slog.New(slog.NewTextHandler(io.Discard, nil))))
	w.aisles = aisles
	w.publisher = publisher
	w.status = 0
	w.body = nil
	w.headers = nil
}

func (w *world) stop() {
	if w.server != nil {
		w.server.Close()
		w.server = nil
	}
}

// call issues a real net/http request against the test server and returns the
// status, body and headers without touching the recorded "last response".
func (w *world) call(ctx context.Context, method, path string, payload any) (int, []byte, http.Header, error) {
	var reader io.Reader = http.NoBody
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, nil, err
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, w.server.URL+path, reader)
	if err != nil {
		return 0, nil, nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := w.server.Client().Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, err
	}
	return resp.StatusCode, body, resp.Header, nil
}

// record issues a request and remembers it as the response the Then steps
// assert against.
func (w *world) record(ctx context.Context, method, path string, payload any) error {
	status, body, headers, err := w.call(ctx, method, path, payload)
	if err != nil {
		return err
	}
	w.status, w.body, w.headers = status, body, headers
	return nil
}

// seed issues a Given-step request out of band and fails loudly unless it
// produced the expected status, so a broken fixture never masquerades as a
// failing assertion later.
func (w *world) seed(ctx context.Context, method, path string, payload any, want int) error {
	status, body, _, err := w.call(ctx, method, path, payload)
	if err != nil {
		return err
	}
	if status != want {
		return fmt.Errorf("seeding %s %s: expected %d, got %d: %s", method, path, want, status, string(body))
	}
	return nil
}

func (w *world) decode(dest any) error {
	if err := json.Unmarshal(w.body, dest); err != nil {
		return fmt.Errorf("response body is not valid JSON (%w): %s", err, string(w.body))
	}
	return nil
}

// ---------------------------------------------------------------- Given ----

func (w *world) anEmptyWarehouseMap() error {
	if published := len(w.publisher.Events()); published != 0 {
		return fmt.Errorf("expected a clean warehouse map, but %d domain events were already published", published)
	}
	return nil
}

func (w *world) aLocationType(ctx context.Context, name string, weight, volume float64) error {
	return w.seed(ctx, http.MethodPost, "/location-types", map[string]any{
		"name": name, "defaultCapacity": map[string]any{"maxWeightKg": weight, "maxVolumeM3": volume},
	}, http.StatusCreated)
}

func (w *world) aRegisteredSite(ctx context.Context, siteCode string) error {
	return w.seed(ctx, http.MethodPost, "/sites", map[string]any{
		"siteCode": siteCode, "name": "Fulfilment Centre " + siteCode,
	}, http.StatusCreated)
}

func (w *world) aRegisteredZone(ctx context.Context, areaCode, zoneCode, siteCode, temperatureClass string) error {
	return w.seed(ctx, http.MethodPost, "/sites/"+siteCode+"/zones", map[string]any{
		"areaCode": areaCode, "zoneCode": zoneCode, "temperatureClass": temperatureClass, "hazmat": zoneCode == "HAZ",
	}, http.StatusCreated)
}

func (w *world) aRegisteredAisle(ctx context.Context, aisleCode, zoneID string, sequenceHint int) error {
	return w.seed(ctx, http.MethodPost, "/zones/"+zoneID+"/aisles", map[string]any{
		"aisleCode": aisleCode, "sequenceHint": sequenceHint, "direction": "TwoWay",
	}, http.StatusCreated)
}

func (w *world) aRegisteredSlot(ctx context.Context, locationCode, locationType string) error {
	return w.seed(ctx, http.MethodPost, "/locations", map[string]any{
		"locationCode": locationCode, "locationType": locationType,
	}, http.StatusCreated)
}

func (w *world) aDenyRuleByTemperature(ctx context.Context, ruleID, locationType, temperatureClass string) error {
	return w.seed(ctx, http.MethodPost, "/placement-rules", map[string]any{
		"ruleId": ruleID, "locationType": locationType, "effect": "Deny",
		"zone": map[string]any{"temperatureClass": temperatureClass},
	}, http.StatusCreated)
}

func (w *world) anAllowRuleByZoneCode(ctx context.Context, ruleID, locationType, zoneCode string) error {
	return w.seed(ctx, http.MethodPost, "/placement-rules", map[string]any{
		"ruleId": ruleID, "locationType": locationType, "effect": "Allow",
		"zone": map[string]any{"zoneCode": zoneCode},
	}, http.StatusCreated)
}

// theAisleHasBeenDecommissioned retires an aisle directly through the
// repository: v1 exposes no HTTP-facing aisle-decommission command, but the
// "no slot under a non-Active parent" invariant still has to be provable
// end-to-end.
func (w *world) theAisleHasBeenDecommissioned(ctx context.Context, aisleID string) error {
	a, err := w.aisles.FindByID(ctx, aisleID)
	if err != nil {
		return err
	}
	if a == nil {
		return fmt.Errorf("aisle %q does not exist", aisleID)
	}
	if err := a.Decommission(); err != nil {
		return err
	}
	return w.aisles.Save(ctx, a)
}

// ----------------------------------------------------------------- When ----

func (w *world) iRegisterTheSite(ctx context.Context, siteCode, name string) error {
	return w.record(ctx, http.MethodPost, "/sites", map[string]any{"siteCode": siteCode, "name": name})
}

func (w *world) iRegisterTheZone(ctx context.Context, areaCode, zoneCode, siteCode, temperatureClass string) error {
	return w.record(ctx, http.MethodPost, "/sites/"+siteCode+"/zones", map[string]any{
		"areaCode": areaCode, "zoneCode": zoneCode, "temperatureClass": temperatureClass, "hazmat": zoneCode == "HAZ",
	})
}

func (w *world) iRegisterTheAisle(ctx context.Context, aisleCode, zoneID string, sequenceHint int, direction string) error {
	return w.record(ctx, http.MethodPost, "/zones/"+zoneID+"/aisles", map[string]any{
		"aisleCode": aisleCode, "sequenceHint": sequenceHint, "direction": direction,
	})
}

func (w *world) iRegisterTheSlot(ctx context.Context, locationCode, locationType string) error {
	return w.record(ctx, http.MethodPost, "/locations", map[string]any{
		"locationCode": locationCode, "locationType": locationType,
	})
}

func (w *world) iDecommissionTheSlot(ctx context.Context, locationCode string) error {
	return w.record(ctx, http.MethodPost, "/locations/"+locationCode+"/decommission", nil)
}

// iImportTheRows turns the Gherkin data table into the JSON array the import
// endpoint takes, so the feature file reads like the CSV a warehouse would
// actually export.
func (w *world) iImportTheRows(ctx context.Context, table *godog.Table) error {
	if len(table.Rows) < 2 {
		return fmt.Errorf("the import table needs a header row and at least one data row")
	}

	headers := make([]string, 0, len(table.Rows[0].Cells))
	for _, cell := range table.Rows[0].Cells {
		headers = append(headers, cell.Value)
	}

	rows := make([]map[string]any, 0, len(table.Rows)-1)
	for _, row := range table.Rows[1:] {
		payload := map[string]any{}
		for i, cell := range row.Cells {
			key := headers[i]
			if key == "sequenceHint" {
				hint, err := strconv.Atoi(cell.Value)
				if err != nil {
					return err
				}
				payload[key] = hint
				continue
			}
			payload[key] = cell.Value
		}
		payload["direction"] = "TwoWay"
		rows = append(rows, payload)
	}

	return w.record(ctx, http.MethodPost, "/locations/import", rows)
}

func (w *world) iRequestTheLayoutOfSite(ctx context.Context, siteCode string) error {
	return w.record(ctx, http.MethodGet, "/sites/"+siteCode+"/layout", nil)
}

func (w *world) iRequestTheLayoutOfSiteAsSVG(ctx context.Context, siteCode string) error {
	return w.record(ctx, http.MethodGet, "/sites/"+siteCode+"/layout?format=svg", nil)
}

func (w *world) iRequestTheGridOfZone(ctx context.Context, zoneID string) error {
	return w.record(ctx, http.MethodGet, "/zones/"+zoneID+"/grid", nil)
}

func (w *world) iRequestTheSlot(ctx context.Context, locationCode string) error {
	return w.record(ctx, http.MethodGet, "/locations/"+locationCode, nil)
}

func (w *world) iRequestTheClassificationOf(ctx context.Context, locationCode string) error {
	return w.record(ctx, http.MethodGet, "/locations/"+locationCode+"/classification", nil)
}

func (w *world) iListTheSites(ctx context.Context) error {
	return w.record(ctx, http.MethodGet, "/sites", nil)
}

func (w *world) iListTheZonesOfSite(ctx context.Context, siteCode string) error {
	return w.record(ctx, http.MethodGet, "/sites/"+siteCode+"/zones", nil)
}

func (w *world) iListTheAislesOfZone(ctx context.Context, zoneID string) error {
	return w.record(ctx, http.MethodGet, "/zones/"+zoneID+"/aisles", nil)
}

func (w *world) iListTheLocationTypes(ctx context.Context) error {
	return w.record(ctx, http.MethodGet, "/location-types", nil)
}

func (w *world) iListThePlacementRules(ctx context.Context) error {
	return w.record(ctx, http.MethodGet, "/placement-rules", nil)
}

func (w *world) iDefineTheDenyRuleByTemperature(ctx context.Context, ruleID, locationType, temperatureClass string) error {
	return w.record(ctx, http.MethodPost, "/placement-rules", map[string]any{
		"ruleId": ruleID, "locationType": locationType, "effect": "Deny",
		"zone": map[string]any{"temperatureClass": temperatureClass},
	})
}

func (w *world) iDefineTheDenyRuleWithEmptyPredicate(ctx context.Context, ruleID, locationType string) error {
	return w.record(ctx, http.MethodPost, "/placement-rules", map[string]any{
		"ruleId": ruleID, "locationType": locationType, "effect": "Deny",
		"zone": map[string]any{},
	})
}

// ----------------------------------------------------------------- Then ----

func (w *world) theResponseStatusIs(expected int) error {
	if w.status != expected {
		return fmt.Errorf("expected status %d, got %d: %s", expected, w.status, string(w.body))
	}
	return nil
}

func (w *world) theResponseHasALocationHeader(expected string) error {
	if got := w.headers.Get("Location"); got != expected {
		return fmt.Errorf("expected Location %q, got %q", expected, got)
	}
	return nil
}

func (w *world) theResponseContentTypeIs(expected string) error {
	if got := w.headers.Get("Content-Type"); got != expected {
		return fmt.Errorf("expected Content-Type %q, got %q", expected, got)
	}
	return nil
}

// theProblemDetailTypeIs asserts the RFC 7807 body: correct content type, and
// a "type" URI whose last segment identifies the error category.
func (w *world) theProblemDetailTypeIs(slug string) error {
	if ct := w.headers.Get("Content-Type"); ct != "application/problem+json" {
		return fmt.Errorf("expected Content-Type application/problem+json, got %q", ct)
	}
	var problem struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Status int    `json:"status"`
	}
	if err := w.decode(&problem); err != nil {
		return err
	}
	if got := problem.Type[strings.LastIndex(problem.Type, "/")+1:]; got != slug {
		return fmt.Errorf("expected problem type %q, got %q (from %q)", slug, got, problem.Type)
	}
	if problem.Status != w.status {
		return fmt.Errorf("problem body status %d does not match HTTP status %d", problem.Status, w.status)
	}
	return nil
}

func (w *world) theProblemDetailNamesTheRule(ruleID string) error {
	var problem struct {
		Detail string `json:"detail"`
	}
	if err := w.decode(&problem); err != nil {
		return err
	}
	if !strings.Contains(problem.Detail, ruleID) {
		return fmt.Errorf("expected the problem detail to name rule %q, got %q", ruleID, problem.Detail)
	}
	return nil
}

func (w *world) theSlotResponseReports(locationType, status string) error {
	var slot struct {
		LocationType string `json:"locationType"`
		Status       string `json:"status"`
	}
	if err := w.decode(&slot); err != nil {
		return err
	}
	if slot.LocationType != locationType || slot.Status != status {
		return fmt.Errorf("expected a %s slot with status %s, got %s/%s", locationType, status, slot.LocationType, slot.Status)
	}
	return nil
}

func (w *world) theDomainEventWasPublished(name string) error {
	published := make([]string, 0, len(w.publisher.Events()))
	for _, e := range w.publisher.Events() {
		if e.EventName() == name {
			return nil
		}
		published = append(published, e.EventName())
	}
	return fmt.Errorf("expected domain event %q to be published, got %v", name, published)
}

// resourceExists queries a resource out of band, so using it as an assertion
// never clobbers the response the surrounding steps assert against.
func (w *world) resourceExists(ctx context.Context, path string) error {
	status, body, _, err := w.call(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("expected %s to exist, got %d: %s", path, status, string(body))
	}
	return nil
}

func (w *world) theSiteExists(ctx context.Context, siteCode string) error {
	return w.resourceExists(ctx, "/sites/"+siteCode)
}

func (w *world) theZoneExists(ctx context.Context, zoneID string) error {
	return w.resourceExists(ctx, "/zones/"+zoneID)
}

func (w *world) theSlotExists(ctx context.Context, locationCode string) error {
	return w.resourceExists(ctx, "/locations/"+locationCode)
}

func (w *world) noSlotExists(ctx context.Context, locationCode string) error {
	status, _, _, err := w.call(ctx, http.MethodGet, "/locations/"+locationCode, nil)
	if err != nil {
		return err
	}
	if status != http.StatusNotFound {
		return fmt.Errorf("expected %q not to exist, got status %d", locationCode, status)
	}
	return nil
}

// ------------------------------------------------------------ import Then --

type importReport struct {
	RowsSubmitted int `json:"rowsSubmitted"`
	SlotsImported int `json:"slotsImported"`
	RowsRejected  int `json:"rowsRejected"`
	Results       []struct {
		Index        int    `json:"index"`
		LocationCode string `json:"locationCode"`
		Succeeded    bool   `json:"succeeded"`
		Error        string `json:"error"`
	} `json:"results"`
}

func (w *world) theImportReportSays(submitted, imported, rejected int) error {
	var report importReport
	if err := w.decode(&report); err != nil {
		return err
	}
	if report.RowsSubmitted != submitted || report.SlotsImported != imported || report.RowsRejected != rejected {
		return fmt.Errorf("expected %d/%d/%d submitted/imported/rejected, got %d/%d/%d",
			submitted, imported, rejected, report.RowsSubmitted, report.SlotsImported, report.RowsRejected)
	}
	if len(report.Results) != submitted {
		return fmt.Errorf("expected a per-row result for each of the %d rows, got %d", submitted, len(report.Results))
	}
	return nil
}

func (w *world) importRowWasRejectedMentioning(index int, fragment string) error {
	var report importReport
	if err := w.decode(&report); err != nil {
		return err
	}
	if index >= len(report.Results) {
		return fmt.Errorf("no result for row %d", index)
	}
	result := report.Results[index]
	if result.Succeeded {
		return fmt.Errorf("expected row %d to be rejected, but it succeeded", index)
	}
	if !strings.Contains(result.Error, fragment) {
		return fmt.Errorf("expected row %d's error to mention %q, got %q", index, fragment, result.Error)
	}
	return nil
}

func (w *world) importRowSucceeded(index int) error {
	var report importReport
	if err := w.decode(&report); err != nil {
		return err
	}
	if index >= len(report.Results) {
		return fmt.Errorf("no result for row %d", index)
	}
	if !report.Results[index].Succeeded {
		return fmt.Errorf("expected row %d to succeed, got error %q", index, report.Results[index].Error)
	}
	return nil
}

// ------------------------------------------------------------ layout Then --

type layoutBody struct {
	Totals struct {
		Zones  int `json:"zones"`
		Aisles int `json:"aisles"`
		Slots  int `json:"slots"`
	} `json:"totals"`
	Zones []struct {
		ZoneID string `json:"zoneId"`
		Aisles []struct {
			AisleID   string `json:"aisleId"`
			AisleCode string `json:"aisleCode"`
			Slots     []struct {
				LocationCode string `json:"locationCode"`
			} `json:"slots"`
		} `json:"aisles"`
	} `json:"zones"`
}

func (w *world) theLayoutReports(zones, aisles, slots int) error {
	var layout layoutBody
	if err := w.decode(&layout); err != nil {
		return err
	}
	if layout.Totals.Zones != zones || layout.Totals.Aisles != aisles || layout.Totals.Slots != slots {
		return fmt.Errorf("expected %d zones / %d aisles / %d slots, got %d / %d / %d",
			zones, aisles, slots, layout.Totals.Zones, layout.Totals.Aisles, layout.Totals.Slots)
	}
	return nil
}

func (w *world) theLayoutZonesAreOrdered(expected string) error {
	var layout layoutBody
	if err := w.decode(&layout); err != nil {
		return err
	}
	got := make([]string, 0, len(layout.Zones))
	for _, z := range layout.Zones {
		got = append(got, z.ZoneID)
	}
	return expectOrder(expected, got, "zones")
}

func (w *world) theAislesOfLayoutZoneAreOrdered(zoneID, expected string) error {
	var layout layoutBody
	if err := w.decode(&layout); err != nil {
		return err
	}
	for _, z := range layout.Zones {
		if z.ZoneID != zoneID {
			continue
		}
		got := make([]string, 0, len(z.Aisles))
		for _, a := range z.Aisles {
			got = append(got, a.AisleCode)
		}
		return expectOrder(expected, got, "aisles of "+zoneID)
	}
	return fmt.Errorf("zone %q is not in the layout", zoneID)
}

func (w *world) theSlotsOfLayoutAisleAreOrdered(aisleID, expected string) error {
	var layout layoutBody
	if err := w.decode(&layout); err != nil {
		return err
	}
	for _, z := range layout.Zones {
		for _, a := range z.Aisles {
			if a.AisleID != aisleID {
				continue
			}
			got := make([]string, 0, len(a.Slots))
			for _, s := range a.Slots {
				got = append(got, s.LocationCode)
			}
			return expectOrder(expected, got, "slots of "+aisleID)
		}
	}
	return fmt.Errorf("aisle %q is not in the layout", aisleID)
}

// -------------------------------------------------------------- grid Then --

type gridBody struct {
	Columns []struct {
		AisleCode string `json:"aisleCode"`
		Bay       string `json:"bay"`
	} `json:"columns"`
	Levels []string `json:"levels"`
	Rows   []struct {
		Level string `json:"level"`
		Cells []*struct {
			Positions []struct {
				Position string `json:"position"`
			} `json:"positions"`
		} `json:"cells"`
	} `json:"rows"`
}

func (w *world) theGridColumnsAreOrdered(expected string) error {
	var grid gridBody
	if err := w.decode(&grid); err != nil {
		return err
	}
	got := make([]string, 0, len(grid.Columns))
	for _, c := range grid.Columns {
		got = append(got, c.AisleCode+"/"+c.Bay)
	}
	return expectOrder(expected, got, "grid columns")
}

func (w *world) theGridLevelsAre(expected string) error {
	var grid gridBody
	if err := w.decode(&grid); err != nil {
		return err
	}
	return expectOrder(expected, grid.Levels, "grid levels")
}

func (w *world) everyGridRowHasOneCellPerColumn() error {
	var grid gridBody
	if err := w.decode(&grid); err != nil {
		return err
	}
	for _, row := range grid.Rows {
		if len(row.Cells) != len(grid.Columns) {
			return fmt.Errorf("row %q has %d cells but the grid has %d columns", row.Level, len(row.Cells), len(grid.Columns))
		}
	}
	return nil
}

func (w *world) theGridCellHoldsPositions(level string, column int, expected string) error {
	cell, err := w.gridCell(level, column)
	if err != nil {
		return err
	}
	if cell == nil {
		return fmt.Errorf("cell at level %q column %d is a gap, expected positions %q", level, column, expected)
	}
	got := make([]string, 0, len(cell.Positions))
	for _, p := range cell.Positions {
		got = append(got, p.Position)
	}
	return expectOrder(expected, got, fmt.Sprintf("positions at level %s column %d", level, column))
}

func (w *world) theGridCellIsAGap(level string, column int) error {
	cell, err := w.gridCell(level, column)
	if err != nil {
		return err
	}
	if cell != nil {
		return fmt.Errorf("expected a null gap at level %q column %d, got %+v", level, column, cell.Positions)
	}
	return nil
}

func (w *world) gridCell(level string, column int) (*struct {
	Positions []struct {
		Position string `json:"position"`
	} `json:"positions"`
}, error) {
	var grid gridBody
	if err := w.decode(&grid); err != nil {
		return nil, err
	}
	for _, row := range grid.Rows {
		if row.Level != level {
			continue
		}
		if column >= len(row.Cells) {
			return nil, fmt.Errorf("row %q has no column %d", level, column)
		}
		return row.Cells[column], nil
	}
	return nil, fmt.Errorf("the grid has no row for level %q", level)
}

// --------------------------------------------------------------- svg Then --

func (w *world) theResponseIsAWellFormedSVGMentioning(fragment string) error {
	document := string(w.body)
	if !strings.HasPrefix(document, `<svg xmlns="http://www.w3.org/2000/svg"`) {
		return fmt.Errorf("expected an SVG root element, got %.80q", document)
	}
	decoder := xml.NewDecoder(strings.NewReader(document))
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("the SVG is not well-formed XML: %w", err)
		}
	}
	if !strings.Contains(document, fragment) {
		return fmt.Errorf("expected the SVG to mention %q", fragment)
	}
	return nil
}

// ------------------------------------------------------ catalog Then --

func (w *world) theListedSitesAre(expected string) error {
	var listed []struct {
		SiteCode string `json:"siteCode"`
	}
	if err := w.decode(&listed); err != nil {
		return err
	}
	got := make([]string, 0, len(listed))
	for _, s := range listed {
		got = append(got, s.SiteCode)
	}
	return expectOrder(expected, got, "listed sites")
}

func (w *world) theListedZonesAre(expected string) error {
	var listed []struct {
		ZoneID string `json:"zoneId"`
	}
	if err := w.decode(&listed); err != nil {
		return err
	}
	got := make([]string, 0, len(listed))
	for _, z := range listed {
		got = append(got, z.ZoneID)
	}
	return expectOrder(expected, got, "listed zones")
}

func (w *world) theListedAislesAre(expected string) error {
	var listed []struct {
		AisleCode string `json:"aisleCode"`
	}
	if err := w.decode(&listed); err != nil {
		return err
	}
	got := make([]string, 0, len(listed))
	for _, a := range listed {
		got = append(got, a.AisleCode)
	}
	return expectOrder(expected, got, "listed aisles")
}

func (w *world) theListedLocationTypesAre(expected string) error {
	var listed []struct {
		Name string `json:"name"`
	}
	if err := w.decode(&listed); err != nil {
		return err
	}
	got := make([]string, 0, len(listed))
	for _, t := range listed {
		got = append(got, t.Name)
	}
	return expectOrder(expected, got, "listed location types")
}

func (w *world) theListedPlacementRulesAre(expected string) error {
	var listed []struct {
		RuleID string `json:"ruleId"`
	}
	if err := w.decode(&listed); err != nil {
		return err
	}
	got := make([]string, 0, len(listed))
	for _, r := range listed {
		got = append(got, r.RuleID)
	}
	return expectOrder(expected, got, "listed placement rules")
}

func (w *world) theClassificationReports(hazmat, temperatureClass string) error {
	wantHazmat := hazmat == "true"
	var classification struct {
		Hazmat           bool   `json:"hazmat"`
		TemperatureClass string `json:"temperatureClass"`
	}
	if err := w.decode(&classification); err != nil {
		return err
	}
	if classification.Hazmat != wantHazmat || classification.TemperatureClass != temperatureClass {
		return fmt.Errorf("expected hazmat %t / temperature class %q, got %t / %q",
			wantHazmat, temperatureClass, classification.Hazmat, classification.TemperatureClass)
	}
	return nil
}

// ------------------------------------------------------------- helpers -----

func expectOrder(expected string, got []string, what string) error {
	want := strings.Split(expected, ",")
	if len(want) != len(got) {
		return fmt.Errorf("expected %d %s (%v), got %d (%v)", len(want), what, want, len(got), got)
	}
	for i := range want {
		if want[i] != got[i] {
			return fmt.Errorf("expected %s in order %v, got %v", what, want, got)
		}
	}
	return nil
}

// ------------------------------------------------------------- wiring ------

// InitializeScenario registers the step definitions and gives every scenario
// its own server and its own in-memory state.
func InitializeScenario(sc *godog.ScenarioContext) {
	w := &world{}

	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		w.start()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		w.stop()
		return ctx, nil
	})

	// Given
	sc.Step(`^an empty warehouse map$`, w.anEmptyWarehouseMap)
	sc.Step(`^a LocationType "([^"]*)" with capacity ([\d.]+) kg and ([\d.]+) m3$`, w.aLocationType)
	sc.Step(`^a registered Site "([^"]*)"$`, w.aRegisteredSite)
	sc.Step(`^a registered Zone "([^"]*)"/"([^"]*)" in Site "([^"]*)" with temperature class "([^"]*)"$`, w.aRegisteredZone)
	sc.Step(`^a registered Aisle "([^"]*)" in Zone "([^"]*)" with sequence hint (\d+)$`, w.aRegisteredAisle)
	sc.Step(`^a registered LocationSlot "([^"]*)" of type "([^"]*)"$`, w.aRegisteredSlot)
	sc.Step(`^a PlacementRule "([^"]*)" denying "([^"]*)" where temperature class is "([^"]*)"$`, w.aDenyRuleByTemperature)
	sc.Step(`^a PlacementRule "([^"]*)" allowing "([^"]*)" where zone code is "([^"]*)"$`, w.anAllowRuleByZoneCode)
	sc.Step(`^the Aisle "([^"]*)" has been decommissioned$`, w.theAisleHasBeenDecommissioned)

	// When
	sc.Step(`^I register the Site "([^"]*)" named "([^"]*)"$`, w.iRegisterTheSite)
	sc.Step(`^I register the Zone "([^"]*)"/"([^"]*)" in Site "([^"]*)" with temperature class "([^"]*)"$`, w.iRegisterTheZone)
	sc.Step(`^I register the Aisle "([^"]*)" in Zone "([^"]*)" with sequence hint (\d+) and direction "([^"]*)"$`, w.iRegisterTheAisle)
	sc.Step(`^I register the LocationSlot "([^"]*)" of type "([^"]*)"$`, w.iRegisterTheSlot)
	sc.Step(`^I decommission the LocationSlot "([^"]*)"$`, w.iDecommissionTheSlot)
	sc.Step(`^I import the following facility layout rows:$`, w.iImportTheRows)
	sc.Step(`^I request the layout of Site "([^"]*)"$`, w.iRequestTheLayoutOfSite)
	sc.Step(`^I request the layout of Site "([^"]*)" as SVG$`, w.iRequestTheLayoutOfSiteAsSVG)
	sc.Step(`^I request the grid of Zone "([^"]*)"$`, w.iRequestTheGridOfZone)
	sc.Step(`^I request the LocationSlot "([^"]*)"$`, w.iRequestTheSlot)
	sc.Step(`^I request the classification of LocationSlot "([^"]*)"$`, w.iRequestTheClassificationOf)
	sc.Step(`^I list the sites$`, w.iListTheSites)
	sc.Step(`^I list the zones of Site "([^"]*)"$`, w.iListTheZonesOfSite)
	sc.Step(`^I list the aisles of Zone "([^"]*)"$`, w.iListTheAislesOfZone)
	sc.Step(`^I list the location types$`, w.iListTheLocationTypes)
	sc.Step(`^I list the placement rules$`, w.iListThePlacementRules)
	sc.Step(`^I define the PlacementRule "([^"]*)" denying "([^"]*)" where temperature class is "([^"]*)"$`, w.iDefineTheDenyRuleByTemperature)
	sc.Step(`^I define the PlacementRule "([^"]*)" denying "([^"]*)" with an empty zone predicate$`, w.iDefineTheDenyRuleWithEmptyPredicate)

	// Then
	sc.Step(`^the response status is (\d+)$`, w.theResponseStatusIs)
	sc.Step(`^the response has a Location header pointing at "([^"]*)"$`, w.theResponseHasALocationHeader)
	sc.Step(`^the response content type is "([^"]*)"$`, w.theResponseContentTypeIs)
	sc.Step(`^the problem detail type is "([^"]*)"$`, w.theProblemDetailTypeIs)
	sc.Step(`^the problem detail names the rule "([^"]*)"$`, w.theProblemDetailNamesTheRule)
	sc.Step(`^the LocationSlot response reports type "([^"]*)" with status "([^"]*)"$`, w.theSlotResponseReports)
	sc.Step(`^the domain event "([^"]*)" was published$`, w.theDomainEventWasPublished)
	sc.Step(`^the Site "([^"]*)" exists$`, w.theSiteExists)
	sc.Step(`^the Zone "([^"]*)" exists$`, w.theZoneExists)
	sc.Step(`^the LocationSlot "([^"]*)" exists$`, w.theSlotExists)
	sc.Step(`^no LocationSlot "([^"]*)" exists$`, w.noSlotExists)

	sc.Step(`^the import report says (\d+) submitted, (\d+) imported, (\d+) rejected$`, w.theImportReportSays)
	sc.Step(`^import row (\d+) was rejected mentioning "([^"]*)"$`, w.importRowWasRejectedMentioning)
	sc.Step(`^import row (\d+) succeeded$`, w.importRowSucceeded)

	sc.Step(`^the layout reports (\d+) zones, (\d+) aisles and (\d+) slots$`, w.theLayoutReports)
	sc.Step(`^the layout zones are ordered "([^"]*)"$`, w.theLayoutZonesAreOrdered)
	sc.Step(`^the aisles of layout zone "([^"]*)" are ordered "([^"]*)"$`, w.theAislesOfLayoutZoneAreOrdered)
	sc.Step(`^the slots of layout aisle "([^"]*)" are ordered "([^"]*)"$`, w.theSlotsOfLayoutAisleAreOrdered)

	sc.Step(`^the grid columns are ordered "([^"]*)"$`, w.theGridColumnsAreOrdered)
	sc.Step(`^the grid levels are "([^"]*)"$`, w.theGridLevelsAre)
	sc.Step(`^every grid row has one cell per column$`, w.everyGridRowHasOneCellPerColumn)
	sc.Step(`^the grid cell at level "([^"]*)" column (\d+) holds positions "([^"]*)"$`, w.theGridCellHoldsPositions)
	sc.Step(`^the grid cell at level "([^"]*)" column (\d+) is a gap$`, w.theGridCellIsAGap)

	sc.Step(`^the response is a well-formed SVG document mentioning "([^"]*)"$`, w.theResponseIsAWellFormedSVGMentioning)

	sc.Step(`^the listed sites are "([^"]*)"$`, w.theListedSitesAre)
	sc.Step(`^the listed zones are "([^"]*)"$`, w.theListedZonesAre)
	sc.Step(`^the listed aisles are "([^"]*)"$`, w.theListedAislesAre)
	sc.Step(`^the listed location types are "([^"]*)"$`, w.theListedLocationTypesAre)
	sc.Step(`^the listed placement rules are "([^"]*)"$`, w.theListedPlacementRulesAre)
	sc.Step(`^the classification reports hazmat (true|false) and temperature class "([^"]*)"$`, w.theClassificationReports)
}
