package main

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
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
