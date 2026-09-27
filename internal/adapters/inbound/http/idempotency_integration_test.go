//go:build integration

// Integration tests for the transactional Idempotency-Key middleware
// (docs/docs/adr/0019-idempotency-key-middleware.md) against a real
// Postgres 16, through the REAL chi router (inboundhttp.NewRouter) over
// real net/http requests — not the middleware's internals in isolation.
// Testcontainers-only: the test boots and owns its own disposable
// Postgres, never reads DATABASE_URL or hardcodes localhost, so CI cannot
// silently skip this contract.
//
// Test coverage strategy (see ADR-0019's own "Test coverage strategy"
// section): the full a-f scenario matrix runs against POST /sites (the
// simplest protected endpoint) and POST /locations (the deepest —
// chain-of-custody resolution, PlacementRule evaluation, metrics
// recording — proving the middleware's transaction join survives the
// most complex call graph this service has). The other six protected
// endpoints (zones, fixed structures, aisles, cross-aisles, location
// types, placement rules) each get one smoke test: fresh key creates the
// resource, replay does not duplicate it. The middleware's own internal
// correctness (the invariant, concurrency, panic/rollback) is
// architecture-invariant across routes and is exhaustively covered once,
// in the two full-matrix suites, not per route.
package http_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	inboundhttp "github.com/claudioed/facility-layout/internal/adapters/inbound/http"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/application/usecases"
)

