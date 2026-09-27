//go:build integration

package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// migrationsDir resolves /migrations relative to this test file, so the
// test works regardless of the working directory `go test` is invoked from.
func migrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to resolve test file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "migrations")
}

func requireDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping Postgres integration test")
	}
	return url
}

// newPool migrates the database and opens a pool. Every integration test
// starts from a migrated, truncated schema so tests do not see each other's
// rows regardless of execution order.
func newPool(t *testing.T) (context.Context, *pgxPool) {
	t.Helper()

	databaseURL := requireDatabaseURL(t)
	if err := postgres.RunMigrations(databaseURL, migrationsDir(t)); err != nil {
		t.Fatalf("unexpected error running migrations: %v", err)
	}

	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, databaseURL)
	if err != nil {
		t.Fatalf("unexpected error opening pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(ctx, `
		TRUNCATE location_slots, placement_rules, location_types, aisles, zones, sites, outbox_events RESTART IDENTITY CASCADE
	`); err != nil {
		t.Fatalf("unexpected error truncating: %v", err)
	}
	return ctx, pool
}

func mustCode(t *testing.T, raw string) shared.LocationCode {
	t.Helper()
	code, err := shared.ParseLocationCode(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return code
}

func mustCapacity(t *testing.T, weight, volume float64) shared.Capacity {
	t.Helper()
	capacity, err := shared.NewCapacity(weight, volume)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return capacity
}

func mustLocationType(t *testing.T, name string, weight, volume float64) placement.LocationType {
	t.Helper()
	lt, err := placement.NewLocationType(name, placement.Storage, mustCapacity(t, weight, volume))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return lt
}

// fixedTime is the deterministic timestamp the outbox test publishes with.
func fixedTime() time.Time {
	return time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC)
}
