// Package postgres provides pgxpool-backed implementations of every
// outbound port, plus a golang-migrate runner for the SQL migrations in
// /migrations.
package postgres

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MaxConns is the OLTP pool's per-process connection ceiling, shared by
// both cmd/facility (the api Deployment, HPA-scalable up to
// charts/facility-layout values.yaml's autoscaling.api.maxReplicas, 4) and
// cmd/mcp (the mcp Deployment, fixed at 1 replica -- see that chart
// value's own doc comment for why it does not get an HPA).
//
// Sized against this shared Postgres instance's REAL max_connections (100,
// an unmodified Bitnami chart default -- warehouse-infra's Terraform does
// not override it) the same way order-management's ADR-0026 sized its own
// OLTP pool: at the api Deployment's HPA ceiling of 4 replicas, 4 * 10 =
// 40 connections. facility-layout's OLTP path now goes through PgBouncer
// in transaction-pooling mode (warehouse-infra PR #43) rather than
// directly against the shared Postgres instance, so this MaxConns bounds
// each process's own demand on the PgBouncer pool, not a direct
// server-side connection slot -- PgBouncer is what absorbs the real
// fleet-wide multiplexing across all 10 services. See ADR-0022 for the
// full connection-budget accounting.
const MaxConns = 10

// StatementTimeout bounds how long a single query may hold a connection on
// the OLTP database before Postgres cancels it. facility-layout's OLTP
// queries are all single-aggregate reads/writes (one Site/Zone/Aisle/Slot,
// keyed by id or location code) that normally complete in low
// milliseconds; 5s is generous headroom for lock contention or a slow disk
// without letting one runaway or blocked query hold a pool slot --
// therefore a bulkhead slot the HPA's replica math is sizing capacity
// around -- indefinitely. See ADR-0022.
const StatementTimeout = "5s"

// NewPool opens a connection pool against databaseURL, with MaxConns and
// StatementTimeout applied to every connection, traced with OpenTelemetry:
// every query, batch, copy and connection acquisition becomes a child span
// of whatever request or use case triggered it, so a slow endpoint can be
// attributed to a specific statement.
//
// otelpgx records the SQL statement in normalized form by default (no
// literal values), which is what keeps span attributes free of customer
// data and of unbounded cardinality — do not disable it.
//
// Pool statistics (idle/acquired/max connections) are also exported as
// metrics. Failing to register them is not fatal: telemetry never keeps the
// service from opening its database.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	return NewPoolWithLimits(ctx, databaseURL, MaxConns, StatementTimeout)
}

// NewPoolWithLimits is NewPool's shared implementation, taking maxConns and
// statementTimeout explicitly so an integration test can drive a much
// shorter timeout directly -- proving the AfterConnect hook really applies
// the setting to every new connection, by triggering an actual
// cancellation -- without waiting out the real production value.
// Production callers should use NewPool.
func NewPoolWithLimits(ctx context.Context, databaseURL string, maxConns int32, statementTimeout string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = maxConns
	cfg.ConnConfig.Tracer = otelpgx.NewTracer(
		// otelpgx v0.12.0 stopped prefixing span names with the operation
		// kind ("query ", "prepare ", "batch query ") by default, to follow
		// the OTel database span-naming convention. This repo's tests and
		// tracing dashboards key off the "query " prefix (see
		// TestDatabaseCallsBecomeChildSpans), so opt back into the previous
		// behavior explicitly rather than silently losing it on the bump.
		otelpgx.WithQuerySpanNamePrefix(),
	)
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, fmt.Sprintf("SET statement_timeout = '%s'", statementTimeout))
		return err
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := otelpgx.RecordStats(pool); err != nil {
		slog.Warn("pgx pool stats metrics not registered", "error", err)
	}
	return pool, nil
}
