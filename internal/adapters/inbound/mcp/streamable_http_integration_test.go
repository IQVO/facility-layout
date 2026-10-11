//go:build integration

// Integration tests for the MCP inbound adapter over the REAL Streamable
// HTTP transport: mcp.Handler(server) mounted on an httptest.Server, driven
// by the SDK's own client (mcp.NewClient + StreamableClientTransport), with
// the REAL Postgres-backed read use cases behind it — exactly the deployment
// shape cmd/mcp exposes. This proves the wire contract (initialize,
// tools/list, tools/call) end-to-end against real infrastructure, not the
// tool handlers in isolation over in-memory repos.
//
// facility-layout is a read-only Open Host Service: every registered tool is
// a read tool and there is no write tool to drive, so the state the tools
// read is seeded through the REAL write use cases over the same private
// database. Domain rejections and invalid input must still come back as
// tool errors (res.IsError), never transport errors.
//
// Postgres comes from testcontainers: one container per package run, one
// private database per test, migrated once. Never an external DATABASE_URL,
// never t.Skip.
package mcp_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	mcpadapter "github.com/claudioed/facility-layout/internal/adapters/inbound/mcp"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// One Postgres container serves the whole package, migrated once into a
// template database; each test gets a private clone (milliseconds). See
// internal/application/usecases/usecases_wiring_integration_test.go for the
// same pattern's rationale. Never an external DATABASE_URL, never t.Skip.
const mcpTemplateDB = "mcp_migrated_template"

var (
	mcpSharedBaseURL string
	mcpDBSeq         atomic.Uint64
)

func TestMain(m *testing.M) {
	os.Exit(runMCPTests(m))
}

func runMCPTests(m *testing.M) int {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("facility_mcp"),
		tcpostgres.WithUsername("facility"),
		tcpostgres.WithPassword("facility"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start postgres container: %v\n", err)
		return 1
	}
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			fmt.Fprintf(os.Stderr, "terminate postgres container: %v\n", err)
		}
	}()

	var err2 error
	mcpSharedBaseURL, err2 = container.ConnectionString(ctx, "sslmode=disable")
	if err2 != nil {
		fmt.Fprintf(os.Stderr, "postgres connection string: %v\n", err2)
		return 1
	}
	if err := mcpCreateDatabase(ctx, mcpTemplateDB); err != nil {
		fmt.Fprintf(os.Stderr, "create template database: %v\n", err)
		return 1
	}
	if err := postgres.RunMigrations(mcpWithDB(mcpSharedBaseURL, mcpTemplateDB), mcpMigrationsDir()); err != nil {
		fmt.Fprintf(os.Stderr, "migrate template: %v\n", err)
		return 1
	}
	return m.Run()
}

// mcpMigrationsDir resolves /migrations relative to this test file, so the
// harness works regardless of the working directory `go test` runs from.
func mcpMigrationsDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("unable to resolve test file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "migrations")
}

// mcpWithDB rewrites the path of a connection URL to the named database.
func mcpWithDB(baseURL, name string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		panic(err)
	}
	u.Path = "/" + name
	return u.String()
}

// mcpCreateDatabase creates an empty database inside the shared container.
func mcpCreateDatabase(ctx context.Context, name string) error {
	conn, err := pgx.Connect(ctx, mcpSharedBaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", name)); err != nil {
		return fmt.Errorf("create database %s: %w", name, err)
	}
	return nil
}

// mcpMigratedDB hands the test a connection URL to its own private database
// cloned from the migrated template.
func mcpMigratedDB(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("mcp_%d", mcpDBSeq.Add(1))
	conn, err := pgx.Connect(context.Background(), mcpSharedBaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), fmt.Sprintf(
		"CREATE DATABASE %q WITH TEMPLATE %q", name, mcpTemplateDB)); err != nil {
		t.Fatalf("clone database: %v", err)
	}
	return mcpWithDB(mcpSharedBaseURL, name)
}

// mcpIntegrationHarness wires the REAL production stack — Postgres repos,
// UnitOfWork, write use cases for seeding, read use cases, mcp.NewServer,
// mcp.Handler — seeds a small warehouse map through the real write path,
// and serves the read stack over Streamable HTTP with a connected SDK
// client session. Deps.Reports stays nil: the curated report tool is
// conditionally registered and outside the pinned default surface.
type mcpIntegrationHarness struct {
	session   *sdkmcp.ClientSession
	publisher *events.BufferedPublisher
}