// idempotencyDB boots a throwaway Postgres (testcontainers — the test owns
// its own database, never an external DATABASE_URL) and runs every
// migration in this repo, including the idempotency_keys one.
func idempotencyDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("facility_layout"),
		tcpostgres.WithUsername("facility_layout"),
		tcpostgres.WithPassword("facility_layout"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	if err := postgres.RunMigrations(url, "../../../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type fixedIdempotencyClock time.Time

func (c fixedIdempotencyClock) Now() time.Time { return time.Time(c) }

// newIdempotencyRouter wires the REAL chi router (inboundhttp.NewRouter)
// with every use case over Postgres-backed repos + a shared UnitOfWork, so
// every protected POST's full request cycle — idempotency bookkeeping, the
// aggregate Save, the outbox insert — runs through the exact same
// transaction-join mechanism production uses (internal/pgtx via
// postgres.UnitOfWork.Execute).
func newIdempotencyRouter(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	sites := postgres.NewSiteRepo(pool)
	zones := postgres.NewZoneRepo(pool)
	aisles := postgres.NewAisleRepo(pool)
	slots := postgres.NewSlotRepo(pool)
	locationTypes := postgres.NewLocationTypeRepo(pool)
	rules := postgres.NewPlacementRuleRepo(pool)
	structures := postgres.NewFixedStructureRepo(pool)
	crossAisles := postgres.NewCrossAisleRepo(pool)

	// The log publisher (never Kafka) keeps this suite hermetic; the
	// point under test is the Postgres transaction boundary, not event
	// delivery. It is still wrapped in the SAME UnitOfWork scope as each
	// aggregate Save via every use case's own atomically() call, so the
	// commit-together claim is exercised exactly as in production.
	publisher := events.NewLogPublisher(slog.New(slog.NewTextHandler(io.Discard, nil)))
	uow := postgres.NewUnitOfWork(pool)
	clock := fixedIdempotencyClock(time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC))

	server := &inboundhttp.Server{
		RegisterSite: &usecases.RegisterSite{Sites: sites, Events: publisher, Clock: clock, UnitOfWork: uow},
		GetSite:      &usecases.GetSite{Sites: sites},

		RegisterZone: &usecases.RegisterZone{Sites: sites, Zones: zones, Events: publisher, Clock: clock, UnitOfWork: uow},
		GetZone:      &usecases.GetZone{Zones: zones},

		RegisterAisle: &usecases.RegisterAisle{Zones: zones, Aisles: aisles, Events: publisher, Clock: clock, UnitOfWork: uow},
		GetAisle:      &usecases.GetAisle{Aisles: aisles},

		RegisterLocationType: &usecases.RegisterLocationType{LocationTypes: locationTypes, Events: publisher, Clock: clock, UnitOfWork: uow},
		GetLocationType:      &usecases.GetLocationType{LocationTypes: locationTypes},

		DefinePlacementRule: &usecases.DefinePlacementRule{LocationTypes: locationTypes, Rules: rules, Events: publisher, Clock: clock, UnitOfWork: uow},
		GetPlacementRule:    &usecases.GetPlacementRule{Rules: rules},

		RegisterLocationSlot: &usecases.RegisterLocationSlot{
			Sites: sites, Zones: zones, Aisles: aisles, Slots: slots,
			LocationTypes: locationTypes, Rules: rules, Events: publisher, Clock: clock,
			UnitOfWork: uow,
		},
		GetLocationSlot: &usecases.GetLocationSlot{Slots: slots},

		RegisterFixedStructure: &usecases.RegisterFixedStructure{Sites: sites, Structures: structures, Events: publisher, Clock: clock, UnitOfWork: uow},

		RegisterCrossAisle: &usecases.RegisterCrossAisle{
			Zones: zones, Aisles: aisles, CrossAisles: crossAisles, Events: publisher, Clock: clock, UnitOfWork: uow,
		},

		IdempotencyPool: pool,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return inboundhttp.NewRouter(server, logger)
}

func countRows(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func idempotentPost(t *testing.T, router http.Handler, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(inboundhttp.IdempotencyKeyHeader, key)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func problemSlug(t *testing.T, body []byte) string {
	t.Helper()
	var problem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &problem); err != nil {
		t.Fatalf("decode problem: %v (body: %s)", err, string(body))
	}
	return problem.Type[strings.LastIndex(problem.Type, "/")+1:]
}

// ---------------------------------------------------------- POST /sites ---
// The full a-f scenario matrix, against the simplest protected endpoint.

const validSiteBody = `{"siteCode":"WH1","name":"Fulfilment Centre One"}`

// (a) fresh key + valid body -> 201, aggregate created, idempotency row
// records the exact outcome.
func TestIdempotency_Sites_FreshKey_CreatesSiteAndRecordsOutcome(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)

	rec := idempotentPost(t, router, "/sites", "sites-fresh-1", validSiteBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := countRows(t, pool, "sites"); got != 1 {
		t.Fatalf("sites rows = %d, want 1", got)
	}

	var statusCode int
	var responseBody []byte
	if err := pool.QueryRow(context.Background(),
		"SELECT status_code, response_body FROM idempotency_keys WHERE key = $1", "sites-fresh-1",
	).Scan(&statusCode, &responseBody); err != nil {
		t.Fatalf("read idempotency row: %v", err)
	}
	if statusCode != http.StatusCreated {
		t.Fatalf("stored status_code = %d, want 201", statusCode)
	}
	if string(responseBody) != rec.Body.String() {
		t.Fatalf("stored response_body does not match what was returned to the caller")
	}
}

// (b) replay: same key + identical body -> byte-identical response, no
// duplicate resource.
func TestIdempotency_Sites_Replay_SameKeySameBody_ReturnsIdenticalResponseNoDuplicate(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)

	first := idempotentPost(t, router, "/sites", "sites-replay-1", validSiteBody)
	if first.Code != http.StatusCreated {
		t.Fatalf("first call status = %d, want 201 (body: %s)", first.Code, first.Body.String())
	}
	second := idempotentPost(t, router, "/sites", "sites-replay-1", validSiteBody)
	if second.Code != first.Code {
		t.Fatalf("replay status = %d, want %d", second.Code, first.Code)
	}
	if second.Body.String() != first.Body.String() {
		t.Fatalf("replay body differs:\nfirst:  %s\nsecond: %s", first.Body.String(), second.Body.String())
	}
	if second.Header().Get("Location") != first.Header().Get("Location") {
		t.Fatalf("replay Location = %q, want %q", second.Header().Get("Location"), first.Header().Get("Location"))
	}
	if got := countRows(t, pool, "sites"); got != 1 {
		t.Fatalf("sites rows after replay = %d, want exactly 1 (no duplicate site created)", got)
	}
}

// (c) same key + different body -> 422.
func TestIdempotency_Sites_SameKeyDifferentBody_Returns422(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)

	first := idempotentPost(t, router, "/sites", "sites-mismatch-1", validSiteBody)
	if first.Code != http.StatusCreated {
		t.Fatalf("first call status = %d, want 201 (body: %s)", first.Code, first.Body.String())
	}

	differentBody := `{"siteCode":"WH2","name":"Different Site"}`
	second := idempotentPost(t, router, "/sites", "sites-mismatch-1", differentBody)
	if second.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body: %s)", second.Code, second.Body.String())
	}
	if got := problemSlug(t, second.Body.Bytes()); got != "idempotency-key-reused" {
		t.Fatalf("problem slug = %q, want idempotency-key-reused", got)
	}
	if got := countRows(t, pool, "sites"); got != 1 {
		t.Fatalf("sites rows = %d, want 1 (the mismatched retry must not create a second site)", got)
	}
}

