package postgres

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Housekeeping defaults (ADR-0026). cmd/facility reads the matching
// environment variables and passes explicit values; these are the fallbacks.
const (
	// DefaultSweepInterval is how often the Sweeper wakes up.
	DefaultSweepInterval = time.Hour
	// DefaultIdempotencyKeyTTL is how long an idempotency_keys row is kept.
	// It bounds how late a client retry can still be replayed from cache
	// rather than treated as a brand-new request (ADR-0019).
	DefaultIdempotencyKeyTTL = 24 * time.Hour
	// DefaultOutboxRetention is how long a PUBLISHED outbox_events row is
	// kept for forensics before deletion (ADR-0018). Unpublished rows are
	// never swept, however old.
	DefaultOutboxRetention = 7 * 24 * time.Hour
	// defaultSweepBatch bounds each DELETE statement so a large backlog is
	// removed in short transactions instead of one long table lock.
	defaultSweepBatch = 1000
)

// SweepResult reports how many rows one pass deleted from each table.
type SweepResult struct {
	IdempotencyKeys int64
	OutboxEvents    int64
}

// Sweeper is the periodic housekeeping job that bounds the two append-only
// tables this service otherwise grows without limit: idempotency_keys
// (rows older than the TTL) and outbox_events (PUBLISHED rows older than
// the retention). It is deliberately one small type with one loop rather
// than two jobs — both are "delete old rows in batches" and share an
// interval.
//
// A TTL/retention of 0 (or negative) disables that half: the rows are kept
// forever.
//
// It is safe to run in several replicas at once: each DELETE targets an
// explicit id/key set chosen by a subquery, so concurrent sweepers can only
// delete rows that are anyway eligible; at worst one deletes zero rows.
type Sweeper struct {
	pool            *pgxpool.Pool
	logger          *slog.Logger
	interval        time.Duration
	idempotencyTTL  time.Duration
	outboxRetention time.Duration
	batchSize       int
}

// SweeperOption configures a Sweeper beyond its required pool.
type SweeperOption func(*Sweeper)

// WithSweepInterval overrides the default 1h interval.
func WithSweepInterval(d time.Duration) SweeperOption {
	return func(s *Sweeper) { s.interval = d }
}

// WithIdempotencyKeyTTL overrides the default 24h key TTL (<=0 disables).
func WithIdempotencyKeyTTL(d time.Duration) SweeperOption {
	return func(s *Sweeper) { s.idempotencyTTL = d }
}

// WithOutboxRetention overrides the default 7d published-row retention
// (<=0 disables).
func WithOutboxRetention(d time.Duration) SweeperOption {
	return func(s *Sweeper) { s.outboxRetention = d }
}

// WithSweepBatchSize overrides the default 1000-row DELETE batch.
func WithSweepBatchSize(n int) SweeperOption {
	return func(s *Sweeper) {
		if n > 0 {
			s.batchSize = n
		}
	}
}

// WithSweeperLogger overrides the default slog.Default().
func WithSweeperLogger(l *slog.Logger) SweeperOption {
	return func(s *Sweeper) { s.logger = l }
}

// NewSweeper builds a Sweeper over pool with the ADR-0026 defaults.
func NewSweeper(pool *pgxpool.Pool, opts ...SweeperOption) *Sweeper {
	s := &Sweeper{
		pool:            pool,
		logger:          slog.Default(),
		interval:        DefaultSweepInterval,
		idempotencyTTL:  DefaultIdempotencyKeyTTL,
		outboxRetention: DefaultOutboxRetention,
		batchSize:       defaultSweepBatch,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Run sweeps once immediately (so a freshly started pod after a long
// outage catches up without waiting a full interval) and then every
// interval until ctx is cancelled. It never returns a non-nil error: a
// failed pass is logged and retried on the next tick.
func (s *Sweeper) Run(ctx context.Context) error {
	if s.interval <= 0 {
		return nil
	}
	for {
		res, err := s.SweepOnce(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			s.logger.Error("housekeeping sweep failed", "error", err,
				"idempotency_keys_deleted", res.IdempotencyKeys, "outbox_events_deleted", res.OutboxEvents)
		case res.IdempotencyKeys > 0 || res.OutboxEvents > 0:
			s.logger.Info("housekeeping sweep",
				"idempotency_keys_deleted", res.IdempotencyKeys, "outbox_events_deleted", res.OutboxEvents)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(s.interval):
		}
	}
}

// SweepOnce runs exactly one pass, deleting expired rows batch by batch
// until a batch comes back short, and reports the totals. Exposed so tests
// drive it deterministically; production uses Run.
func (s *Sweeper) SweepOnce(ctx context.Context) (SweepResult, error) {
	var res SweepResult
	var err error

	if s.idempotencyTTL > 0 {
		res.IdempotencyKeys, err = s.deleteInBatches(ctx, `
			DELETE FROM idempotency_keys WHERE key IN (
				SELECT key FROM idempotency_keys
				WHERE created_at < now() - ($1 * interval '1 second')
				ORDER BY created_at
				LIMIT $2
			)`, s.idempotencyTTL)
		if err != nil {
			return res, err
		}
	}

	if s.outboxRetention > 0 {
		res.OutboxEvents, err = s.deleteInBatches(ctx, `
			DELETE FROM outbox_events WHERE id IN (
				SELECT id FROM outbox_events
				WHERE published_at IS NOT NULL
				  AND published_at < now() - ($1 * interval '1 second')
				ORDER BY id
				LIMIT $2
			)`, s.outboxRetention)
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

// deleteInBatches runs stmt (which takes the age in seconds and the batch
// size) repeatedly until a batch deletes fewer rows than the batch size or
// ctx is cancelled, returning the total deleted.
func (s *Sweeper) deleteInBatches(ctx context.Context, stmt string, age time.Duration) (int64, error) {
	var total int64
	for {
		tag, err := s.pool.Exec(ctx, stmt, age.Seconds(), s.batchSize)
		if err != nil {
			return total, err
		}
		n := tag.RowsAffected()
		total += n
		if n < int64(s.batchSize) {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}