// newMCPIntegrationHarness seeds one WH1 layout on a fresh private database
// and serves the real MCP stack over Streamable HTTP.
func newMCPIntegrationHarness(t *testing.T) *mcpIntegrationHarness {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, mcpMigratedDB(t))
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	publisher := events.NewBufferedPublisher()
	clock := memory.NewFixedClock(time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
	uow := postgres.NewUnitOfWork(pool)

	sites := postgres.NewSiteRepo(pool)
	zones := postgres.NewZoneRepo(pool)
	aisles := postgres.NewAisleRepo(pool)
	slots := postgres.NewSlotRepo(pool)
	locationTypes := postgres.NewLocationTypeRepo(pool)
	rules := postgres.NewPlacementRuleRepo(pool)
	crossAisles := postgres.NewCrossAisleRepo(pool)

	// Seed through the REAL write use cases, so the read models the tools
	// project are built from genuinely-registered aggregates in Postgres.
	registerSite := &usecases.RegisterSite{Sites: sites, Events: publisher, Clock: clock, UnitOfWork: uow}
	registerZone := &usecases.RegisterZone{Sites: sites, Zones: zones, Events: publisher, Clock: clock, UnitOfWork: uow}
	registerAisle := &usecases.RegisterAisle{Zones: zones, Aisles: aisles, Events: publisher, Clock: clock, UnitOfWork: uow}
	registerType := &usecases.RegisterLocationType{LocationTypes: locationTypes, Events: publisher, Clock: clock, UnitOfWork: uow}
	registerSlot := &usecases.RegisterLocationSlot{
		Sites: sites, Zones: zones, Aisles: aisles, Slots: slots,
		LocationTypes: locationTypes, Rules: rules, Events: publisher, Clock: clock,
		UnitOfWork: uow,
	}

	capacity := func(w, v float64) shared.Capacity {
		c, err := shared.NewCapacity(w, v)
		if err != nil {
			t.Fatalf("capacity: %v", err)
		}
		return c
	}
	code := func(raw string) shared.LocationCode {
		c, err := shared.ParseLocationCode(raw)
		if err != nil {
			t.Fatalf("parse code %q: %v", raw, err)
		}
		return c
	}

	if _, err := registerSite.Execute(ctx, "WH1", "Fulfilment Centre One"); err != nil {
		t.Fatalf("seed site: %v", err)
	}
	if _, err := registerType.Execute(ctx, placement.PalletRack, placement.Storage, capacity(1200, 2.4)); err != nil {
		t.Fatalf("seed pallet rack: %v", err)
	}
	if _, err := registerType.Execute(ctx, "DockDoor", placement.Dock, shared.Capacity{}); err != nil {
		t.Fatalf("seed dock door: %v", err)
	}
	if _, err := registerZone.Execute(ctx, "WH1", "STOR", "AMB", shared.Ambient, false, nil, nil); err != nil {
		t.Fatalf("seed storage zone: %v", err)
	}
	if _, err := registerZone.Execute(ctx, "WH1", "DOCK", "OB", shared.Ambient, false, nil, nil); err != nil {
		t.Fatalf("seed dock zone: %v", err)
	}
	if _, err := registerAisle.Execute(ctx, "WH1-STOR-AMB", "A07", 7, shared.TwoWay); err != nil {
		t.Fatalf("seed storage aisle: %v", err)
	}
	if _, err := registerAisle.Execute(ctx, "WH1-DOCK-OB", "D01", 1, shared.TwoWay); err != nil {
		t.Fatalf("seed dock aisle: %v", err)
	}
	for _, raw := range []string{
		"WH1-STOR-AMB-A07-01-01-A",
		"WH1-STOR-AMB-A07-01-02-A",
		"WH1-STOR-AMB-A07-03-01-A",
	} {
		if _, err := registerSlot.Execute(ctx, code(raw), placement.PalletRack, shared.Capacity{}, "", nil); err != nil {
			t.Fatalf("seed slot %s: %v", raw, err)
		}
	}
	if _, err := registerSlot.Execute(ctx, code("WH1-DOCK-OB-D01-01-01-A"), "DockDoor", shared.Capacity{}, "Outbound", nil); err != nil {
		t.Fatalf("seed dock slot: %v", err)
	}

	server := mcpadapter.NewServer(mcpadapter.Deps{
		GetSiteLayout: &usecases.GetSiteLayout{Sites: sites, Zones: zones, Aisles: aisles, Slots: slots, Structures: postgres.NewFixedStructureRepo(pool)},
		GetZoneGrid:   &usecases.GetZoneGrid{Zones: zones, Aisles: aisles, Slots: slots},
		ListSites:     &usecases.ListSites{Sites: sites},
		ListLocationsByRole: &usecases.ListLocationsByRole{
			Sites: sites, Zones: zones, Slots: slots,
		},
		GetZoneTravelGraph: &usecases.GetZoneTravelGraph{
			Zones: zones, Aisles: aisles, Slots: slots, CrossAisles: crossAisles,
		},
		EstimateTravelDistance: &usecases.EstimateTravelDistance{
			Zones: zones, Aisles: aisles, Slots: slots, CrossAisles: crossAisles,
		},
	})

	hs := httptest.NewServer(mcpadapter.Handler(server))
	t.Cleanup(hs.Close)

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "itcov-test-host", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint: hs.URL, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	return &mcpIntegrationHarness{session: session, publisher: publisher}
}

