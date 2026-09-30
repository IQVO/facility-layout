package analyticsstore

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ConsumedEventsRepo is a pgxpool-backed idempotency gate over the analytical
// database's analytics_consumed_events table. It is the consumer's dedupe set,
// kept separate from the projection's own event-id claim (analytics_processed_events)
// so the two idempotency layers do not race for the same row.
type ConsumedEventsRepo struct {
	pool *pgxpool.Pool
}

// NewConsumedEventsRepo constructs a ConsumedEventsRepo over pool.
func NewConsumedEventsRepo(pool *pgxpool.Pool) *ConsumedEventsRepo {
	return &ConsumedEventsRepo{pool: pool}
}

// IsProcessed reports whether eventId is already recorded in
// analytics_consumed_events.
func (r *ConsumedEventsRepo) IsProcessed(ctx context.Context, eventId string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM analytics_consumed_events WHERE event_id = $1)`,
		eventId).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// MarkProcessed records eventId in analytics_consumed_events if absent,
// returning true iff this call newly recorded it.
func (r *ConsumedEventsRepo) MarkProcessed(ctx context.Context, eventId string) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`INSERT INTO analytics_consumed_events (event_id) VALUES ($1) ON CONFLICT (event_id) DO NOTHING`,
		eventId)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
