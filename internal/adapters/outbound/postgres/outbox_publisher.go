package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"

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
// CloudEvents `id` (ADR-0024) ONCE per domain event; it is shared by that
// event's row across every encoder invocation and persisted inside the
// row's pre-encoded value, so a relay redelivery carries the same id on
// every topic it was enqueued for. Passing no encoders defaults to the integration publisher
// alone.
func NewOutboxPublisher(pool *pgxpool.Pool, newId func() string, encoders ...outboundkafka.Encoder) *OutboxPublisher {
	return &OutboxPublisher{pool: pool, newId: newId, encoders: encoders}
}

// Publish stores event's encoded message for every configured encoder in
// the outbox. It never touches Kafka. The W3C trace context of ctx (the
// use case's request or consumer span) is captured once and stored with
// every row in the headers column (ADR-0038), NULL when no span is active.
func (p *OutboxPublisher) Publish(ctx context.Context, event shared.DomainEvent) error {
	eventId := p.newId()
	headers, err := marshalTraceHeaders(outboundkafka.TraceHeaders(ctx))
	if err != nil {
		return err
	}
	q := querierFrom(ctx, p.pool)
	for _, enc := range p.encoders {
		encoded, err := enc.Encode(ctx, event, eventId)
		if err != nil {
			return err
		}
		_, err = q.Exec(ctx, `
			INSERT INTO outbox_events (topic, event_type, key, value, headers)
			VALUES ($1, $2, $3, $4, $5)
		`, encoded.Topic, encoded.EventType, encoded.Key, encoded.Value, headers)
		if err != nil {
			return fmt.Errorf("postgres: enqueue outbox event %s for %s: %w", encoded.EventType, encoded.Topic, err)
		}
	}
	return nil
}

// outboxHeader is the JSON shape one Kafka header takes inside
// outbox_events.headers: [{"key":"traceparent","value":"00-..."}] (the same
// shape order-management's ADR 0041 stores).
type outboxHeader struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// marshalTraceHeaders encodes the captured trace headers for the headers
// column; no headers is SQL NULL, so an untraced row relays unchanged.
func marshalTraceHeaders(headers []kafkago.Header) ([]byte, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	out := make([]outboxHeader, 0, len(headers))
	for _, h := range headers {
		out = append(out, outboxHeader{Key: h.Key, Value: string(h.Value)})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("postgres: marshal outbox trace headers: %w", err)
	}
	return b, nil
}

// unmarshalTraceHeaders decodes the headers column; NULL is no headers.
func unmarshalTraceHeaders(raw []byte) ([]kafkago.Header, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var in []outboxHeader
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	out := make([]kafkago.Header, 0, len(in))
	for _, h := range in {
		out = append(out, kafkago.Header{Key: h.Key, Value: []byte(h.Value)})
	}
	return out, nil
}