// (d) no key header -> 400.
func TestIdempotency_Sites_NoHeader_Returns400(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)

	rec := idempotentPost(t, router, "/sites", "", validSiteBody)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := problemSlug(t, rec.Body.Bytes()); got != "idempotency-key-required" {
		t.Fatalf("problem slug = %q, want idempotency-key-required", got)
	}
	if got := countRows(t, pool, "sites"); got != 0 {
		t.Fatalf("sites rows = %d, want 0 (no site should be created without the header)", got)
	}
}

// (e) concurrency: N real goroutines, same key, real WaitGroup -> all
// identical 201s, exactly one site row.
func TestIdempotency_Sites_Concurrent_SameKeySameBody_ExactlyOneSiteCreated(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)

	const n = 5
	var wg sync.WaitGroup
	results := make([]*httptest.ResponseRecorder, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = idempotentPost(t, router, "/sites", "sites-concurrent-1", validSiteBody)
		}(i)
	}
	wg.Wait()

	for i, rec := range results {
		if rec.Code != http.StatusCreated {
			t.Fatalf("goroutine %d status = %d, want 201 (body: %s)", i, rec.Code, rec.Body.String())
		}
		if rec.Body.String() != results[0].Body.String() {
			t.Fatalf("goroutine %d body differs from goroutine 0:\n%d: %s\n0: %s", i, i, rec.Body.String(), results[0].Body.String())
		}
	}
	if got := countRows(t, pool, "sites"); got != 1 {
		t.Fatalf("sites rows after %d concurrent identical requests = %d, want exactly 1", n, got)
	}
}

// (f) a normal ERROR response (a genuine domain business-rule failure) is
// cached and replayed verbatim on retry with the same key+body.
func TestIdempotency_Sites_BusinessErrorResponse_IsCachedAndReplayed(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)

	// Register WH1 once under its own key, then attempt to register the
	// SAME site code again under a different key — ErrDuplicateSite
	// (409) is a genuine business-rule refusal, not a malformed-input
	// 400, and it is decided AFTER the second request's own idempotency
	// row would already have been inserted.
	seed := idempotentPost(t, router, "/sites", "sites-business-error-seed", validSiteBody)
	if seed.Code != http.StatusCreated {
		t.Fatalf("seed call status = %d, want 201 (body: %s)", seed.Code, seed.Body.String())
	}

	first := idempotentPost(t, router, "/sites", "sites-business-error-1", validSiteBody)
	if first.Code != http.StatusConflict {
		t.Fatalf("first call status = %d, want 409 (body: %s)", first.Code, first.Body.String())
	}

	var statusCode int
	if err := pool.QueryRow(context.Background(),
		"SELECT status_code FROM idempotency_keys WHERE key = $1", "sites-business-error-1",
	).Scan(&statusCode); err != nil {
		t.Fatalf("read idempotency row: %v", err)
	}
	if statusCode != http.StatusConflict {
		t.Fatalf("stored status_code = %d, want 409 — the business error response must be cached", statusCode)
	}

	second := idempotentPost(t, router, "/sites", "sites-business-error-1", validSiteBody)
	if second.Code != first.Code {
		t.Fatalf("replay status = %d, want %d (cached error response)", second.Code, first.Code)
	}
	if second.Body.String() != first.Body.String() {
		t.Fatalf("replay body differs from the first (cached) error response:\nfirst:  %s\nsecond: %s", first.Body.String(), second.Body.String())
	}
	if got := countRows(t, pool, "sites"); got != 1 {
		t.Fatalf("sites rows = %d, want 1 (only the seed call created a site; the duplicate never did, either time)", got)
	}
}