// callTool invokes a tool and fails the test on a transport error — the
// fleet's rule is that rejections travel as tool errors, not protocol
// failures.
func (h *mcpIntegrationHarness) callTool(t *testing.T, name string, args map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	res, err := h.session.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("tools/call %s: %v", name, err)
	}
	return res
}

// resultText concatenates a tool result's text content.
func resultText(res *sdkmcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if text, ok := c.(*sdkmcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

// structured returns the tool result's structured content as a map.
func structured(t *testing.T, res *sdkmcp.CallToolResult) map[string]any {
	t.Helper()
	m, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("no structured content: %+v", res.StructuredContent)
	}
	return m
}

func TestMCPIntegration_ListToolsExposesTheContract(t *testing.T) {
	h := newMCPIntegrationHarness(t)

	list, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	// Every tool this read-only Open Host Service registers.
	for _, want := range []string{
		"list_sites", "get_site_layout", "get_zone_grid",
		"list_functional_locations", "get_zone_travel_graph", "estimate_travel_distance",
	} {
		if !names[want] {
			t.Fatalf("tools/list must expose %q, got %v", want, names)
		}
	}
	// This context is read-only: every tool must be annotated as such so a
	// host can gate writes — there is no write tool to annotate.
	for _, tool := range list.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool %q must carry ReadOnlyHint=true", tool.Name)
		}
	}
}

