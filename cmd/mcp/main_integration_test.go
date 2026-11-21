//go:build integration

package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	inboundmcp "github.com/claudioed/facility-layout/internal/adapters/inbound/mcp"
	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

// The cmd-level proof for cmd/mcp: the binary's own composition
// (buildAdapters + the exact Deps run() wires) serves, over the real MCP
// SDK against a real testcontainers Postgres, exactly what the write side
// persisted — and, because facility-layout's MCP surface is documented
// read-only (ADR-0007; docs/docs/ddd/use-cases.md "the MCP server wires
// only read use cases, so it cannot mutate the map"), its reads must add no
// outbox row.
//
// The map is seeded through the REAL write use cases with the REAL
// transactional outbox (ADR-0018) — the same machinery cmd/facility drives
// over REST — so the test also proves the write path that feeds the reads:
// every registration persists AND enqueues its event on both topics.

// The canonical map these tests seed. WH1 owns a storage zone (two PalletRack
// slots in A07, bays 01 and 03), a dock zone (one inbound DockDoor with a
// GLN) and a yard zone (one PARKING YardSpot), plus a GLN on the site
// itself (ADR-0045).
const (
	seedSite       = "WH1"
	seedZoneID     = "WH1-STOR-AMB"
	storageSlotA   = "WH1-STOR-AMB-A07-01-01-A"
	storageSlotB   = "WH1-STOR-AMB-A07-03-01-A"
	dockSlot       = "WH1-DOCK-D01-D01-01-01-A"
	yardSlot       = "WH1-YARD-Y01-Y01-01-01-A"
	topicEvents    = "warehouse.facility.events"
	topicAnalytics = "warehouse.facility.analytics"
	// seedEventCount: 1 site + 3 location types + 3 zones + 3 aisles +
	// 4 slots. Each event is enqueued once per topic (ADR-0018).
	seedEventCount = 14
)

var (
	seedSiteGLN = glnOf("545013100001")
	seedDockGLN = glnOf("545013100002")
)

// glnOf appends the GS1 mod-10 check digit to a 12-digit GLN body
// (ADR-0045), so the fixtures stay valid by construction.
func glnOf(body string) string {
	return fmt.Sprintf("%s%d", body, shared.GLNCheckDigit(body))
}

// One testcontainers Postgres serves the whole package. TestMain starts it,
// migrates a template database once, and every test then gets its own
// database cloned from that template — the same isolation strategy as
// internal/application/usecases's integration harness. Never an external
// DATABASE_URL, never t.Skip, never a localhost DSN.
const mcpTemplateDB = "mcp_migrated_template"

var (
	mcpSharedURL string
	mcpDBSeq     atomic.Uint64
	mcpStartOnce sync.Once
	mcpStartErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	mcpShutdown()
	os.Exit(code)
}

// mcpShutdown terminates the package's container, if one was started.
func mcpShutdown() {
	if mcpContainer != nil {
		if err := testcontainers.TerminateContainer(mcpContainer); err != nil {
			fmt.Fprintf(os.Stderr, "terminate postgres container: %v\n", err)
		}
	}
}

var mcpContainer *tcpostgres.PostgresContainer

