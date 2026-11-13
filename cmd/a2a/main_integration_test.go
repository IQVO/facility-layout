//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	inbounda2a "github.com/claudioed/facility-layout/internal/adapters/inbound/a2a"
	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

// The cmd-level proof for cmd/a2a: the binary's own HTTP surface — the real
// JSON-RPC router with the Agent Card at the well-known path — answers, over
// a real testcontainers Postgres, exactly what the write side persisted.
// ADR-0043 fixes this agent as read-only ("no write skill, because the MCP
// surface offers no write"; cmd/a2a "wires no write use case, no event
// publisher and no outbox"), so its skill calls must add no outbox row.
//
// The map is seeded through the REAL write use cases with the REAL
// transactional outbox (ADR-0018) — the machinery cmd/facility drives over
// REST — so the test also proves the write path that feeds the skills.

// The canonical map these tests seed (mirroring cmd/mcp's integration
// suite): WH1 with a storage zone (two PalletRack slots in A07, bays 01 and
// 03), a dock zone (one inbound DockDoor with a GLN) and a yard zone (one
// PARKING YardSpot), plus a GLN on the site itself (ADR-0045).
const (
	seedSite     = "WH1"
	seedZoneID   = "WH1-STOR-AMB"
	storageSlotA = "WH1-STOR-AMB-A07-01-01-A"
	storageSlotB = "WH1-STOR-AMB-A07-03-01-A"
	dockSlot     = "WH1-DOCK-D01-D01-01-01-A"
	yardSlot     = "WH1-YARD-Y01-Y01-01-01-A"
	// seedEventCount: 1 site + 3 location types + 3 zones + 3 aisles +
	// 4 slots, each enqueued once per outbox topic (ADR-0018).
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

// One testcontainers Postgres serves the whole package: booted in TestMain,
// migrated once into a template database, one private clone per test. Never
// an external DATABASE_URL, never t.Skip, never a localhost DSN.
const a2aTemplateDB = "a2a_migrated_template"

var (
	a2aSharedURL string
	a2aDBSeq     atomic.Uint64
	a2aStartOnce sync.Once
	a2aStartErr  error
	a2aContainer *tcpostgres.PostgresContainer
)

func TestMain(m *testing.M) {
	code := m.Run()
	if a2aContainer != nil {
		if err := testcontainers.TerminateContainer(a2aContainer); err != nil {
			fmt.Fprintf(os.Stderr, "terminate postgres container: %v\n", err)
		}
	}
	os.Exit(code)
}

// a2aStart boots the container and the migrated template exactly once.
func a2aStart() error {
	a2aStartOnce.Do(func() {
		ctx := context.Background()
		container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("facility_a2a"),
			tcpostgres.WithUsername("facility"),
			tcpostgres.WithPassword("facility"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err != nil {
			a2aStartErr = fmt.Errorf("start postgres container: %w", err)
			return
		}
		a2aContainer = container
		if a2aSharedURL, err = container.ConnectionString(ctx, "sslmode=disable"); err != nil {
			a2aStartErr = fmt.Errorf("connection string: %w", err)
			return
		}
		conn, err := pgx.Connect(ctx, a2aSharedURL)
		if err != nil {
			a2aStartErr = fmt.Errorf("connect: %w", err)
			return
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", a2aTemplateDB)); err != nil {
			a2aStartErr = fmt.Errorf("create template database: %w", err)
			return
		}
		if err := postgres.RunMigrations(a2aWithDB(a2aSharedURL, a2aTemplateDB), a2aMigrationsDir()); err != nil {
			a2aStartErr = fmt.Errorf("migrate template: %w", err)
		}
	})
	return a2aStartErr
}

// a2aMigrationsDir resolves /migrations relative to this test file, so the
// harness works regardless of the working directory go test runs from.
func a2aMigrationsDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("unable to resolve test file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
}

// a2aWithDB rewrites the path of a connection URL to the named database.
func a2aWithDB(baseURL, name string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		panic(err)
	}
	u.Path = "/" + name
	return u.String()
}

// a2aFreshDB hands the test its own private database, cloned from the
// migrated template.
func a2aFreshDB(t *testing.T) string {
	t.Helper()
	if err := a2aStart(); err != nil {
		t.Fatalf("shared postgres: %v", err)
	}
	name := fmt.Sprintf("a2a_cmd_%d", a2aDBSeq.Add(1))
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, a2aSharedURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q WITH TEMPLATE %q", name, a2aTemplateDB)); err != nil {
		t.Fatalf("clone database: %v", err)
	}
	return a2aWithDB(a2aSharedURL, name)
}

