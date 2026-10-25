//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
)

// insertRawOutboxRow writes one unpublished outbox_events row directly, so the
// attempt-cap tests control event_type and key exactly.
func insertRawOutboxRow(t *testing.T, pool *pgxpool.Pool, eventType, key string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO outbox_events (topic, event_type, key, value)
		VALUES ('test.integration', $1, $2, '{"stub":true}')
		RETURNING id
	`, eventType, []byte(key)).Scan(&id)
	if err != nil {
		t.Fatalf("insert outbox row: %v", err)
	}
	return id
}

// outboxAttempts returns attempts, last_error and whether the row is published.
func outboxAttempts(t *testing.T, pool *pgxpool.Pool, id int64) (attempts int, lastErr string, published bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
		SELECT attempts, coalesce(last_error, ''), published_at IS NOT NULL
		FROM outbox_events WHERE id = $1
	`, id).Scan(&attempts, &lastErr, &published)
	if err != nil {
		t.Fatalf("read outbox row %d: %v", id, err)
	}
	return attempts, lastErr, published
}

// errorRecords decodes the JSON log lines in buf and returns the ERROR ones.
func errorRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	for dec.More() {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("decode log line: %v", err)
		}
		if rec["level"] == "ERROR" {
			out = append(out, rec)
		}
	}
	return out
}

// failingPasses runs n relay passes that must each fail on the poison row
// without publishing anything.
func failingPasses(t *testing.T, relay *postgres.OutboxRelay, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		published, err := relay.RelayOnce(context.Background())
		if err == nil || published != 0 {
			t.Fatalf("pass %d: want the poison row to fail with nothing published, got published=%d err=%v", i, published, err)
		}
	}
}

// assertCapRecord checks the single ERROR record names the excluded row.
func assertCapRecord(t *testing.T, recs []map[string]any, id int64) {
	t.Helper()
	if len(recs) != 1 {
		t.Fatalf("want exactly 1 ERROR record when the row hits the cap, got %d: %v", len(recs), recs)
	}
	rec := recs[0]
	if got, _ := rec["outbox_id"].(float64); int64(got) != id {
		t.Errorf("outbox_id = %v, want %d", rec["outbox_id"], id)
	}
	want := map[string]string{"topic": "test.integration", "event_type": "Poison", "key": "WH-CAP-1"}
	for field, v := range want {
		if rec[field] != v {
			t.Errorf("%s = %v, want %q", field, rec[field], v)
		}
	}
	if got, _ := rec["max_attempts"].(float64); int(got) != 3 {
		t.Errorf("max_attempts = %v, want 3", rec["max_attempts"])
	}
}

// TestOutboxRelay_MaxAttempts_PoisonRowExcludedThenGoodRowPublishes is gap fix
// D12 (ADR 0036): a poison row that fails every send blocks the relay only
// until it has failed maxAttempts times. The pass that records the last
// attempt logs it at ERROR with id, topic, type and key; from then on the row
// is excluded from claims (still in outbox_events with attempts and
// last_error for inspection) and the good row behind it, with the same key,
// publishes.
func TestOutboxRelay_MaxAttempts_PoisonRowExcludedThenGoodRowPublishes(t *testing.T) {
	pool := outboxDB(t)
	ctx := context.Background()
	poisonID := insertRawOutboxRow(t, pool, "Poison", "WH-CAP-1")
	goodID := insertRawOutboxRow(t, pool, "Good", "WH-CAP-1")

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sink := &recordingSink{failOn: "Poison", failErr: errors.New("message too large")}
	relay := postgres.NewOutboxRelay(pool, sink, logger, postgres.WithMaxAttempts(3))

	failingPasses(t, relay, 2)
	if recs := errorRecords(t, &logs); len(recs) != 0 {
		t.Fatalf("no ERROR record expected below the cap, got %v", recs)
	}
	failingPasses(t, relay, 1)
	assertCapRecord(t, errorRecords(t, &logs), poisonID)

	published, err := relay.RelayOnce(ctx)
	if err != nil || published != 1 {
		t.Fatalf("pass after the cap: want the good row published, got published=%d err=%v", published, err)
	}
	if len(sink.sent) != 1 || sink.sent[0].EventType != "Good" {
		t.Fatalf("sink got %+v, want only the Good row", sink.sent)
	}
	if _, _, ok := outboxAttempts(t, pool, goodID); !ok {
		t.Fatal("the good row is not marked published")
	}

	// The excluded row is never claimed again and stays inspectable.
	if published, err := relay.RelayOnce(ctx); err != nil || published != 0 {
		t.Fatalf("pass after drain: published=%d err=%v, want a no-op", published, err)
	}
	attempts, lastErr, ok := outboxAttempts(t, pool, poisonID)
	if ok || attempts != 3 || lastErr != "message too large" {
		t.Fatalf("poison row: attempts=%d last_error=%q published=%v, want 3, the send error, unpublished", attempts, lastErr, ok)
	}
}

// TestOutboxRelay_MaxAttempts_ReArmedRowIsClaimedAgain proves the runbook's
// re-arm SQL: resetting attempts to 0 makes an excluded row claimable again.
func TestOutboxRelay_MaxAttempts_ReArmedRowIsClaimedAgain(t *testing.T) {
	pool := outboxDB(t)
	ctx := context.Background()
	poisonID := insertRawOutboxRow(t, pool, "Poison", "WH-CAP-2")

	sink := &recordingSink{failOn: "Poison", failErr: errors.New("broker rejected")}
	relay := postgres.NewOutboxRelay(pool, sink, slog.Default(), postgres.WithMaxAttempts(1))
	failingPasses(t, relay, 1)
	if published, err := relay.RelayOnce(ctx); err != nil || published != 0 {
		t.Fatalf("capped row was claimed again: published=%d err=%v", published, err)
	}

	if _, err := pool.Exec(ctx, `UPDATE outbox_events SET attempts = 0 WHERE id = $1`, poisonID); err != nil {
		t.Fatalf("re-arm: %v", err)
	}
	sink.failOn = ""
	if published, err := relay.RelayOnce(ctx); err != nil || published != 1 {
		t.Fatalf("re-armed row: published=%d err=%v, want it published", published, err)
	}
}

// TestOutboxRelay_NoCap_PoisonRowBlocksLaterRows pins the semantics of
// WithMaxAttempts(0) (OUTBOX_MAX_ATTEMPTS=0, unlimited): the poison row is
// retried forever and the row behind it never publishes. This is why every
// cmd/* relay must pass a cap (architecture fitness test).
func TestOutboxRelay_NoCap_PoisonRowBlocksLaterRows(t *testing.T) {
	pool := outboxDB(t)
	insertRawOutboxRow(t, pool, "Poison", "WH-CAP-3")
	goodID := insertRawOutboxRow(t, pool, "Good", "WH-CAP-3")

	sink := &recordingSink{failOn: "Poison", failErr: errors.New("message too large")}
	relay := postgres.NewOutboxRelay(pool, sink, slog.Default(), postgres.WithMaxAttempts(0))
	failingPasses(t, relay, 5)
	if _, _, ok := outboxAttempts(t, pool, goodID); ok || len(sink.sent) != 0 {
		t.Fatalf("with no cap the good row must stay blocked, published=%v sent=%d", ok, len(sink.sent))
	}
}
