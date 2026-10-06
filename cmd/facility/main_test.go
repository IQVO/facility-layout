package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
)

// TestMigrationsDatabaseURLFallback proves the fallback wiring
// publisherConfigFromEnv applies: getenv("MIGRATIONS_DATABASE_URL",
// databaseURL) must return MIGRATIONS_DATABASE_URL's own value when it is
// set, and databaseURL itself (DATABASE_URL) when it is unset. This is the
// exact env-lookup line the fix for the PgBouncer/pg_advisory_lock
// incompatibility (ADR 0023-migrations-direct-postgres-connection.md,
// mirroring order-management's ADR-0029) depends on: any environment that
// doesn't provision the split (local dev, CI integration tests, a cluster
// whose Terraform predates this fix) must keep working exactly as before,
// using DATABASE_URL for everything including migrations.
func TestMigrationsDatabaseURLFallback(t *testing.T) {
	const databaseURL = "postgres://u:***@pgbouncer.example:6432/facility_layout?sslmode=disable"

	t.Run("falls back to DATABASE_URL when MIGRATIONS_DATABASE_URL is unset", func(t *testing.T) {
		os.Unsetenv("MIGRATIONS_DATABASE_URL")

		got := getenv("MIGRATIONS_DATABASE_URL", databaseURL)
		if got != databaseURL {
			t.Fatalf("getenv fallback = %q, want the DATABASE_URL value %q", got, databaseURL)
		}
	})

	t.Run("uses MIGRATIONS_DATABASE_URL when set, not DATABASE_URL", func(t *testing.T) {
		const direct = "postgres://u:***@postgres-postgresql.example:5432/facility_layout?sslmode=disable"
		t.Setenv("MIGRATIONS_DATABASE_URL", direct)

		got := getenv("MIGRATIONS_DATABASE_URL", databaseURL)
		if got != direct {
			t.Fatalf("getenv = %q, want the direct MIGRATIONS_DATABASE_URL value %q (must NOT silently keep using DATABASE_URL/PgBouncer)", got, direct)
		}
		if got == databaseURL {
			t.Fatal("MIGRATIONS_DATABASE_URL and DATABASE_URL collapsed to the same value — the whole point of this env var is that it differs")
		}
	})
}

// TestPublisherConfigFromEnv_ThreadsMigrationsDatabaseURL proves
// publisherConfigFromEnv itself — not just the getenv helper above —
// populates publisherConfig.migrationsDatabaseURL with the fallback value,
// so dialPostgres (which reads cfg.migrationsDatabaseURL, never
// cfg.databaseURL, for the migration step) actually receives it.
func TestPublisherConfigFromEnv_ThreadsMigrationsDatabaseURL(t *testing.T) {
	const databaseURL = "postgres://u:***@pgbouncer.example:6432/facility_layout?sslmode=disable"

	t.Run("falls back to DATABASE_URL", func(t *testing.T) {
		t.Setenv("DATABASE_URL", databaseURL)
		os.Unsetenv("MIGRATIONS_DATABASE_URL")

		cfg := publisherConfigFromEnv()
		if cfg.databaseURL != databaseURL {
			t.Fatalf("cfg.databaseURL = %q, want %q", cfg.databaseURL, databaseURL)
		}
		if cfg.migrationsDatabaseURL != databaseURL {
			t.Fatalf("cfg.migrationsDatabaseURL = %q, want the DATABASE_URL fallback %q", cfg.migrationsDatabaseURL, databaseURL)
		}
	})

	t.Run("uses MIGRATIONS_DATABASE_URL when set", func(t *testing.T) {
		const direct = "postgres://u:***@postgres-postgresql.example:5432/facility_layout?sslmode=disable"
		t.Setenv("DATABASE_URL", databaseURL)
		t.Setenv("MIGRATIONS_DATABASE_URL", direct)

		cfg := publisherConfigFromEnv()
		if cfg.databaseURL != databaseURL {
			t.Fatalf("cfg.databaseURL = %q, want %q (must stay on the pooled DSN)", cfg.databaseURL, databaseURL)
		}
		if cfg.migrationsDatabaseURL != direct {
			t.Fatalf("cfg.migrationsDatabaseURL = %q, want the direct DSN %q", cfg.migrationsDatabaseURL, direct)
		}
	})
}

