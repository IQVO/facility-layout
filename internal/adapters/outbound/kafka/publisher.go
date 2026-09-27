// Package kafka provides the outbound adapter that publishes facility-layout
// domain events onto Kafka, satisfying ports.EventPublisher.
//
// facility-layout is an Open Host Service with a Published Language: its
// domain events ARE its integration contract, and every downstream service
// (inventory-storage, wes-work-planning, workforce-management,
// fulfillment-execution) is a Conformist to them. Unlike a service that
// forwards a single enriched event, this publisher therefore emits EVERY
// domain event to the integration topic — the whole Published Language.
//
// The events already serialize themselves to JSON (their struct tags are the
// wire shape), so the CloudEvents-like Envelope carries the event's own JSON
// as its data field verbatim; no per-event marshalling switch is needed.
package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// Topic is the integration topic facility-layout publishes its Published
// Language onto. The name follows the estate convention
// warehouse.<context>.events, matching the other services' integration
// topics so a single broker convention holds across the mesh.
const Topic = "warehouse.facility.events"

// Envelope is the CloudEvents-like wrapper shared across the warehouse-systems
// services' integration topics. data carries the domain event's own JSON.
type Envelope struct {
	EventId    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Source     string          `json:"source"`
	Data       json.RawMessage `json:"data"`
}

// Writer is the subset of *kafkago.Writer the Publisher needs, so tests can
// substitute a fake without a live broker.
type Writer interface {
	WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
}

// Encoded is one already-encoded, wire-ready Kafka message: the topic it
// belongs on (so a multi-topic outbox/relay can route it correctly), the
// partition key, and the JSON-marshalled envelope. It is the unit the
// transactional outbox (postgres.OutboxPublisher) stores and the outbox
// relay later hands to a Sink, so the direct-publish and outbox paths can
// never disagree about what a message looks like.
type Encoded struct {
	Topic     string
	EventType string
	Key       []byte
	Value     []byte
}

// Encoder turns one domain event into its Kafka wire form for one topic,
// without sending it. Both Publisher (this file, the integration topic)
// and AnalyticsPublisher (analytics_publisher.go) implement it, so
// postgres.NewOutboxPublisher can fan a single event out to both topics
// inside one transaction.
type Encoder interface {
	Encode(ctx context.Context, event shared.DomainEvent, eventId string) (Encoded, error)
}

// Publisher publishes facility-layout domain events onto Kafka. It satisfies
// ports.EventPublisher.
type Publisher struct {
	Writer Writer
	NewId  func() string
}

// NewPublisher constructs a Publisher writing to Topic on brokers. newId
// mints the envelope event_id (e.g. a UUID).
func NewPublisher(brokers []string, newId func() string) *Publisher {
	return &Publisher{
		Writer: &kafkago.Writer{
			Addr:                   kafkago.TCP(brokers...),
			Topic:                  Topic,
			Balancer:               &kafkago.LeastBytes{},
			AllowAutoTopicCreation: true,
		},
		NewId: newId,
	}
}

// Encode translates event into its Kafka wire form on Topic, without
// sending it. eventId is supplied by the caller (rather than minted here)
// so the outbox can persist the same id it will later publish under,
// making redelivery detectable by consumers.
func (p *Publisher) Encode(_ context.Context, event shared.DomainEvent, eventId string) (Encoded, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return Encoded{}, fmt.Errorf("kafka: marshal event data: %w", err)
	}
	env := Envelope{
		EventId:    eventId,
		EventType:  event.EventType(),
		OccurredAt: event.OccurredAt(),
		Source:     "facility-layout",
		Data:       data,
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return Encoded{}, fmt.Errorf("kafka: marshal envelope: %w", err)
	}
	return Encoded{Topic: Topic, EventType: event.EventType(), Key: []byte(aggregateKey(event)), Value: payload}, nil
}

// Publish emits event onto Topic wrapped in an Envelope. The message key is
// the event's aggregate identity (so all events for one aggregate land on the
// same partition, preserving per-aggregate order); event.EventType() is the
// service's Published Language type.
func (p *Publisher) Publish(ctx context.Context, event shared.DomainEvent) error {
	enc, err := p.Encode(ctx, event, p.NewId())
	if err != nil {
		return err
	}
	if err := p.send(ctx, enc); err != nil {
		return fmt.Errorf("kafka: publish %s: %w", event.EventName(), err)
	}
	return nil
}

// send writes one already-encoded message to Topic (the Writer's fixed
// topic — kafka-go rejects a message with Topic set when the Writer also
// has one, so enc.Topic is not applied here; RelaySink is the adapter
// that applies it per-message for a topic-less writer).
func (p *Publisher) send(ctx context.Context, enc Encoded) error {
	msg := kafkago.Message{Key: enc.Key, Value: enc.Value}
	return p.Writer.WriteMessages(ctx, msg)
}

// aggregateKey returns the partition/ordering key for an event: the identity
// of the aggregate that raised it. Falls back to the event type when an event
// has no obvious aggregate id, which still gives a stable, non-empty key.
func aggregateKey(event shared.DomainEvent) string {
	switch e := event.(type) {
	case shared.SiteRegistered:
		return e.SiteCode
	case shared.ZoneRegistered:
		return e.ZoneID
	case shared.AisleRegistered:
		return e.AisleID
	case shared.LocationTypeRegistered:
		return e.LocationType
	case shared.PlacementRuleDefined:
		return e.RuleID
	case shared.LocationSlotRegistered:
		return e.LocationCode
	case shared.LocationSlotDecommissioned:
		return e.LocationCode
	case shared.FacilityLayoutImported:
		return event.EventType()
	default:
		return event.EventType()
	}
}

// Close releases the underlying Kafka writer.
func (p *Publisher) Close() error {
	if w, ok := p.Writer.(*kafkago.Writer); ok {
		return w.Close()
	}
	return nil
}