// mcpStart boots the container and the migrated template exactly once.
func mcpStart() error {
	mcpStartOnce.Do(func() {
		ctx := context.Background()
		container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("facility_mcp"),
			tcpostgres.WithUsername("facility"),
			tcpostgres.WithPassword("facility"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err != nil {
			mcpStartErr = fmt.Errorf("start postgres container: %w", err)
			return
		}
		mcpContainer = container
		if mcpSharedURL, err = container.ConnectionString(ctx, "sslmode=disable"); err != nil {
			mcpStartErr = fmt.Errorf("connection string: %w", err)
			return
		}
		conn, err := pgx.Connect(ctx, mcpSharedURL)
		if err != nil {
			mcpStartErr = fmt.Errorf("connect: %w", err)
			return
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", mcpTemplateDB)); err != nil {
			mcpStartErr = fmt.Errorf("create template database: %w", err)
			return
		}
		if err := postgres.RunMigrations(mcpWithDB(mcpSharedURL, mcpTemplateDB), mcpMigrationsDir()); err != nil {
			mcpStartErr = fmt.Errorf("migrate template: %w", err)
		}
	})
	return mcpStartErr
}

// mcpMigrationsDir resolves /migrations relative to this test file, so the
// harness works regardless of the working directory go test runs from.
func mcpMigrationsDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("unable to resolve test file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
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

// mcpFreshDB hands the test its own private database, cloned from the
// migrated template.
func mcpFreshDB(t *testing.T) string {
	t.Helper()
	if err := mcpStart(); err != nil {
		t.Fatalf("shared postgres: %v", err)
	}
	name := fmt.Sprintf("mcp_cmd_%d", mcpDBSeq.Add(1))
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, mcpSharedURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q WITH TEMPLATE %q", name, mcpTemplateDB)); err != nil {
		t.Fatalf("clone database: %v", err)
	}
	return mcpWithDB(mcpSharedURL, name)
}

// seedFacilityMap registers the canonical map through the real write use
// cases — real Postgres repos, the real UnitOfWork bracket and the real
// OutboxPublisher fanning every event to the integration and analytics
// topics — exactly the stack cmd/facility's REST surface drives.
func seedFacilityMap(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	var seq atomic.Uint64
	newId := func() string { return fmt.Sprintf("itcov-evt-%03d", seq.Add(1)) }
	publisher := postgres.NewOutboxPublisher(pool, newId,
		&outboundkafka.Publisher{NewId: newId},
		&outboundkafka.AnalyticsPublisher{NewId: newId},
	)
	clock := memory.NewFixedClock(time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC))
	uow := postgres.NewUnitOfWork(pool)
	glns := postgres.NewGLNRegistry(pool)

	sites := postgres.NewSiteRepo(pool)
	zones := postgres.NewZoneRepo(pool)
	aisles := postgres.NewAisleRepo(pool)
	slots := postgres.NewSlotRepo(pool)
	locationTypes := postgres.NewLocationTypeRepo(pool)

	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	code := func(raw string) shared.LocationCode {
		t.Helper()
		parsed, err := shared.ParseLocationCode(raw)
		must(err, "parse "+raw)
		return parsed
	}
	capacity := func(weight, volume float64) shared.Capacity {
		t.Helper()
		c, err := shared.NewCapacity(weight, volume)
		must(err, "capacity")
		return c
	}

	registerSite := &usecases.RegisterSite{Sites: sites, Events: publisher, Clock: clock, UnitOfWork: uow, GLNs: glns}
	registerType := &usecases.RegisterLocationType{LocationTypes: locationTypes, Events: publisher, Clock: clock, UnitOfWork: uow}
	registerZone := &usecases.RegisterZone{Sites: sites, Zones: zones, Events: publisher, Clock: clock, UnitOfWork: uow}
	registerAisle := &usecases.RegisterAisle{Zones: zones, Aisles: aisles, Events: publisher, Clock: clock, UnitOfWork: uow}
	registerSlot := &usecases.RegisterLocationSlot{
		Sites: sites, Zones: zones, Aisles: aisles, Slots: slots,
		LocationTypes: locationTypes, Rules: postgres.NewPlacementRuleRepo(pool),
		Events: publisher, Clock: clock, UnitOfWork: uow, GLNs: glns,
	}

	_, err := registerSite.ExecuteWithGLN(ctx, seedSite, "Fulfilment Centre One", site.RoleUnspecified, seedSiteGLN)
	must(err, "register site")
	_, err = registerType.Execute(ctx, placement.PalletRack, placement.Storage, capacity(1200, 2.4))
	must(err, "register PalletRack")
	_, err = registerType.Execute(ctx, "DockDoor", placement.Dock, capacity(10, 0.5))
	must(err, "register DockDoor")
	_, err = registerType.Execute(ctx, "YardSpot", placement.Yard, capacity(10, 0.5))
	must(err, "register YardSpot")
	for _, z := range []struct{ area, zone string }{{"STOR", "AMB"}, {"DOCK", "D01"}, {"YARD", "Y01"}} {
		_, err = registerZone.Execute(ctx, seedSite, z.area, z.zone, shared.Ambient, false, nil, nil)
		must(err, "register zone "+z.zone)
	}
	for _, a := range []struct {
		zoneID, aisle string
		hint          int
	}{
		{seedZoneID, "A07", 7},
		{"WH1-DOCK-D01", "D01", 1},
		{"WH1-YARD-Y01", "Y01", 1},
	} {
		_, err = registerAisle.Execute(ctx, a.zoneID, a.aisle, a.hint, shared.TwoWay)
		must(err, "register aisle "+a.aisle)
	}
	_, err = registerSlot.Execute(ctx, code(storageSlotA), placement.PalletRack, shared.Capacity{}, "", nil)
	must(err, "register storage slot A")
	_, err = registerSlot.Execute(ctx, code(storageSlotB), placement.PalletRack, shared.Capacity{}, "", nil)
	must(err, "register storage slot B")
	_, err = registerSlot.ExecuteWithOptions(ctx, code(dockSlot), "DockDoor", shared.Capacity{}, "Inbound", nil,
		usecases.SlotOptions{GLN: seedDockGLN})
	must(err, "register dock slot")
	_, err = registerSlot.ExecuteWithOptions(ctx, code(yardSlot), "YardSpot", shared.Capacity{}, "", nil,
		usecases.SlotOptions{SpotKind: string(shared.SpotKindParking)})
	must(err, "register yard slot")
}