// TestDialPostgres_UsesMigrationsDatabaseURLNotDatabaseURLForMigrations
// proves dialPostgres itself threads cfg.migrationsDatabaseURL into the
// migration step (not cfg.databaseURL), using a schemeless
// MIGRATIONS_DATABASE_URL to get a distinctive parse error that a
// dial/"connection refused" error against databaseURL could never produce.
// If dialPostgres ignored migrationsDatabaseURL and ran migrations against
// databaseURL instead, this test would see a dial/timeout error, not the
// parse error, since databaseURL points nothing is listening on.
func TestDialPostgres_UsesMigrationsDatabaseURLNotDatabaseURLForMigrations(t *testing.T) {
	const (
		bogusMigrationsURL = "not-a-valid-connection-string"
		unreachableAppURL  = "postgres://u:***@127.0.0.1:1/facility_layout?sslmode=disable&connect_timeout=1"
	)

	cfg := publisherConfig{
		databaseURL:           unreachableAppURL,
		migrationsDatabaseURL: bogusMigrationsURL,
		migrationsPath:        migrationsDirForTest(t),
	}

	_, err := dialPostgres(cfg, quietLogger())
	if err == nil {
		t.Fatal("a malformed MIGRATIONS_DATABASE_URL must fail boot")
	}
	if !strings.Contains(err.Error(), "parse scheme") {
		t.Fatalf("err = %v — expected the bogus-URL parse error from migrate.New; a \"connection refused\"/dial error here would mean migrations ran against databaseURL/unreachableAppURL instead of migrationsDatabaseURL", err)
	}
}

// migrationsDirForTest returns a directory with no migration files, which
// is enough for migrate.New to succeed opening the source (the failure
// this test cares about happens parsing the malformed database URL,
// before any migration file is read).
func migrationsDirForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

// quietLogger is a slog.Logger that discards everything, for tests that
// need to pass one in but don't assert on its output.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestStartBackground_TypedNilSweeperDoesNotPanic reproduces the contract
// job crash: with no DATABASE_URL the service runs in-memory and
// memoryAdapters returns a typed-nil *postgres.Sweeper. Boxing that into
// startBackground's interface parameter makes worker != nil, so the old
// guard let it through and worker.Run dereferenced a nil receiver,
// SIGSEGV-panicking the whole process ~5ms after boot (Schemathesis's
// in-memory service died before probing a single endpoint). The
// normalization inside startBackground must treat it as "no worker".
func TestStartBackground_TypedNilSweeperDoesNotPanic(t *testing.T) {
	logger := quietLogger()
	errCh := make(chan error, 1)
	var typedNilSweeper *postgres.Sweeper

	done := startBackground(context.Background(), logger, typedNilSweeper, "housekeeping sweeper running", errCh)

	select {
	case <-done:
		// done closed without launching the goroutine: correct.
	case <-time.After(5 * time.Second):
		t.Fatal("startBackground did not return a closed done channel for a typed-nil sweeper within 5s")
	case err := <-errCh:
		t.Fatalf("startBackground reported an error for a typed-nil sweeper: %v", err)
	}
	select {
	case err := <-errCh:
		t.Fatalf("unexpected error from typed-nil sweeper: %v", err)
	default:
	}
}

// TestStartBackground_RealWorkerRuns proves the normalization did not
// break the positive path: a non-nil worker is still started and its
// Run error (non-cancel) is still surfaced on errCh.
func TestStartBackground_RealWorkerRuns(t *testing.T) {
	logger := quietLogger()
	errCh := make(chan error, 1)
	ran := make(chan struct{})
	worker := workerStub{ran: ran}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startBackground(ctx, logger, worker, "housekeeping sweeper running", errCh)

	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("worker.Run was never called within 5s")
	}
	select {
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	default:
	}
	select {
	case <-done:
		t.Fatal("done must not be closed while the worker is still running")
	default:
	}
	cancel() // Run returns on ctx cancel; wait for the goroutine to finish.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker goroutine did not stop within 5s of ctx cancel")
	}
}

// workerStub is a minimal Run(context.Context) error used to prove
// startBackground's positive path.
type workerStub struct{ ran chan struct{} }

func (w workerStub) Run(ctx context.Context) error {
	close(w.ran)
	<-ctx.Done()
	return ctx.Err()
}
