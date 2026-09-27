package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// OutboxPublisher implements ports.EventPublisher by writing each
// configured encoder's Kafka wire form of event into outbox_events
// instead of the broker (ADR-0018). When called inside UnitOfWork.Execute
// the insert joins the use case's transaction, so the aggregate change
// and every one of its outbox rows commit together or not at all.
// OutboxRelay later drains the table onto Kafka.
//
// One outbox row is written per (event x encoder), so facility-layout's
// integration topic (warehouse.facility.events) and its analytics topic
// (warehouse.facility.analytics) can never diverge: both are enqueued in
// the exact same transaction as the aggregate write.
type OutboxPublisher struct {
	pool     *pgxpool.Pool
	newId    func() string
	encoders []outboundkafka.Encoder
}

// NewOutboxPublisher constructs an OutboxPublisher over pool that fans
// each event through every encoder given, in order. newId mints the
// envelope event_id shared by a given event's row across every encoder
// invocation, so a redelivery carries the same id on every topic it was
// enqueued for. Passing no encoders defaults to the integration publisher
// alone.
func NewOutboxPublisher(pool *pgxpool.Pool, newId func() string, encoders ...outboundkafka.Encoder) *OutboxPublisher {
	return &OutboxPublisher{pool: pool, newId: newId, encoders: encoders}
}

// Publish stores event's encoded message for every configured encoder in
// the outbox. It never touches Kafka.
func (p *OutboxPublisher) Publish(ctx context.Context, event shared.DomainEvent) error {
	eventId := p.newId()
	q := querierFrom(ctx, p.pool)
	for _, enc := range p.encoders {
		encoded, err := enc.Encode(ctx, event, eventId)
		if err != nil {
			return err
		}
		_, err = q.Exec(ctx, `
			INSERT INTO outbox_events (topic, event_type, key, value)
			VALUES ($1, $2, $3, $4)
		`, encoded.Topic, encoded.EventType, encoded.Key, encoded.Value)
		if err != nil {
			return fmt.Errorf("postgres: enqueue outbox event %s for %s: %w", encoded.EventType, encoded.Topic, err)
		}
	}
	return nil
}