// seedFacilityMap registers the canonical map through the real write use
// cases — real Postgres repos, the real UnitOfWork bracket and the real
// OutboxPublisher fanning every event to both topics — exactly the stack
// cmd/facility's REST surface drives.
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
		zoneID string
		aisle  string
		hint   int
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

// countOutboxRows counts outbox_events rows.
func countOutboxRows(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM outbox_events").Scan(&n); err != nil {
		t.Fatalf("count outbox_events: %v", err)
	}
	return n
}

// TestA2AAgainstPostgres_ReadsWhatTheWriteSidePersistedAndNeverWrites is
// the cmd-level A2A integration test: the binary migrates its database
// (idempotently), serves its real router (JSON-RPC plus the Agent Card at
// the well-known path), answers both documented skills from what the write
// use cases persisted, rejects an unknown skill, and — because ADR-0043
// fixes the agent as read-only — leaves the outbox untouched.
func TestA2AAgainstPostgres_ReadsWhatTheWriteSidePersistedAndNeverWrites(t *testing.T) {
	dbURL := a2aFreshDB(t)
	ctx := context.Background()

	pool, err := postgres.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	seedFacilityMap(t, pool)
	before := countOutboxRows(t, pool)
	if before != 2*seedEventCount {
		t.Fatalf("outbox rows after the seed = %d, want %d (one row per event per topic)", before, 2*seedEventCount)
	}

	// The binary's own composition root: real migrations re-run + ping.
	adapters, closeAdapters, err := buildAdapters(dbURL, dbURL, a2aMigrationsDir(), quietLogger())
	if err != nil {
		t.Fatalf("buildAdapters: %v", err)
	}
	t.Cleanup(closeAdapters)

	// The exact Deps run() wires: only the two read use cases (ADR-0043's
	// write gate).
	deps := inbounda2a.Deps{
		GetSiteLayout:          &usecases.GetSiteLayout{Sites: adapters.sites, Zones: adapters.zones, Aisles: adapters.aisles, Slots: adapters.slots, Structures: adapters.structures},
		EstimateTravelDistance: &usecases.EstimateTravelDistance{Zones: adapters.zones, Aisles: adapters.aisles, Slots: adapters.slots, CrossAisles: adapters.crossAisles},
	}

	// The real HTTP surface: healthz, the well-known Agent Card, and the
	// JSON-RPC endpoint at both mounts.
	srv := httptest.NewServer(newRouter(
		inbounda2a.Handler(inbounda2a.NewExecutor(deps)),
		inbounda2a.Card(inbounda2a.Config{AgentURL: "http://placeholder.invalid/", Version: "it"}),
	))
	t.Cleanup(srv.Close)

	assertAgentCardServed(t, srv)

	client, err := a2aclient.NewFromCard(ctx, inbounda2a.Card(inbounda2a.Config{AgentURL: srv.URL + "/", Version: "it"}))
	if err != nil {
		t.Fatalf("client from card: %v", err)
	}

	assertSiteLayoutSkill(t, client)
	assertTravelDistanceSkill(t, client)
	assertProblemAnswers(t, client)

	if after := countOutboxRows(t, pool); after != before {
		t.Fatalf("outbox rows %d -> %d: the documented read-only A2A agent wrote an event", before, after)
	}
}

