//go:build integration

package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// One Postgres serves the whole package, booted lazily on first use and
// terminated once in TestMain — the same lifecycle cmd/a2a uses (this repo
// has no internal/testing harness package; each package owns its container).
var (
	pgContainer *tcpostgres.PostgresContainer
	pgSharedURL string
	startOnce   sync.Once
	startErr    error
	dbSeq       atomic.Uint64
)

func TestMain(m *testing.M) {
	code := m.Run()
	if pgContainer != nil {
		if err := testcontainers.TerminateContainer(pgContainer); err != nil {
			fmt.Fprintf(os.Stderr, "terminate postgres container: %v\n", err)
		}
	}
	os.Exit(code)
}

// repBootShared starts the package's Postgres exactly once.
func repBootShared() error {
	startOnce.Do(func() {
		ctx := context.Background()
		pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("facility_reports_it"),
			tcpostgres.WithUsername("facility"),
			tcpostgres.WithPassword("facility"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err != nil {
			startErr = fmt.Errorf("start postgres container: %w", err)
			return
		}
		pgContainer = pg
		if pgSharedURL, err = pg.ConnectionString(ctx, "sslmode=disable"); err != nil {
			startErr = fmt.Errorf("connection string: %w", err)
		}
	})
	return startErr
}

// repFreshDB hands the test its own private EMPTY database; the test runs the
// analytics migrations itself (the reader never migrates: ADR-0010).
func repFreshDB(t *testing.T) string {
	t.Helper()
	if err := repBootShared(); err != nil {
		t.Fatalf("shared postgres: %v", err)
	}
	name := fmt.Sprintf("rep_cmd_%d", dbSeq.Add(1))
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, pgSharedURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", name)); err != nil {
		t.Fatalf("create database: %v", err)
	}
	u, err := url.Parse(pgSharedURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}
