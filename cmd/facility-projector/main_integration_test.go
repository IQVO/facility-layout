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
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// One Postgres and one Kafka serve the whole package, booted lazily on first
// use and terminated once in TestMain — the same lifecycle cmd/a2a and the
// inbound/kafka integration tests use (this repo has no internal/testing
// harness package; each package owns its containers).
var (
	pgContainer *tcpostgres.PostgresContainer
	pgSharedURL string
	kafkaBroker string
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

// bootShared starts the package's Postgres and Kafka exactly once.
func bootShared() error {
	startOnce.Do(func() {
		ctx := context.Background()
		pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("facility_projector_it"),
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
			return
		}
		kc, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1",
			tckafka.WithClusterID(fmt.Sprintf("facility-projector-itest-%d", time.Now().UnixNano())))
		if err != nil {
			startErr = fmt.Errorf("start kafka container: %w", err)
			return
		}
		brokers, err := kc.Brokers(ctx)
		if err != nil {
			startErr = fmt.Errorf("resolve kafka brokers: %w", err)
			return
		}
		kafkaBroker = brokers[0]
		// The container is terminated through ryuk; kept referenced only for
		// clarity of lifecycle in TestMain's termination of the postgres one.
	})
	return startErr
}

// itFreshDB hands the test its own private EMPTY database: the projector's
// run() runs the analytics migrations itself (ANALYTICS_MIGRATIONS_PATH).
func itFreshDB(t *testing.T) string {
	t.Helper()
	if err := bootShared(); err != nil {
		t.Fatalf("shared containers: %v", err)
	}
	name := fmt.Sprintf("proj_cmd_%d", dbSeq.Add(1))
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