// countOutboxRows counts outbox_events rows matching where ("" for all).
func countOutboxRows(t *testing.T, pool *pgxpool.Pool, where string) int {
	t.Helper()
	q := "SELECT count(*) FROM outbox_events"
	if where != "" {
		q += " WHERE " + where
	}
	var n int
	if err := pool.QueryRow(context.Background(), q).Scan(&n); err != nil {
		t.Fatalf("count outbox_events: %v", err)
	}
	return n
}

// TestMCPAgainstPostgres_ReadsWhatTheWriteSidePersistedAndNeverWrites is the
// cmd-level MCP integration test: the binary migrates its database
// (idempotently: the clone is already migrated, a second run is a no-op),
// serves every read tool over the real SDK, answers exactly what the write
// use cases persisted, and — because the surface is documented read-only —
// leaves the outbox untouched.
func TestMCPAgainstPostgres_ReadsWhatTheWriteSidePersistedAndNeverWrites(t *testing.T) {
	dbURL := mcpFreshDB(t)
	ctx := context.Background()

	pool, err := postgres.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	seedFacilityMap(t, pool)

	// The write path persisted AND enqueued: every event on both topics.
	if n := countOutboxRows(t, pool, "topic = '"+topicEvents+"'"); n != seedEventCount {
		t.Fatalf("integration-topic outbox rows = %d, want %d", n, seedEventCount)
	}
	if n := countOutboxRows(t, pool, "topic = '"+topicAnalytics+"'"); n != seedEventCount {
		t.Fatalf("analytics-topic outbox rows = %d, want %d", n, seedEventCount)
	}
	before := countOutboxRows(t, pool, "")

	// The binary's own composition root: real migrations re-run + ping.
	adapters, closeAdapters, err := buildAdapters(dbURL, dbURL, mcpMigrationsDir(), quietLoggerMCP())
	if err != nil {
		t.Fatalf("buildAdapters: %v", err)
	}
	t.Cleanup(closeAdapters)

	// The exact Deps run() wires (minus the optional reports client, which
	// is not registered when REPORTS_BASE_URL is unset).
	server := inboundmcp.NewServer(inboundmcp.Deps{
		GetSiteLayout:          &usecases.GetSiteLayout{Sites: adapters.sites, Zones: adapters.zones, Aisles: adapters.aisles, Slots: adapters.slots, Structures: adapters.structures},
		GetZoneGrid:            &usecases.GetZoneGrid{Zones: adapters.zones, Aisles: adapters.aisles, Slots: adapters.slots},
		ListSites:              &usecases.ListSites{Sites: adapters.sites},
		ListLocationsByRole:    &usecases.ListLocationsByRole{Sites: adapters.sites, Zones: adapters.zones, Slots: adapters.slots},
		GetZoneTravelGraph:     &usecases.GetZoneTravelGraph{Zones: adapters.zones, Aisles: adapters.aisles, Slots: adapters.slots, CrossAisles: adapters.crossAisles},
		EstimateTravelDistance: &usecases.EstimateTravelDistance{Zones: adapters.zones, Aisles: adapters.aisles, Slots: adapters.slots, CrossAisles: adapters.crossAisles},
	})

	ct, st := sdk.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	session, err := sdk.NewClient(&sdk.Implementation{Name: "itcov", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	assertListSites(t, session)
	assertGetSiteLayout(t, session)
	assertGetZoneGrid(t, session)
	assertListFunctionalLocations(t, session)
	assertZoneTravelGraphAndDistance(t, session)
	assertUnknownSiteIsAToolError(t, session)

	if after := countOutboxRows(t, pool, ""); after != before {
		t.Fatalf("outbox rows %d -> %d: the documented read-only MCP surface wrote an event", before, after)
	}
}

// mcpCall invokes one tool and returns its structured content.
func mcpCall(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s: tool error: %+v", name, res.Content)
	}
	out, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("%s: structured content is not an object: %+v", name, res.StructuredContent)
	}
	return out
}

