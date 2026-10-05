//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
)

func seedIdempotencyKey(t *testing.T, pool *pgxpool.Pool, key string, age time.Duration) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO idempotency_keys (key, method, path, request_hash, status_code, created_at)
		VALUES ($1, 'POST', '/sites', 'h', 201, now() - ($2 * interval '1 second'))`,
		key, age.Seconds())
	if err != nil {
		t.Fatalf("seed idempotency key %s: %v", key, err)
	}
}

// seedOutboxRow inserts one outbox row aged `age`; published rows get
// published_at = created_at (they were relayed immediately).
func seedOutboxRow(t *testing.T, pool *pgxpool.Pool, eventType string, age time.Duration, published bool) {
	t.Helper()
	pub := "NULL"
	if published {
		pub = "now() - ($2 * interval '1 second')"
	}
	_, err := pool.Exec(context.Background(), fmt.Sprintf(`
		INSERT INTO outbox_events (topic, event_type, value, created_at, published_at)
		VALUES ('t', $1, '\x7b7d', now() - ($2 * interval '1 second'), %s)`, pub),
		eventType, age.Seconds())
	if err != nil {
		t.Fatalf("seed outbox row %s: %v", eventType, err)
	}
}

func countIdempotency(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM idempotency_keys`).Scan(&n); err != nil {
		t.Fatalf("count idempotency keys: %v", err)
	}
	return n
}

// TestSweeper_DeletesOnlyExpiredIdempotencyKeysAndPublishedOutboxRows is the
// housekeeping contract (ADR-0026) against a real Postgres: expired
// idempotency keys and PUBLISHED outbox rows past retention go; fresh keys,
// recent published rows and — critically — UNPUBLISHED rows of any age stay.
func TestSweeper_DeletesOnlyExpiredIdempotencyKeysAndPublishedOutboxRows(t *testing.T) {
	pool := outboxDB(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		seedIdempotencyKey(t, pool, fmt.Sprintf("old-%d", i), 48*time.Hour)
	}
	seedIdempotencyKey(t, pool, "fresh-1", time.Hour)
	seedIdempotencyKey(t, pool, "fresh-2", 0)

	seedOutboxRow(t, pool, "OldPublished", 10*24*time.Hour, true)
	seedOutboxRow(t, pool, "OldPublished", 9*24*time.Hour, true)
	seedOutboxRow(t, pool, "RecentPublished", 24*time.Hour, true)
	seedOutboxRow(t, pool, "OldUnpublished", 30*24*time.Hour, false)
	seedOutboxRow(t, pool, "FreshUnpublished", 0, false)

	// Batch size 2 forces the multi-batch loop over the 5 old keys.
	s := postgres.NewSweeper(pool, postgres.WithSweepBatchSize(2))
	res, err := s.SweepOnce(ctx)
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if res.IdempotencyKeys != 5 {
		t.Errorf("deleted idempotency keys = %d, want 5", res.IdempotencyKeys)
	}
	if res.OutboxEvents != 2 {
		t.Errorf("deleted outbox rows = %d, want 2", res.OutboxEvents)
	}

	if got := countIdempotency(t, pool); got != 2 {
		t.Errorf("idempotency keys left = %d, want the 2 fresh ones", got)
	}
	if got := countOutbox(t, pool, "event_type = 'OldPublished'"); got != 0 {
		t.Errorf("old published outbox rows left = %d, want 0", got)
	}
	for _, kept := range []string{"RecentPublished", "OldUnpublished", "FreshUnpublished"} {
		if got := countOutbox(t, pool, "event_type = '"+kept+"'"); got != 1 {
			t.Errorf("%s rows left = %d, want 1 (must not be swept)", kept, got)
		}
	}

	// A second pass over a clean table is a no-op.
	res, err = s.SweepOnce(ctx)
	if err != nil || res != (postgres.SweepResult{}) {
		t.Errorf("second SweepOnce = %+v, %v; want zero result, nil", res, err)
	}
}

// A TTL/retention of 0 disables that half of the sweep entirely.
func TestSweeper_ZeroTTLAndRetentionKeepEverything(t *testing.T) {
	pool := outboxDB(t)

	seedIdempotencyKey(t, pool, "ancient", 365*24*time.Hour)
	seedOutboxRow(t, pool, "AncientPublished", 365*24*time.Hour, true)

	s := postgres.NewSweeper(pool, postgres.WithIdempotencyKeyTTL(0), postgres.WithOutboxRetention(0))
	res, err := s.SweepOnce(context.Background())
	if err != nil || res != (postgres.SweepResult{}) {
		t.Fatalf("SweepOnce = %+v, %v; want zero result, nil", res, err)
	}
	if got := countIdempotency(t, pool); got != 1 {
		t.Errorf("idempotency keys = %d, want 1 (TTL disabled)", got)
	}
	if got := countOutbox(t, pool, "TRUE"); got != 1 {
		t.Errorf("outbox rows = %d, want 1 (retention disabled)", got)
	}
}

// Run sweeps immediately on start, then stops cleanly on cancel.
func TestSweeper_RunSweepsOnStartAndStopsOnCancel(t *testing.T) {
	pool := outboxDB(t)
	seedIdempotencyKey(t, pool, "stale", 48*time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	s := postgres.NewSweeper(pool, postgres.WithSweepInterval(50*time.Millisecond))
	go func() { done <- s.Run(ctx) }()

	deadline := time.Now().Add(20 * time.Second)
	for countIdempotency(t, pool) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("Run never swept the stale idempotency key")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil on cancel", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}