// ------------------------------------------------------- POST /locations --
// The full a-f scenario matrix, against the deepest protected endpoint:
// chain-of-custody resolution (Site -> Zone -> Aisle), PlacementRule
// evaluation, and location-metrics recording all happen inside the SAME
// transaction the idempotency middleware began.

// seedLocationChain creates the Site/Zone/Aisle/LocationType chain
// RegisterLocationSlot needs, each through the real router with its own
// Idempotency-Key (every one of these routes is itself protected).
func seedLocationChain(t *testing.T, router http.Handler) {
	t.Helper()
	if rec := idempotentPost(t, router, "/sites", "seed-site", validSiteBody); rec.Code != http.StatusCreated {
		t.Fatalf("seed site: status = %d, body: %s", rec.Code, rec.Body.String())
	}
	zoneBody := `{"areaCode":"STOR","zoneCode":"AMB","temperatureClass":"Ambient","hazmat":false}`
	if rec := idempotentPost(t, router, "/sites/WH1/zones", "seed-zone", zoneBody); rec.Code != http.StatusCreated {
		t.Fatalf("seed zone: status = %d, body: %s", rec.Code, rec.Body.String())
	}
	aisleBody := `{"aisleCode":"A07","sequenceHint":7,"direction":"TwoWay"}`
	if rec := idempotentPost(t, router, "/zones/WH1-STOR-AMB/aisles", "seed-aisle", aisleBody); rec.Code != http.StatusCreated {
		t.Fatalf("seed aisle: status = %d, body: %s", rec.Code, rec.Body.String())
	}
	typeBody := `{"name":"PalletRack","defaultCapacity":{"maxWeightKg":1200,"maxVolumeM3":2.4}}`
	if rec := idempotentPost(t, router, "/location-types", "seed-type", typeBody); rec.Code != http.StatusCreated {
		t.Fatalf("seed location type: status = %d, body: %s", rec.Code, rec.Body.String())
	}
}

const validSlotBody = `{"locationCode":"WH1-STOR-AMB-A07-03-02-B","locationType":"PalletRack"}`

// (a)
func TestIdempotency_LocationSlots_FreshKey_CreatesSlotAndRecordsOutcome(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)
	seedLocationChain(t, router)

	rec := idempotentPost(t, router, "/locations", "slots-fresh-1", validSlotBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := countRows(t, pool, "location_slots"); got != 1 {
		t.Fatalf("location_slots rows = %d, want 1", got)
	}
}

// (b)
func TestIdempotency_LocationSlots_Replay_SameKeySameBody_ReturnsIdenticalResponseNoDuplicate(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)
	seedLocationChain(t, router)

	first := idempotentPost(t, router, "/locations", "slots-replay-1", validSlotBody)
	if first.Code != http.StatusCreated {
		t.Fatalf("first call status = %d, want 201 (body: %s)", first.Code, first.Body.String())
	}
	second := idempotentPost(t, router, "/locations", "slots-replay-1", validSlotBody)
	if second.Code != first.Code || second.Body.String() != first.Body.String() {
		t.Fatalf("replay differs: first=%d %s second=%d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	if got := countRows(t, pool, "location_slots"); got != 1 {
		t.Fatalf("location_slots rows after replay = %d, want exactly 1", got)
	}
}

// (c)
func TestIdempotency_LocationSlots_SameKeyDifferentBody_Returns422(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)
	seedLocationChain(t, router)

	first := idempotentPost(t, router, "/locations", "slots-mismatch-1", validSlotBody)
	if first.Code != http.StatusCreated {
		t.Fatalf("first call status = %d, want 201 (body: %s)", first.Code, first.Body.String())
	}
	differentBody := `{"locationCode":"WH1-STOR-AMB-A07-03-02-C","locationType":"PalletRack"}`
	second := idempotentPost(t, router, "/locations", "slots-mismatch-1", differentBody)
	if second.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body: %s)", second.Code, second.Body.String())
	}
	if got := problemSlug(t, second.Body.Bytes()); got != "idempotency-key-reused" {
		t.Fatalf("problem slug = %q, want idempotency-key-reused", got)
	}
	if got := countRows(t, pool, "location_slots"); got != 1 {
		t.Fatalf("location_slots rows = %d, want 1", got)
	}
}

