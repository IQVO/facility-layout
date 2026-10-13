package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
)

// Sink is where OutboxRelay forwards drained events — in production
// kafka.RelaySink; in tests a recorder.
type Sink interface {
	Send(ctx context.Context, enc outboundkafka.Encoded) error
}

// OutboxRelay drains unpublished outbox_events rows onto a Sink, oldest
// first, marking each row published as it goes (ADR-0018).
//
// Delivery is at-least-once: a crash between a successful Send and the
// row's UPDATE republishes that event on the next pass. Ordering per
// aggregate is preserved because rows are drained in id order within one
// relay and FOR UPDATE SKIP LOCKED keeps two relays (a rolling deploy's
// overlapping old and new pod) from claiming the same row.
//
// With WithMaxAttempts(n), n > 0 (ADR 0036; cmd/facility passes
// OUTBOX_MAX_ATTEMPTS, default 20), a row that has failed n times is
// excluded from claims and logged at ERROR: later rows, including rows with
// the same key, then overtake it. That trade is deliberate (one poison row
// must not stop the whole relay forever) and the excluded row stays in
// outbox_events (attempts, last_error) until an operator re-arms it.
type OutboxRelay struct {
	pool        *pgxpool.Pool
	sink        Sink
	logger      *slog.Logger
	interval    time.Duration
	batchSize   int
	maxAttempts int
}

// DefaultOutboxMaxAttempts is the attempt cap a composition root uses when
// OUTBOX_MAX_ATTEMPTS is unset (fleet convention, ADR 0036). The relay's own
// zero value stays 0 (unlimited) so the option is always an explicit choice.
const DefaultOutboxMaxAttempts = 20

// RelayOption customises an OutboxRelay.
type RelayOption func(*OutboxRelay)

// WithMaxAttempts overrides the default of 0 (unlimited retries): a row
// that has failed this many times is excluded from future claims (still
// visible in outbox_events for manual inspection) instead of retried
// indefinitely, and the failure that reaches the cap is logged at ERROR
// with the row's id, topic, type and key (ADR 0036). 0 keeps retrying
// without bound. Every cmd/* that builds a relay must pass it (architecture
// fitness test TestOutboxRelaysPassAttemptCap).
func WithMaxAttempts(n int) RelayOption {
	return func(r *OutboxRelay) { r.maxAttempts = n }
}

// WithInterval sets how long the relay sleeps between passes when the
// last pass found nothing to publish. Default 1s.
func WithInterval(d time.Duration) RelayOption {
	return func(r *OutboxRelay) { r.interval = d }
}

// WithBatchSize caps how many rows one pass claims. Default 100.
func WithBatchSize(n int) RelayOption {
	return func(r *OutboxRelay) { r.batchSize = n }
}

// NewOutboxRelay constructs a relay draining pool into sink.
func NewOutboxRelay(pool *pgxpool.Pool, sink Sink, logger *slog.Logger, opts ...RelayOption) *OutboxRelay {
	if logger == nil {
		logger = slog.Default()
	}
	r := &OutboxRelay{pool: pool, sink: sink, logger: logger, interval: time.Second, batchSize: 100}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Run drains the outbox until ctx is cancelled. A pass that publishes a
// full batch is followed immediately by another pass (there is probably
// more waiting); an empty pass sleeps for the configured interval. A
// failing pass is logged and retried after the interval — the rows stay
// unpublished, so nothing is lost.
func (r *OutboxRelay) Run(ctx context.Context) error {
	for {
		n, err := r.RelayOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			r.logger.ErrorContext(ctx, "outbox relay pass failed", "error", err)
		}
		if n == r.batchSize && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.interval):
		}
	}
}