func TestMCPIntegration_CallToolRoundTripThroughPostgres(t *testing.T) {
	h := newMCPIntegrationHarness(t)

	// list_sites: the seeded site over the real repo.
	sites := h.callTool(t, "list_sites", map[string]any{})
	if sites.IsError {
		t.Fatalf("list_sites returned a tool error: %s", resultText(sites))
	}
	siteList, ok := structured(t, sites)["sites"].([]any)
	if !ok || len(siteList) != 1 {
		t.Fatalf("expected 1 site over the wire, got %+v", sites.StructuredContent)
	}

	// get_site_layout: the whole nested chain from Postgres.
	layout := h.callTool(t, "get_site_layout", map[string]any{"siteCode": "WH1"})
	if layout.IsError {
		t.Fatalf("get_site_layout returned a tool error: %s", resultText(layout))
	}
	site, ok := structured(t, layout)["site"].(map[string]any)
	if !ok || site["code"] != "WH1" {
		t.Fatalf("layout must carry site WH1, got %+v", layout.StructuredContent)
	}

	// get_zone_grid: the seeded zone's drawable matrix.
	grid := h.callTool(t, "get_zone_grid", map[string]any{"zoneId": "WH1-STOR-AMB"})
	if grid.IsError {
		t.Fatalf("get_zone_grid returned a tool error: %s", resultText(grid))
	}
	gridBody := structured(t, grid)
	if gridBody["zoneId"] != "WH1-STOR-AMB" {
		t.Fatalf("grid must carry zoneId WH1-STOR-AMB, got %+v", gridBody)
	}
	columns, ok := gridBody["columns"].([]any)
	if !ok || len(columns) != 2 {
		t.Fatalf("expected 2 (aisle, bay) columns for bays 01 and 03, got %+v", gridBody["columns"])
	}

	// list_functional_locations: the seeded dock door, with its dock flow.
	functional := h.callTool(t, "list_functional_locations", map[string]any{"siteCode": "WH1", "role": "Dock"})
	if functional.IsError {
		t.Fatalf("list_functional_locations returned a tool error: %s", resultText(functional))
	}
	locations, ok := structured(t, functional)["locations"].([]any)
	if !ok || len(locations) != 1 {
		t.Fatalf("expected 1 dock location, got %+v", functional.StructuredContent)
	}
	dock, _ := locations[0].(map[string]any)
	if dock["locationCode"] != "WH1-DOCK-OB-D01-01-01-A" || dock["dockFlow"] != "Outbound" {
		t.Fatalf("dock door did not round-trip: %+v", dock)
	}

	// get_zone_travel_graph: waypoints and edges derived from Postgres state.
	graph := h.callTool(t, "get_zone_travel_graph", map[string]any{"zoneId": "WH1-STOR-AMB"})
	if graph.IsError {
		t.Fatalf("get_zone_travel_graph returned a tool error: %s", resultText(graph))
	}
	graphBody := structured(t, graph)
	nodes, ok := graphBody["nodes"].([]any)
	if !ok || len(nodes) == 0 {
		t.Fatalf("travel graph must expose waypoints, got %+v", graphBody)
	}
	if edges, ok := graphBody["edges"].([]any); !ok || len(edges) == 0 {
		t.Fatalf("travel graph must expose edges, got %+v", graphBody["edges"])
	}

	// estimate_travel_distance: a routed same-zone answer over the pitch
	// fallback (no geometry recorded), flagged estimated.
	distance := h.callTool(t, "estimate_travel_distance", map[string]any{
		"from": "WH1-STOR-AMB-A07-01-01-A",
		"to":   "WH1-STOR-AMB-A07-03-01-A",
	})
	if distance.IsError {
		t.Fatalf("estimate_travel_distance returned a tool error: %s", resultText(distance))
	}
	distBody := structured(t, distance)
	if metres, ok := distBody["metresM"].(float64); !ok || metres <= 0 {
		t.Fatalf("expected a positive routed distance, got %+v", distBody)
	}
	if estimated, ok := distBody["estimated"].(bool); !ok || !estimated {
		t.Fatalf("a pitch-fallback route must be flagged estimated, got %+v", distBody)
	}

	// The writes that seeded all this really went through the real stack:
	// the publisher saw every registration event.
	for _, name := range []string{
		"SiteRegistered", "ZoneRegistered", "AisleRegistered", "LocationSlotRegistered",
	} {
		var saw bool
		for _, e := range h.publisher.Events() {
			if e.EventName() == name {
				saw = true
			}
		}
		if !saw {
			t.Fatalf("expected %q to be published through the real stack, got %+v", name, h.publisher.Events())
		}
	}
}

func TestMCPIntegration_CallToolSurfacesRejectionsAsToolErrors(t *testing.T) {
	h := newMCPIntegrationHarness(t)

	cases := []struct {
		name     string
		tool     string
		args     map[string]any
		wantSlug string
	}{
		// A domain rejection: the named site does not exist.
		{"unknown site", "get_site_layout", map[string]any{"siteCode": "GHOST"}, "site-not-found"},
		// A domain rejection: the named zone does not exist.
		{"unknown zone", "get_zone_grid", map[string]any{"zoneId": "WH1-STOR-NOPE"}, "zone-not-found"},
		// Invalid input: a required argument is empty.
		{"empty site code", "get_site_layout", map[string]any{"siteCode": ""}, "invalid-site-code"},
		// Invalid input: a malformed seven-segment location code.
		{"malformed location code", "estimate_travel_distance", map[string]any{
			"from": "not-a-location", "to": "WH1-STOR-AMB-A07-01-01-A",
		}, "malformed-location-code"},
		// A domain rejection: routing between zones without geometry refuses.
		{"cross zone without geometry", "estimate_travel_distance", map[string]any{
			"from": "WH1-STOR-AMB-A07-01-01-A", "to": "WH1-DOCK-OB-D01-01-01-A",
		}, "no-route-between-zones"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.callTool(t, tc.tool, tc.args)
			if !res.IsError {
				t.Fatalf("%s must surface a tool error, got success: %+v", tc.tool, res.StructuredContent)
			}
			// The fleet convention (ADR-0033): the error text reads
			// "<slug>: detail", so the caller can classify it.
			if text := resultText(res); !strings.HasPrefix(text, tc.wantSlug+":") {
				t.Fatalf("error text must start with %q, got %q", tc.wantSlug+":", text)
			}
		})
	}
}