// assertListSites proves list_sites returns the registered site with its
// ADR-0035/0037/0045 header fields.
func assertListSites(t *testing.T, session *sdk.ClientSession) {
	t.Helper()
	out := mcpCall(t, session, "list_sites", map[string]any{})
	sites, ok := out["sites"].([]any)
	if !ok || len(sites) != 1 {
		t.Fatalf("list_sites = %+v, want exactly the seeded site", out)
	}
	s := sites[0].(map[string]any)
	if s["code"] != seedSite || s["name"] != "Fulfilment Centre One" || s["siteRole"] != "UNSPECIFIED" {
		t.Fatalf("list_sites entry = %+v", s)
	}
	if s["gln"] != seedSiteGLN {
		t.Fatalf("list_sites gln = %v, want the site's GLN (ADR-0045)", s["gln"])
	}
	if s["capabilityRevision"] != 0.0 || s["transferOriginEnabled"] != false {
		t.Fatalf("list_sites capability header = %+v", s)
	}
}

// zoneOf finds one zone DTO by id inside a get_site_layout result.
func zoneOf(t *testing.T, layout map[string]any, zoneID string) map[string]any {
	t.Helper()
	zones, ok := layout["zones"].([]any)
	if !ok {
		t.Fatalf("layout has no zones: %+v", layout)
	}
	for _, z := range zones {
		m := z.(map[string]any)
		if m["zoneId"] == zoneID {
			return m
		}
	}
	t.Fatalf("layout has no zone %s: %+v", zoneID, layout)
	return nil
}

// assertGetSiteLayout proves get_site_layout returns the nested map the
// write side registered: zone header, aisle, and slot codes.
func assertGetSiteLayout(t *testing.T, session *sdk.ClientSession) {
	t.Helper()
	layout := mcpCall(t, session, "get_site_layout", map[string]any{"siteCode": seedSite})
	siteHeader, ok := layout["site"].(map[string]any)
	if !ok || siteHeader["code"] != seedSite || siteHeader["gln"] != seedSiteGLN {
		t.Fatalf("get_site_layout site header = %+v", layout["site"])
	}
	if got := len(layout["zones"].([]any)); got != 3 {
		t.Fatalf("get_site_layout zones = %d, want the 3 seeded zones", got)
	}
	storage := zoneOf(t, layout, seedZoneID)
	if storage["temperatureClass"] != "Ambient" || storage["hazmat"] != false {
		t.Fatalf("storage zone header = %+v", storage)
	}
	aisles := storage["aisles"].([]any)
	if len(aisles) != 1 {
		t.Fatalf("storage zone aisles = %+v", storage["aisles"])
	}
	a := aisles[0].(map[string]any)
	if a["aisleCode"] != "A07" || a["direction"] != "TwoWay" || a["sequenceHint"] != 7.0 {
		t.Fatalf("A07 = %+v", a)
	}
	if codes := a["slotCodes"].([]any); len(codes) != 2 || codes[0] != storageSlotA || codes[1] != storageSlotB {
		t.Fatalf("A07 slotCodes = %+v, want both storage slots in bay order", a["slotCodes"])
	}
}