// RelayOnce performs a single pass: claim up to batchSize unpublished rows
// under a row lock, send each in order, and mark it published. It returns
// how many rows were published. On the first Send failure the pass stops
// (preserving per-aggregate ordering — a later event must not overtake a
// failed earlier one), records the error on that row, and returns it;
// rows already sent in this pass stay marked published. With an attempt
// cap (WithMaxAttempts), rows that have already failed that many times are
// not claimed at all (ADR 0036).
func (r *OutboxRelay) RelayOnce(ctx context.Context) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: begin relay pass: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	batch, err := r.claimBatch(ctx, tx)
	if err != nil {
		return 0, err
	}

	published := 0
	for _, p := range batch {
		if err := r.sink.Send(ctx, p.enc); err != nil {
			return published, r.recordFailure(ctx, tx, p, err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE outbox_events SET published_at = now(), attempts = attempts + 1, last_error = NULL WHERE id = $1
		`, p.id); err != nil {
			return published, fmt.Errorf("postgres: mark outbox row %d published: %w", p.id, err)
		}
		published++
	}
	if err := tx.Commit(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return published, fmt.Errorf("postgres: commit relay pass: %w", err)
	}
	if published > 0 {
		r.logger.DebugContext(ctx, "outbox relay published events", "count", published)
	}
	return published, nil
}

// pendingRow is one claimed, unpublished outbox_events row.
type pendingRow struct {
	id  int64
	enc outboundkafka.Encoded
}

// claimBatch selects and locks up to batchSize unpublished rows, oldest
// first, within tx. With an attempt cap, rows that have reached it are
// excluded (ADR 0036).
func (r *OutboxRelay) claimBatch(ctx context.Context, tx pgx.Tx) ([]pendingRow, error) {
	attemptsFilter := ""
	args := []any{r.batchSize}
	if r.maxAttempts > 0 {
		attemptsFilter = "AND attempts < $2"
		args = append(args, r.maxAttempts)
	}
	rows, err := tx.Query(ctx, `
		SELECT id, topic, event_type, key, value
		FROM outbox_events
		WHERE published_at IS NULL `+attemptsFilter+`
		ORDER BY id
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: claim outbox rows: %w", err)
	}
	defer rows.Close()
	var batch []pendingRow
	for rows.Next() {
		var p pendingRow
		if err := rows.Scan(&p.id, &p.enc.Topic, &p.enc.EventType, &p.enc.Key, &p.enc.Value); err != nil {
			return nil, fmt.Errorf("postgres: scan outbox row: %w", err)
		}
		batch = append(batch, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: read outbox rows: %w", err)
	}
	return batch, nil
}

// recordFailure stages the failed row's attempts/last_error, commits the
// pass (every row published so far in it has its UPDATE staged in the same
// tx) and stops there, so a later row never overtakes a row that is still
// being retried. The ERROR for a row that reached the attempt cap is logged
// only after the commit, so it is never emitted for a rolled-back attempt.
func (r *OutboxRelay) recordFailure(ctx context.Context, tx pgx.Tx, p pendingRow, sendErr error) error {
	err := sendErr
	attempts := 0
	if uerr := tx.QueryRow(ctx, `
		UPDATE outbox_events SET attempts = attempts + 1, last_error = $2 WHERE id = $1
		RETURNING attempts
	`, p.id, sendErr.Error()).Scan(&attempts); uerr != nil {
		err = errors.Join(err, fmt.Errorf("postgres: record outbox failure: %w", uerr))
	}
	if cerr := tx.Commit(ctx); cerr != nil {
		return fmt.Errorf("outbox relay: send %s (row %d): %w", p.enc.EventType, p.id,
			errors.Join(err, fmt.Errorf("postgres: commit relay pass: %w", cerr)))
	}
	r.logCapReached(ctx, p, attempts, sendErr)
	return fmt.Errorf("outbox relay: send %s (row %d): %w", p.enc.EventType, p.id, err)
}

// logCapReached logs, once, at ERROR, the failure that made a row reach the
// attempt cap (ADR 0036): from the next pass on claimBatch excludes it,
// later rows overtake it (including rows with the same key) and it stays in
// outbox_events until an operator re-arms it
// (`UPDATE outbox_events SET attempts = 0 WHERE id = ...`). Failures below
// the cap are reported by Run's "outbox relay pass failed".
func (r *OutboxRelay) logCapReached(ctx context.Context, p pendingRow, attempts int, sendErr error) {
	if r.maxAttempts <= 0 || attempts < r.maxAttempts {
		return
	}
	r.logger.ErrorContext(ctx, "outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause",
		"outbox_id", p.id, "topic", p.enc.Topic, "event_type", p.enc.EventType,
		"key", string(p.enc.Key), "attempts", attempts, "max_attempts", r.maxAttempts, "error", sendErr)
}
