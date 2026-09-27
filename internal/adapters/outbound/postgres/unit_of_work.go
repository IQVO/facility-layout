package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/facility-layout/internal/pgtx"
)

// querier is the subset of pgx that both *pgxpool.Pool and pgx.Tx satisfy.
// Every adapter in this package issues its SQL through the querier it
// resolves from the context, so the same repo/publisher code runs either
// autonomously (pool) or inside a UnitOfWork transaction (tx) without
// knowing which.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// withTx and txFrom are thin aliases over internal/pgtx — the ONE
// transaction-in-context mechanism this service uses. It lives in its
// own package (rather than as an unexported type here) specifically so
// internal/adapters/inbound/http's idempotency middleware can bind a
// transaction it began itself into the same slot, and have it join here
// via UnitOfWork.Execute/querierFrom below, without either adapter
// package importing the other (see internal/pgtx's doc comment for the
// full reasoning and internal/architecture's fitness tests for the rule
// this sidesteps).
func withTx(ctx context.Context, tx pgx.Tx) context.Context { return pgtx.WithTx(ctx, tx) }
func txFrom(ctx context.Context) (pgx.Tx, bool)             { return pgtx.TxFrom(ctx) }

// querierFrom resolves the transaction bound to ctx, or falls back to the
// pool when the caller is not inside a UnitOfWork.
func querierFrom(ctx context.Context, pool *pgxpool.Pool) querier {
	if tx, ok := txFrom(ctx); ok {
		return tx
	}
	return pool
}

// UnitOfWork implements ports.UnitOfWork over a single Postgres
// transaction. Everything the wrapped function does through this
// package's adapters — the aggregate upsert(s) AND the outbox insert(s) —
// commits together or not at all (ADR-0018).
type UnitOfWork struct {
	pool *pgxpool.Pool
}

// NewUnitOfWork constructs a UnitOfWork over pool.
func NewUnitOfWork(pool *pgxpool.Pool) *UnitOfWork {
	return &UnitOfWork{pool: pool}
}

// Execute runs fn inside one transaction. A nested call (fn invoked with
// a ctx that already carries a transaction) simply joins the outer scope
// rather than opening a second one, so composing use cases never
// deadlocks on itself.
func (u *UnitOfWork) Execute(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if _, ok := txFrom(ctx); ok {
		return fn(ctx)
	}

	tx, err := u.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin unit of work: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				err = errors.Join(err, fmt.Errorf("postgres: rollback unit of work: %w", rbErr))
			}
		}
	}()

	if err = fn(withTx(ctx, tx)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit unit of work: %w", err)
	}
	return nil
}
