//go:build integration

// The repo's half of the fleet replay-class test (the verbatim template is
// idempotency_replay_classes_integration_test.go, rendered from
// warehouse-harness-template templates/idempotency/replay_classes_test.go.tmpl
// at its origin/develop). This is the ONLY file of the pair that knows the
// store: it boots ONE Postgres for the whole package run (the template runs
// one adapter per problem type, ~100 of them), truncates the idempotency table
// and a scratch probe table per adapter, and hands the template six closures.
package http_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	inboundhttp "github.com/claudioed/facility-layout/internal/adapters/inbound/http"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/pgtx"
)

var (
	replayPoolOnce sync.Once
	replayPool     *pgxpool.Pool
	replayPoolErr  error
)

// sharedReplayPool boots the package's one replay-class Postgres (migrations
// applied, plus the scratch table the template's Write targets) and returns it
// emptied. The container is reaped by testcontainers' Ryuk at process end.
func sharedReplayPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	replayPoolOnce.Do(func() {
		ctx := context.Background()
		container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("facility_layout"),
			tcpostgres.WithUsername("facility_layout"),
			tcpostgres.WithPassword("facility_layout"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err != nil {
			replayPoolErr = err
			return
		}
		url, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			replayPoolErr = err
			return
		}
		if replayPoolErr = postgres.RunMigrations(url, "../../../../migrations"); replayPoolErr != nil {
			return
		}
		if replayPool, replayPoolErr = postgres.NewPool(ctx, url); replayPoolErr != nil {
			return
		}
		_, replayPoolErr = replayPool.Exec(ctx, `CREATE TABLE IF NOT EXISTS replay_probe (id BIGSERIAL PRIMARY KEY, tag TEXT NOT NULL)`)
	})
	if replayPoolErr != nil {
		t.Fatalf("boot the replay-class postgres: %v", replayPoolErr)
	}
	if _, err := replayPool.Exec(context.Background(), `TRUNCATE idempotency_keys, replay_probe`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return replayPool
}

// newReplayOps returns the six operations over ONE clean store per call.
func newReplayOps(t *testing.T) replayOps {
	t.Helper()
	pool := sharedReplayPool(t)
	count := func(t *testing.T, query string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	return replayOps{
		Catalogue: func() []problemEntry {
			var out []problemEntry
			for _, p := range inboundhttp.ProblemTypesForTest() {
				out = append(out, problemEntry{Slug: p.Slug, Status: p.Status, Class: p.Class})
			}
			return out
		},
		Middleware: func(_ *testing.T, next http.Handler) http.Handler {
			return inboundhttp.RequireIdempotencyKey(pool)(next)
		},
		Refuse: func(w http.ResponseWriter, r *http.Request, e problemEntry) {
			inboundhttp.RefuseForTest(w, r, e.Slug, e.Status)
		},
		Write: func(ctx context.Context, tag string) error {
			tx, ok := pgtx.TxFrom(ctx)
			if !ok {
				return errors.New("the middleware bound no transaction into the request context")
			}
			_, err := tx.Exec(ctx, `INSERT INTO replay_probe (tag) VALUES ($1)`, tag)
			return err
		},
		Committed: func(t *testing.T, tag string) int {
			return count(t, `SELECT count(*) FROM replay_probe WHERE tag = $1`, tag)
		},
		KeyRows: func(t *testing.T, key string) int {
			return count(t, `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key)
		},
	}
}