// assertGetZoneGrid proves get_zone_grid places the two storage slots at
// their (aisle, bay, level) coordinates.
func assertGetZoneGrid(t *testing.T, session *sdk.ClientSession) {
	t.Helper()
	grid := mcpCall(t, session, "get_zone_grid", map[string]any{"zoneId": seedZoneID})
	if grid["zoneId"] != seedZoneID {
		t.Fatalf("get_zone_grid = %+v", grid)
	}
	columns := grid["columns"].([]any)
	if len(columns) != 2 {
		t.Fatalf("grid columns = %+v, want (A07,01) and (A07,03)", grid["columns"])
	}
	for i, wantBay := range []string{"01", "03"} {
		c := columns[i].(map[string]any)
		if c["aisleCode"] != "A07" || c["bay"] != wantBay {
			t.Fatalf("column %d = %+v, want bay %s of A07 in walk order", i, c, wantBay)
		}
	}
	rows := grid["rows"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["level"] != "01" {
		t.Fatalf("grid rows = %+v, want the single level 01", grid["rows"])
	}
	cells := rows[0].(map[string]any)["cells"].([]any)
	wantSlots := []string{storageSlotA, storageSlotB}
	for i, want := range wantSlots {
		codes := cells[i].(map[string]any)["slotCodes"].([]any)
		if len(codes) != 1 || codes[0] != want {
			t.Fatalf("cell %d = %+v, want [%s]", i, codes, want)
		}
	}
}

// assertListFunctionalLocations proves the ADR-0016 role listing and the
// ADR-0045 identity fields: the dock door carries its flow and GLN, the yard
// spot its kind.
func assertListFunctionalLocations(t *testing.T, session *sdk.ClientSession) {
	t.Helper()
	docks := mcpCall(t, session, "list_functional_locations", map[string]any{"siteCode": seedSite, "role": "Dock"})
	locations := docks["locations"].([]any)
	if len(locations) != 1 {
		t.Fatalf("Dock locations = %+v", docks)
	}
	d := locations[0].(map[string]any)
	if d["locationCode"] != dockSlot || d["role"] != "Dock" || d["dockFlow"] != "Inbound" || d["gln"] != seedDockGLN {
		t.Fatalf("dock door = %+v", d)
	}
	yards := mcpCall(t, session, "list_functional_locations", map[string]any{"siteCode": seedSite, "role": "Yard"})
	locations = yards["locations"].([]any)
	if len(locations) != 1 {
		t.Fatalf("Yard locations = %+v", yards)
	}
	y := locations[0].(map[string]any)
	if y["locationCode"] != yardSlot || y["spotKind"] != string(shared.SpotKindParking) {
		t.Fatalf("yard spot = %+v", y)
	}
}

// assertZoneTravelGraphAndDistance proves the ADR-0017 surface: the zone's
// travel graph and the estimated same-aisle distance between bays 01 and
// 03. With no aisle geometry recorded, the per-adjacent-bay edge falls back
// to the zone's default bay pitch, and every such leg is flagged estimated.
func assertZoneTravelGraphAndDistance(t *testing.T, session *sdk.ClientSession) {
	t.Helper()
	want := zone.DefaultBayPitchM

	graph := mcpCall(t, session, "get_zone_travel_graph", map[string]any{"zoneId": seedZoneID})
	nodes := graph["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("travel graph nodes = %+v, want the two bay waypoints", graph["nodes"])
	}
	var sawEdge bool
	for _, e := range graph["edges"].([]any) {
		edge := e.(map[string]any)
		from := edge["from"].(map[string]any)
		to := edge["to"].(map[string]any)
		if from["bay"] == "01" && to["bay"] == "03" {
			sawEdge = true
			if edge["metresM"] != want || edge["estimated"] != true {
				t.Fatalf("edge 01->03 = %+v, want %v m estimated", edge, want)
			}
		}
	}
	if !sawEdge {
		t.Fatalf("travel graph has no 01->03 edge: %+v", graph["edges"])
	}

	distance := mcpCall(t, session, "estimate_travel_distance", map[string]any{"from": storageSlotA, "to": storageSlotB})
	if distance["metresM"] != want || distance["estimated"] != true {
		t.Fatalf("estimate_travel_distance = %+v, want %v m estimated (bay-pitch fallback)", distance, want)
	}
	route := distance["route"].([]any)
	if len(route) != 2 || route[0].(map[string]any)["bay"] != "01" || route[1].(map[string]any)["bay"] != "03" {
		t.Fatalf("route = %+v, want bay 01 then bay 03 of A07", distance["route"])
	}
}

// assertUnknownSiteIsAToolError proves the ADR-0033 error contract: an
// unknown site is a tool error whose text starts with the stable slug.
func assertUnknownSiteIsAToolError(t *testing.T, session *sdk.ClientSession) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "get_site_layout", Arguments: map[string]any{"siteCode": "GHOST"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("an unknown site must be a tool error, got %+v", res.StructuredContent)
	}
	if len(res.Content) == 0 {
		t.Fatal("the error result carries no content")
	}
	text, ok := res.Content[0].(*sdk.TextContent)
	if !ok || !strings.HasPrefix(text.Text, "site-not-found:") {
		t.Fatalf("error text = %+v, want the slug-prefixed site-not-found form", res.Content[0])
	}
}
