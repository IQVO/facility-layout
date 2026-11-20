//go:build integration

// A RETRYABLE refusal is not stored (the fleet rule
// .claude/rules/fleet/idempotency-and-outbox.md, docs/docs/adr/0046): nothing
// committed and the answer depends on state that can change, so the key row is
// released and a retry under the same key executes again. Through the REAL
// router over a real Postgres: a genuine lost race (concurrent-modification)
// and a referenced fact that is not known yet (site-not-found).
package http_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/facility-layout/internal/adapters/inbound/http"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// gatedPublisher parks the first Publish after arm() until freed is closed: the
// request that owns it has already saved its aggregate inside its (still open)
// transaction, so a second request for the same code blocks on the row.
type gatedPublisher struct {
	next    ports.EventPublisher
	mu      sync.Mutex
	armed   bool
	arrived chan struct{}
	freed   chan struct{}
}

func newGatedPublisher(next ports.EventPublisher) *gatedPublisher {
	return &gatedPublisher{next: next, arrived: make(chan struct{}), freed: make(chan struct{})}
}

func (g *gatedPublisher) arm() {
	g.mu.Lock()
	g.armed = true
	g.mu.Unlock()
}

func (g *gatedPublisher) Publish(ctx context.Context, event shared.DomainEvent) error {
	g.mu.Lock()
	parked := g.armed
	g.armed = false
	g.mu.Unlock()
	if parked {
		close(g.arrived)
		<-g.freed
	}
	return g.next.Publish(ctx, event)
}

// keyRows is how many rows idempotency_keys holds for key.
func keyRows(t *testing.T, pool *pgxpool.Pool, key string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("count key rows: %v", err)
	}
	return n
}

func replayed(rec *httptest.ResponseRecorder) bool {
	return rec.Header().Get(inboundhttp.IdempotencyReplayedHeader) == "true"
}

// waitForLockWaiter blocks until a backend waits on a row lock: the second
// request is now parked on the first one's uncommitted row.
func waitForLockWaiter(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND datname = current_database()`).Scan(&n); err != nil {
			t.Fatalf("read pg_stat_activity: %v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("no request ever waited on the other's row: the race was not staged")
}

// A genuine lost race. Request A registers site WH1 and is parked after its
// Save, before its commit; request B (another key, same code) passes the
// existence check (A's row is not visible yet), blocks on A's row, and once A
// commits loses the version guard: 409 concurrent-modification. That refusal
// is RETRYABLE: B's key row is gone, and B's retry under the SAME key executes
// (now it finds the site: duplicate-site-code), and only that answer is stored.
func TestReplayClass_ConcurrentModificationLoserIsReleasedAndItsRetryExecutes(t *testing.T) {
	pool := idempotencyDB(t)
	gate := newGatedPublisher(events.NewLogPublisher(slog.New(slog.NewTextHandler(io.Discard, nil))))
	router := newIdempotencyRouterPublishing(t, pool, gate)

	gate.arm()
	a := make(chan *httptest.ResponseRecorder, 1)
	go func() { a <- idempotentPost(t, router, "/sites", "race-a", validSiteBody) }()
	<-gate.arrived

	b := make(chan *httptest.ResponseRecorder, 1)
	go func() { b <- idempotentPost(t, router, "/sites", "race-b", validSiteBody) }()
	waitForLockWaiter(t, pool)
	close(gate.freed)

	winner, loser := <-a, <-b
	if winner.Code != http.StatusCreated {
		t.Fatalf("winner = %d %s, want 201", winner.Code, winner.Body.String())
	}
	if loser.Code != http.StatusConflict || problemSlug(t, loser.Body.Bytes()) != "concurrent-modification" {
		t.Fatalf("loser = %d %s, want 409 concurrent-modification", loser.Code, loser.Body.String())
	}
	if n := keyRows(t, pool, "race-b"); n != 0 {
		t.Fatalf("the loser's key row exists (%d): a RETRYABLE refusal must release the key", n)
	}
	if n := keyRows(t, pool, "race-a"); n != 1 {
		t.Fatalf("the winner's key rows = %d, want 1", n)
	}

	retry := idempotentPost(t, router, "/sites", "race-b", validSiteBody)
	if replayed(retry) {
		t.Fatalf("the retry was replayed from the store; it must execute again (%d %s)", retry.Code, retry.Body.String())
	}
	if retry.Code != http.StatusConflict || problemSlug(t, retry.Body.Bytes()) != "duplicate-site-code" {
		t.Fatalf("retry = %d %s, want 409 duplicate-site-code (the executed answer)", retry.Code, retry.Body.String())
	}
	if n := keyRows(t, pool, "race-b"); n != 1 {
		t.Fatalf("after the executed retry the key rows = %d, want 1 (a FINAL answer is stored)", n)
	}

	again := idempotentPost(t, router, "/sites", "race-b", validSiteBody)
	if !replayed(again) || again.Code != retry.Code || again.Body.String() != retry.Body.String() {
		t.Fatalf("third call = %d replayed=%v %s, want the stored duplicate-site-code replayed", again.Code, replayed(again), again.Body.String())
	}
	if got := countRows(t, pool, "sites"); got != 1 {
		t.Fatalf("sites rows = %d, want 1", got)
	}
}

// A referenced fact that is not known yet. POST /sites/WH1/zones before WH1
// exists answers 404 site-not-found; another request registers WH1; the retry
// under the same key must run and create the zone, then be stored and replayed.
func TestReplayClass_SiteNotFoundIsReleasedSoTheRetryFindsItOnceRegistered(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)
	zone := `{"areaCode":"STOR","zoneCode":"AMB","temperatureClass":"Ambient","hazmat":false}`

	first := idempotentPost(t, router, "/sites/WH1/zones", "late-zone", zone)
	if first.Code != http.StatusNotFound || problemSlug(t, first.Body.Bytes()) != "site-not-found" {
		t.Fatalf("first = %d %s, want 404 site-not-found", first.Code, first.Body.String())
	}
	if n := keyRows(t, pool, "late-zone"); n != 0 {
		t.Fatalf("key rows after the 404 = %d, want 0 (released)", n)
	}

	if seed := idempotentPost(t, router, "/sites", "late-zone-site", validSiteBody); seed.Code != http.StatusCreated {
		t.Fatalf("seed site = %d %s, want 201", seed.Code, seed.Body.String())
	}

	retry := idempotentPost(t, router, "/sites/WH1/zones", "late-zone", zone)
	if retry.Code != http.StatusCreated || replayed(retry) {
		t.Fatalf("retry = %d replayed=%v %s, want a fresh 201", retry.Code, replayed(retry), retry.Body.String())
	}
	again := idempotentPost(t, router, "/sites/WH1/zones", "late-zone", zone)
	if again.Code != http.StatusCreated || !replayed(again) || again.Body.String() != retry.Body.String() {
		t.Fatalf("third call = %d replayed=%v, want the stored 201 replayed", again.Code, replayed(again))
	}
	if got := countRows(t, pool, "zones"); got != 1 {
		t.Fatalf("zones rows = %d, want 1", got)
	}
}