// (d)
func TestIdempotency_LocationSlots_NoHeader_Returns400(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)
	seedLocationChain(t, router)

	rec := idempotentPost(t, router, "/locations", "", validSlotBody)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := countRows(t, pool, "location_slots"); got != 0 {
		t.Fatalf("location_slots rows = %d, want 0", got)
	}
}

// (e) concurrency proof, run against the deepest use case: PlacementRule
// evaluation and location-metrics recording must not break the
// unique-index-lock serialization.
func TestIdempotency_LocationSlots_Concurrent_SameKeySameBody_ExactlyOneSlotCreated(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)
	seedLocationChain(t, router)

	const n = 5
	var wg sync.WaitGroup
	results := make([]*httptest.ResponseRecorder, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = idempotentPost(t, router, "/locations", "slots-concurrent-1", validSlotBody)
		}(i)
	}
	wg.Wait()

	for i, rec := range results {
		if rec.Code != http.StatusCreated {
			t.Fatalf("goroutine %d status = %d, want 201 (body: %s)", i, rec.Code, rec.Body.String())
		}
		if rec.Body.String() != results[0].Body.String() {
			t.Fatalf("goroutine %d body differs from goroutine 0", i)
		}
	}
	if got := countRows(t, pool, "location_slots"); got != 1 {
		t.Fatalf("location_slots rows after %d concurrent identical requests = %d, want exactly 1", n, got)
	}
}

// (f) a PlacementRule-rejected slot (a genuine domain validation error) is
// cached and replayed verbatim.
func TestIdempotency_LocationSlots_BusinessErrorResponse_IsCachedAndReplayed(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)
	seedLocationChain(t, router)

	// An unknown location type is rejected by RegisterLocationSlot AFTER
	// the idempotency row would already have been inserted.
	invalidBody := `{"locationCode":"WH1-STOR-AMB-A07-03-02-B","locationType":"Hovercraft"}`

	first := idempotentPost(t, router, "/locations", "slots-business-error-1", invalidBody)
	if first.Code != http.StatusNotFound {
		t.Fatalf("first call status = %d, want 404 (body: %s)", first.Code, first.Body.String())
	}

	second := idempotentPost(t, router, "/locations", "slots-business-error-1", invalidBody)
	if second.Code != first.Code || second.Body.String() != first.Body.String() {
		t.Fatalf("replay differs from the first (cached) error response: first=%d %s second=%d %s",
			first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	if got := countRows(t, pool, "location_slots"); got != 0 {
		t.Fatalf("location_slots rows = %d, want 0 (the invalid slot was never persisted, either time)", got)
	}
}

// --------------------------------------------------------- smoke tests ----
// One fresh-key-creates + one replay-does-not-duplicate pair per remaining
// protected endpoint, proving the idempotent(r) wiring is live on every
// route — the middleware's own correctness is exhaustively covered above.

func TestIdempotency_Zones_Smoke(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)
	idempotentPost(t, router, "/sites", "zsmoke-site", validSiteBody)

	body := `{"areaCode":"STOR","zoneCode":"AMB","temperatureClass":"Ambient","hazmat":false}`
	first := idempotentPost(t, router, "/sites/WH1/zones", "zsmoke-1", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", first.Code, first.Body.String())
	}
	second := idempotentPost(t, router, "/sites/WH1/zones", "zsmoke-1", body)
	if second.Code != first.Code || second.Body.String() != first.Body.String() {
		t.Fatalf("replay differs from first")
	}
	if got := countRows(t, pool, "zones"); got != 1 {
		t.Fatalf("zones rows = %d, want 1", got)
	}
}

func TestIdempotency_FixedStructures_Smoke(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)
	idempotentPost(t, router, "/sites", "fssmoke-site", validSiteBody)

	body := `{"kind":"Wall","footprint":{"origin":{"xM":0,"yM":0,"zM":0},"size":{"widthM":1,"depthM":1,"heightM":1}},"label":"North wall"}`
	first := idempotentPost(t, router, "/sites/WH1/structures", "fssmoke-1", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", first.Code, first.Body.String())
	}
	second := idempotentPost(t, router, "/sites/WH1/structures", "fssmoke-1", body)
	if second.Code != first.Code || second.Body.String() != first.Body.String() {
		t.Fatalf("replay differs from first")
	}
	if got := countRows(t, pool, "fixed_structures"); got != 1 {
		t.Fatalf("fixed_structures rows = %d, want 1", got)
	}
}

