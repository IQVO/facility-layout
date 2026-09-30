//go:build integration

// Integration tests proving MaxConns and StatementTimeout are actually
// applied to every pool connection — against a real Postgres 16
// (testcontainers), not a mock or a pg_settings assumption. Gated behind
// the `integration` build tag, mirroring every other Postgres
// integration test in this package.
package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
)

// poolLimitsDB boots a throwaway Postgres (own container, never an
// external DATABASE_URL) for tests that only need a bare connection — no
// migrations, since these tests exercise pool-level settings, not
// schema.
func poolLimitsDB(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("facility"),
		tcpostgres.WithUsername("facility"),
		tcpostgres.WithPassword("facility"),
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
	return url
}

// TestNewPool_AppliesStatementTimeoutToNewConnections is the core claim:
// every connection opened by the pool has statement_timeout set to the
// configured value, verified two ways — reading pg_settings back, AND
// triggering a real slow query and confirming Postgres actually cancels
// it at the configured timeout (not just that the setting round-trips).
func TestNewPool_AppliesStatementTimeoutToNewConnections(t *testing.T) {
	url := poolLimitsDB(t)
	ctx := context.Background()

	// A short, deliberately test-only timeout (200ms) so this test
	// doesn't wait out the production 5s value while still genuinely
	// proving cancellation, not just that the GUC round-trips.
	pool, err := postgres.NewPoolWithLimits(ctx, url, 4, "200ms")
	if err != nil {
		t.Fatalf("NewPoolWithLimits: %v", err)
	}
	t.Cleanup(pool.Close)

	// 1. pg_settings reports the value we configured, on a freshly
	// acquired connection (not just the one AfterConnect ran on).
	var reported string
	if err := pool.QueryRow(ctx, "SHOW statement_timeout").Scan(&reported); err != nil {
		t.Fatalf("SHOW statement_timeout: %v", err)
	}
	if reported != "200ms" {
		t.Fatalf("statement_timeout = %q, want %q", reported, "200ms")
	}

	// 2. A query that runs longer than the timeout is actually
	// cancelled by Postgres, not merely configured-but-unenforced.
	_, err = pool.Exec(ctx, "SELECT pg_sleep(2)")
	if err == nil {
		t.Fatal("expected pg_sleep(2) to be cancelled by statement_timeout, got no error")
	}
	if !strings.Contains(err.Error(), "canceling statement due to statement timeout") &&
		!strings.Contains(err.Error(), "57014") { // Postgres SQLSTATE for query_canceled
		t.Fatalf("expected a statement_timeout cancellation error, got: %v", err)
	}

	// 3. The pool itself survives the cancelled query: a subsequent
	// query on the same pool succeeds normally (the connection was not
	// left in a broken state).
	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("pool unusable after a cancelled statement: %v", err)
	}
	if one != 1 {
		t.Fatalf("SELECT 1 = %d, want 1", one)
	}
}

// TestNewPool_AppliesMaxConns proves the pool actually enforces the
// configured connection ceiling: holding maxConns connections open
// simultaneously succeeds, but acquiring one more blocks until a slot is
// released (rather than silently opening an (maxConns+1)th connection).
func TestNewPool_AppliesMaxConns(t *testing.T) {
	url := poolLimitsDB(t)
	ctx := context.Background()

	const maxConns = 2
	pool, err := postgres.NewPoolWithLimits(ctx, url, maxConns, "30s")
	if err != nil {
		t.Fatalf("NewPoolWithLimits: %v", err)
	}
	t.Cleanup(pool.Close)

	// Acquire exactly maxConns connections and hold them.
	acquired := make([]*pgxpool.Conn, 0, maxConns)
	for i := 0; i < maxConns; i++ {
		c, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		acquired = append(acquired, c)
	}
	t.Cleanup(func() {
		for _, c := range acquired {
			c.Release()
		}
	})

	// A further acquire with a short deadline must NOT succeed while
	// every slot is held — proving the ceiling is real, not advisory.
	blockedCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if _, err := pool.Acquire(blockedCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected acquiring beyond MaxConns=%d to block until timeout, got err=%v", maxConns, err)
	}
}