// assertAgentCardServed proves the discovery contract (ADR-0043): the card
// is served at the well-known path and advertises exactly the two curated
// read skills, with no security schemes (ADR-0015).
func assertAgentCardServed(t *testing.T, srv *httptest.Server) {
	t.Helper()
	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil {
		t.Fatalf("get agent card: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("agent card status = %d, want 200", res.StatusCode)
	}
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		Name            string `json:"name"`
		SecuritySchemes any    `json:"securitySchemes"`
		Skills          []struct {
			ID string `json:"id"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(raw, &card); err != nil {
		t.Fatalf("agent card is not JSON: %v: %s", err, raw)
	}
	if card.Name != "facility-layout" {
		t.Fatalf("agent card name = %q", card.Name)
	}
	if card.SecuritySchemes != nil {
		t.Fatal("the card must declare no security schemes (ADR-0015)")
	}
	got := make([]string, 0, len(card.Skills))
	for _, s := range card.Skills {
		got = append(got, s.ID)
	}
	want := "site_layout,travel_distance"
	if strings.Join(got, ",") != want {
		t.Fatalf("card skills = %v, want [%s] and nothing else (no write skill)", got, want)
	}
}

// envelopeOf sends one skill call and returns the decoded envelope plus the
// raw JSON, for assertions and failure messages.
func envelopeOf(t *testing.T, client *a2aclient.Client, skill string, args map[string]any) (map[string]any, string) {
	t.Helper()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatalf("%s: %v", skill, err)
	}
	reply, ok := resp.(*a2a.Message)
	if !ok || len(reply.Parts) < 2 {
		t.Fatalf("%s: reply = %T, want a Message with a data and a text part", skill, resp)
	}
	data, ok := reply.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("%s: first part is %T, want the structured envelope", skill, reply.Parts[0].Content)
	}
	raw, err := json.Marshal(data.Value)
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	return env, string(raw)
}

// assertSiteLayoutSkill proves site_layout answers the seeded map: the
// site header (with its GLN, ADR-0045), the storage zone, its aisle and
// both slot codes.
func assertSiteLayoutSkill(t *testing.T, client *a2aclient.Client) {
	t.Helper()
	env, raw := envelopeOf(t, client, "site_layout", map[string]any{"siteCode": seedSite})
	if env["problem"] != nil {
		t.Fatalf("site_layout problem = %s", raw)
	}
	layout, _ := env["data"].(map[string]any)["siteLayout"].(map[string]any)
	header, _ := layout["site"].(map[string]any)
	if header["code"] != seedSite || header["name"] != "Fulfilment Centre One" || header["gln"] != seedSiteGLN {
		t.Fatalf("site_layout header = %s", raw)
	}
	var storage map[string]any
	for _, z := range layout["zones"].([]any) {
		if m := z.(map[string]any); m["zoneId"] == seedZoneID {
			storage = m
		}
	}
	if storage == nil {
		t.Fatalf("site_layout has no zone %s: %s", seedZoneID, raw)
	}
	if storage["temperatureClass"] != "Ambient" {
		t.Fatalf("storage zone = %s", raw)
	}
	aisles := storage["aisles"].([]any)
	if len(aisles) != 1 {
		t.Fatalf("storage zone aisles = %s", raw)
	}
	a := aisles[0].(map[string]any)
	if a["aisleCode"] != "A07" || a["direction"] != "TwoWay" {
		t.Fatalf("A07 = %s", raw)
	}
	if codes := a["slotCodes"].([]any); len(codes) != 2 || codes[0] != storageSlotA || codes[1] != storageSlotB {
		t.Fatalf("A07 slotCodes = %s", raw)
	}
}

// assertTravelDistanceSkill proves travel_distance routes the same-zone
// request over the zone's travel graph: one adjacent-bay edge at the
// default bay pitch (ADR-0017's fallback, since no aisle geometry was
// recorded), flagged estimated, with the from/to codes echoed.
func assertTravelDistanceSkill(t *testing.T, client *a2aclient.Client) {
	t.Helper()
	want := zone.DefaultBayPitchM
	env, raw := envelopeOf(t, client, "travel_distance", map[string]any{"from": storageSlotA, "to": storageSlotB})
	if env["problem"] != nil {
		t.Fatalf("travel_distance problem = %s", raw)
	}
	d, _ := env["data"].(map[string]any)["travelDistance"].(map[string]any)
	if d["from"] != storageSlotA || d["to"] != storageSlotB {
		t.Fatalf("travel_distance echoes = %s", raw)
	}
	if d["metresM"] != want || d["estimated"] != true {
		t.Fatalf("travel_distance = %s, want %v m estimated (bay-pitch fallback)", raw, want)
	}
	route := d["route"].([]any)
	if len(route) != 2 || route[0].(map[string]any)["bay"] != "01" || route[1].(map[string]any)["bay"] != "03" {
		t.Fatalf("travel_distance route = %s, want bay 01 then bay 03 of A07", raw)
	}
}

// assertProblemAnswers proves the envelope's problem answers: an unknown
// site is the 404 site-not-found problem (the same slug REST uses), and an
// unknown skill — any write verb among them — is the 400 unknown-skill
// problem, because this agent offers no write skill (ADR-0043).
func assertProblemAnswers(t *testing.T, client *a2aclient.Client) {
	t.Helper()
	env, raw := envelopeOf(t, client, "site_layout", map[string]any{"siteCode": "GHOST"})
	problem, _ := env["problem"].(map[string]any)
	if problem == nil || problem["status"] != 404.0 || !strings.HasSuffix(problem["type"].(string), "/site-not-found") {
		t.Fatalf("an unknown site must be the 404 site-not-found problem, got %s", raw)
	}

	env, raw = envelopeOf(t, client, "register_site", map[string]any{"siteCode": "WH9", "name": "Nope"})
	problem, _ = env["problem"].(map[string]any)
	if problem == nil || problem["status"] != 400.0 || !strings.HasSuffix(problem["type"].(string), "/unknown-skill") {
		t.Fatalf("a write skill must be the 400 unknown-skill problem (no write skill exists), got %s", raw)
	}
}