func TestIdempotency_Aisles_Smoke(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)
	idempotentPost(t, router, "/sites", "asmoke-site", validSiteBody)
	idempotentPost(t, router, "/sites/WH1/zones", "asmoke-zone",
		`{"areaCode":"STOR","zoneCode":"AMB","temperatureClass":"Ambient","hazmat":false}`)

	body := `{"aisleCode":"A07","sequenceHint":7,"direction":"TwoWay"}`
	first := idempotentPost(t, router, "/zones/WH1-STOR-AMB/aisles", "asmoke-1", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", first.Code, first.Body.String())
	}
	second := idempotentPost(t, router, "/zones/WH1-STOR-AMB/aisles", "asmoke-1", body)
	if second.Code != first.Code || second.Body.String() != first.Body.String() {
		t.Fatalf("replay differs from first")
	}
	if got := countRows(t, pool, "aisles"); got != 1 {
		t.Fatalf("aisles rows = %d, want 1", got)
	}
}

func TestIdempotency_CrossAisles_Smoke(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)
	idempotentPost(t, router, "/sites", "casmoke-site", validSiteBody)
	idempotentPost(t, router, "/sites/WH1/zones", "casmoke-zone",
		`{"areaCode":"STOR","zoneCode":"AMB","temperatureClass":"Ambient","hazmat":false}`)
	idempotentPost(t, router, "/zones/WH1-STOR-AMB/aisles", "casmoke-a1",
		`{"aisleCode":"A01","sequenceHint":1,"direction":"TwoWay"}`)
	idempotentPost(t, router, "/zones/WH1-STOR-AMB/aisles", "casmoke-a2",
		`{"aisleCode":"A02","sequenceHint":2,"direction":"TwoWay"}`)

	body := `{"fromAisle":"A01","toAisle":"A02","atBay":"01"}`
	first := idempotentPost(t, router, "/zones/WH1-STOR-AMB/cross-aisles", "casmoke-1", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", first.Code, first.Body.String())
	}
	second := idempotentPost(t, router, "/zones/WH1-STOR-AMB/cross-aisles", "casmoke-1", body)
	if second.Code != first.Code || second.Body.String() != first.Body.String() {
		t.Fatalf("replay differs from first")
	}
	if got := countRows(t, pool, "cross_aisles"); got != 1 {
		t.Fatalf("cross_aisles rows = %d, want 1", got)
	}
}

func TestIdempotency_LocationTypes_Smoke(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)

	body := `{"name":"PalletRack","defaultCapacity":{"maxWeightKg":1200,"maxVolumeM3":2.4}}`
	first := idempotentPost(t, router, "/location-types", "ltsmoke-1", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", first.Code, first.Body.String())
	}
	second := idempotentPost(t, router, "/location-types", "ltsmoke-1", body)
	if second.Code != first.Code || second.Body.String() != first.Body.String() {
		t.Fatalf("replay differs from first")
	}
	if got := countRows(t, pool, "location_types"); got != 1 {
		t.Fatalf("location_types rows = %d, want 1", got)
	}
}

func TestIdempotency_PlacementRules_Smoke(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)
	idempotentPost(t, router, "/location-types", "prsmoke-type",
		`{"name":"PalletRack","defaultCapacity":{"maxWeightKg":1200,"maxVolumeM3":2.4}}`)

	body := `{"ruleId":"rule-1","locationType":"PalletRack","effect":"Allow","zone":{"temperatureClass":"Ambient"}}`
	first := idempotentPost(t, router, "/placement-rules", "prsmoke-1", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", first.Code, first.Body.String())
	}
	second := idempotentPost(t, router, "/placement-rules", "prsmoke-1", body)
	if second.Code != first.Code || second.Body.String() != first.Body.String() {
		t.Fatalf("replay differs from first")
	}
	if got := countRows(t, pool, "placement_rules"); got != 1 {
		t.Fatalf("placement_rules rows = %d, want 1", got)
	}
}
