package main

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// TestMigrationsDatabaseURLFallback proves the fallback wiring
// cmd/mcp/main.go's run() applies before calling buildAdapters:
// getenv("MIGRATIONS_DATABASE_URL", databaseURL) must return
// MIGRATIONS_DATABASE_URL's own value when it is set, and databaseURL
// itself (DATABASE_URL) when it is unset. This mirrors cmd/facility's
// identical test — see ADR 0023-migrations-direct-postgres-connection.md
// (mirroring order-management's ADR-0029) for the full "why".
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

// TestBuildAdapters_UsesMigrationsDatabaseURLNotDatabaseURLForMigrations
// proves buildAdapters itself — not just the getenv helper above — threads
// migrationsDatabaseURL into the migration step and databaseURL into the
// pgxpool, rather than the two ever being conflated. Gives databaseURL an
// address nothing listens on (so opening the pgxpool, which happens AFTER
// migrations succeed, would hang/fail loudly if ever reached) and
// migrationsDatabaseURL a schemeless string that migrate.New rejects
// immediately with a distinctive parse error — if buildAdapters ignored
// migrationsDatabaseURL and ran migrations against databaseURL instead,
// this test would see a dial/"connection refused" error, not the parse
// error.
func TestBuildAdapters_UsesMigrationsDatabaseURLNotDatabaseURLForMigrations(t *testing.T) {
	const (
		bogusMigrationsURL = "not-a-valid-connection-string"
		unreachableAppURL  = "postgres://u:***@127.0.0.1:1/facility_layout?sslmode=disable&connect_timeout=1"
	)

	_, _, err := buildAdapters(unreachableAppURL, bogusMigrationsURL, t.TempDir(), quietLoggerMCP())
	if err == nil {
		t.Fatal("a malformed MIGRATIONS_DATABASE_URL must fail boot")
	}
	if !strings.Contains(err.Error(), "parse scheme") {
		t.Fatalf("err = %v — expected the bogus-URL parse error from migrate.New; a \"connection refused\"/dial error here would mean migrations ran against databaseURL/unreachableAppURL instead of migrationsDatabaseURL", err)
	}
}

// TestBuildAdapters_NoDatabaseURLUsesMemoryImmediately proves the
// no-database branch is untouched by the migrationsDatabaseURL parameter:
// with databaseURL empty, buildAdapters must return the in-memory
// adapters instantly, without ever attempting to run migrations (which
// would fail fast against a bogus migrationsDatabaseURL if it were
// reached).
func TestBuildAdapters_NoDatabaseURLUsesMemoryImmediately(t *testing.T) {
	_, closeFn, err := buildAdapters("", "not-a-valid-connection-string", t.TempDir(), quietLoggerMCP())
	if err != nil {
		t.Fatalf("buildAdapters: %v", err)
	}
	closeFn()
}

func quietLoggerMCP() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
