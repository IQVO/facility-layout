//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

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

// These tests own their database: ONE throwaway testcontainers Postgres
// per package run (started lazily by the first newPool call, terminated in
// TestMain), with the real migrations applied once. They never read
// DATABASE_URL and never skip — a broken repository must fail CI, not
// silently pass. newPool truncates the schema per test for isolation.
var sharedPG struct {
	once      sync.Once
	container *tcpostgres.PostgresContainer
	url       string
	err       error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if sharedPG.container != nil {
		if err := testcontainers.TerminateContainer(sharedPG.container); err != nil {
			fmt.Fprintf(os.Stderr, "terminate postgres container: %v\n", err)
		}
	}
	os.Exit(code)
}

// sharedDatabaseURL returns the DSN of the package-wide migrated Postgres,
// booting it on first use.
func sharedDatabaseURL(t *testing.T) string {
	t.Helper()
	sharedPG.once.Do(func() {
		ctx := context.Background()
		container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("facility"),
			tcpostgres.WithUsername("facility"),
			tcpostgres.WithPassword("facility"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err != nil {
			sharedPG.err = fmt.Errorf("start postgres container: %w", err)
			return
		}
		sharedPG.container = container
		url, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			sharedPG.err = fmt.Errorf("connection string: %w", err)
			return
		}
		if err := postgres.RunMigrations(url, migrationsDir(t)); err != nil {
			sharedPG.err = fmt.Errorf("run migrations: %w", err)
			return
		}
		sharedPG.url = url
	})
	if sharedPG.err != nil {
		t.Fatalf("shared postgres: %v", sharedPG.err)
	}
	return sharedPG.url
}

// newPool opens a pool on the shared migrated database. Every integration
// test starts from a truncated schema so tests do not see each other's
// rows regardless of execution order.
func newPool(t *testing.T) (context.Context, *pgxPool) {
	t.Helper()

	databaseURL := sharedDatabaseURL(t)

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
