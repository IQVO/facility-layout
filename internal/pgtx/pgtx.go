// Package pgtx holds the ONE mechanism this service uses to bind a pgx
// transaction into a context.Context: an unexported key type plus
// WithTx/TxFrom. It exists as its own small package — sibling to, not
// nested inside, internal/adapters/inbound or internal/adapters/outbound
// — so that BOTH sides of a genuinely cross-cutting concern can bind and
// read the SAME transaction-in-context without either one importing the
// other's adapter package (which internal/architecture's fitness tests
// forbid in both directions: inbound must never depend on outbound, and
// outbound must never depend on inbound).
//
// internal/adapters/outbound/postgres (UnitOfWork.Execute, querierFrom —
// see that package's unit_of_work.go) is the primary, original owner of
// this mechanism and continues to use it for every repo/publisher
// adapter's own transaction handling (ADR-0018, transactional outbox).
//
// internal/adapters/inbound/http's idempotency middleware
// (RequireIdempotencyKey) is the second, new caller: it begins its OWN
// pgx transaction directly against the pgxpool.Pool it is given (no
// import of the postgres package needed for that), binds it into the
// request's context via pgtx.WithTx, and calls the wrapped handler with
// that context. When the handler's own use case later calls
// ports.UnitOfWork.Execute (backed by postgres.UnitOfWork), that
// Execute call resolves the SAME transaction via pgtx.TxFrom (through
// postgres.querierFrom, which now delegates here) and JOINS it — runs
// its function directly on the existing transaction — instead of
// opening a second, invisible-to-each-other one. This is what makes the
// whole HTTP-request-to-response cycle (idempotency bookkeeping, the
// aggregate Save, the outbox insert) commit or roll back together as
// one atomic unit. See idempotency.go's package doc comment for the
// fuller correctness argument and idempotency_integration_test.go for
// the real, executed proof that the join actually happens rather than
// silently opening a nested transaction. Mirrors order-management's
// identically-named package (PR #105, ADR 0023) — the reference
// implementation this fleet's other services copy for this feature.
package pgtx

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type txKey struct{}

// WithTx returns a child context carrying tx. Any code that resolves a
// transaction from ctx via TxFrom (directly, or indirectly through the
// postgres package's querierFrom/UnitOfWork.Execute) sees exactly this tx.
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// TxFrom reports the transaction bound to ctx, if any.
func TxFrom(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}
