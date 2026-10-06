// Package kafka provides the outbound adapter that publishes facility-layout
// domain events onto Kafka, satisfying ports.EventPublisher.
//
// facility-layout is an Open Host Service with a Published Language: its
// domain events ARE its integration contract, and every downstream service
// (inventory-storage, which consumes the zone and location-slot events into
// its location-classification cache, and warehouse-planning, which consumes
// the location-slot events into its storage/station tally) is a Conformist
// to them; the remaining events are published for future Conformists.
// Unlike a service that forwards a single enriched event, this publisher
// therefore emits EVERY domain event to the integration topic — the whole
// Published Language.
//
// Every message is a CloudEvents 1.0 event in structured content mode
// (ADR-0024), built by internal/adapters/kafka/cloudevents. The events
// carry no serialisation tags: the CloudEvent's `data` is the adapter DTO
// that cloudevents.WireData maps from each domain event, so the JSON wire
// shape is owned here, not by the domain.
package kafka

import (
	"context"
	"fmt"
	"strings"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/facility-layout/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// Topic is the integration topic facility-layout publishes its Published
// Language onto. The name follows the estate convention
// warehouse.<context>.events, matching the other services' integration
// topics so a single broker convention holds across the mesh.
const Topic = "warehouse.facility.events"

// Writer is the subset of *kafkago.Writer the Publisher needs, so tests can
// substitute a fake without a live broker.
type Writer interface {
	WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
}

// Encoded is one already-encoded, wire-ready Kafka message: the topic it
// belongs on (so a multi-topic outbox/relay can route it correctly), the
// partition key, and the structured-mode CloudEvent JSON. It is the unit the
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
// mints the CloudEvents `id` (a UUID).
//
// Balancer is kafkago.Hash (FNV-1a over Message.Key), not LeastBytes: this
// package's kafka-go dependency does NOT replicate Kafka's own key-hashing
// partitioner just because a message carries a non-nil Key — the Balancer
// alone decides partition placement, and LeastBytes routes purely by
// cumulative byte volume, ignoring Key entirely. Hash is the balancer that
// actually gives "same Key always maps to the same partition" (see ADR
// 0021, mirroring order-management PR #111 / ADR 0027, both prompted by
// warehouse-infra PR #42's 1->8 partition scaleup).
func NewPublisher(brokers []string, newId func() string) *Publisher {
	return &Publisher{
		Writer: &kafkago.Writer{
			BatchTimeout:           syncWriterBatchTimeout,
			RequiredAcks:           syncWriterRequiredAcks,
			Addr:                   kafkago.TCP(brokers...),
			Topic:                  Topic,
			Balancer:               &kafkago.Hash{},
			AllowAutoTopicCreation: true,
		},
		NewId: newId,
	}
}

// Encode translates event into its Kafka wire form on Topic, without
// sending it. eventId is supplied by the caller (rather than minted here)
// so the outbox can persist the same CloudEvents id it will later publish
// under, making redelivery detectable by consumers.
func (p *Publisher) Encode(_ context.Context, event shared.DomainEvent, eventId string) (Encoded, error) {
	return encodeCloudEvent(Topic, cloudevents.StreamEvents, event, eventId)
}

// encodeCloudEvent builds the structured-mode CloudEvent for event on one
// stream (integration or analytics). The `type` is the event's own
// Published Language type, `subject` its aggregate id, `time` its
// occurred-at, and `data` the event's wire DTO (cloudevents.WireData).
func encodeCloudEvent(topic, stream string, event shared.DomainEvent, eventId string) (Encoded, error) {
	entity, err := entityOf(event)
	if err != nil {
		return Encoded{}, err
	}
	data, err := cloudevents.WireData(event)
	if err != nil {
		return Encoded{}, fmt.Errorf("kafka: encode %s for %s: %w", event.EventName(), topic, err)
	}
	value, err := cloudevents.New(cloudevents.Spec{
		ID:        eventId,
		Entity:    entity,
		EventName: event.EventName(),
		Subject:   SubjectOf(event),
		Time:      event.OccurredAt(),
		Stream:    stream,
		Version:   1,
		Data:      data,
	})
	if err != nil {
		return Encoded{}, fmt.Errorf("kafka: encode %s for %s: %w", event.EventName(), topic, err)
	}
	return Encoded{Topic: topic, EventType: event.EventType(), Key: []byte(partitionKey(event, eventId)), Value: value}, nil
}

// partitionKey is the Kafka message key: the identity of the aggregate the
// event is about, so every event for one aggregate lands on one partition
// and keeps its order (the Hash balancer routes on it, ADR-0021).
// FacilityLayoutImported is the one exception: it summarises a whole bulk
// import and has no aggregate (its payload carries no site or zone), so no
// consumer can rely on its order against anything. It is keyed by its
// CloudEvents id instead, which spreads import batches over the partitions
// and stays stable across outbox redelivery of the same event (ADR-0032).
func partitionKey(event shared.DomainEvent, eventId string) string {
	if _, ok := event.(shared.FacilityLayoutImported); ok {
		return eventId
	}
	return aggregateKey(event)
}

// entityOf extracts the `<entity>` segment from the event's own Published
// Language type (com.warehouse.wms.facility-layout.<entity>.<EventName>)
// so the CloudEvent `type` is byte-identical to shared.DomainEvent.EventType.
func entityOf(event shared.DomainEvent) (string, error) {
	t := event.EventType()
	want := cloudevents.Type("", event.EventName())
	prefix, suffix, _ := strings.Cut(want, "..")
	if !strings.HasPrefix(t, prefix+".") || !strings.HasSuffix(t, "."+suffix) {
		return "", fmt.Errorf("kafka: event type %q is not in this service's CloudEvents namespace", t)
	}
	entity := strings.TrimSuffix(strings.TrimPrefix(t, prefix+"."), "."+suffix)
	if entity == "" || strings.Contains(entity, ".") {
		return "", fmt.Errorf("kafka: event type %q has no single entity segment", t)
	}
	return entity, nil
}

// Publish emits event onto Topic as a CloudEvent. The message key is
// the event's aggregate identity (so all events for one aggregate land on the
// same partition, preserving per-aggregate order); event.EventType() is the
// service's Published Language type.
func (p *Publisher) Publish(ctx context.Context, event shared.DomainEvent) error {
	enc, err := p.Encode(ctx, event, p.NewId())
	if err != nil {
		return err
	}
	if err := p.Send(ctx, enc); err != nil {
		return fmt.Errorf("kafka: publish %s: %w", event.EventName(), err)
	}
	return nil
}

// Send writes one already-encoded message to Topic (the Writer's fixed
// topic — kafka-go rejects a message with Topic set when the Writer also
// has one, so enc.Topic is not applied here; RelaySink is the adapter
// that applies it per-message for a topic-less writer). The W3C trace
// context of ctx is injected into the message headers (ADR-0009).
func (p *Publisher) Send(ctx context.Context, enc Encoded) error {
	msg := kafkago.Message{Key: enc.Key, Value: enc.Value, Headers: injectTraceContext(ctx, []kafkago.Header{cloudevents.ContentTypeHeader()})}
	return p.Writer.WriteMessages(ctx, msg)
}

// SubjectOf returns the CloudEvents `subject`: the id of the aggregate
// instance the event is about. It equals the partition key (aggregateKey)
// for every event except FacilityLayoutImported, which is a batch outcome
// with no single aggregate instance: its subject is the fixed batch
// identity ImportSubject, and its partition key is its CloudEvents id
// (partitionKey).
func SubjectOf(event shared.DomainEvent) string {
	if _, ok := event.(shared.FacilityLayoutImported); ok {
		return ImportSubject
	}
	return aggregateKey(event)
}

// ImportSubject is the CloudEvents `subject` of FacilityLayoutImported,
// which summarises a whole bulk-import call rather than one aggregate.
const ImportSubject = "layout-import"

// aggregateKey returns the identity of the aggregate that raised the event,
// used as the partition/ordering key (and as the CloudEvents `subject`).
// Every event has an aggregate identity except FacilityLayoutImported
// (see partitionKey/SubjectOf). The final fallback to the event type only
// guards a future event added without a case here: it still gives a
// non-empty key, and TestPublisher_KeysAreAggregateIdentityNotEventType
// rejects an event-type key for every golden case, so add one with the event.
func aggregateKey(event shared.DomainEvent) string {
	switch e := event.(type) {
	case shared.SiteRegistered:
		return e.SiteCode
	case shared.SiteCapabilityChanged:
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
	case shared.LocationGeometryUpdated:
		return e.LocationCode
	case shared.AisleGeometryUpdated:
		return e.AisleID
	case shared.FixedStructureRegistered:
		return e.StructureID
	case shared.CrossAisleRegistered:
		// A cross-aisle's identity is its (zone, from, to, bay) composite.
		return e.ZoneID + "/" + e.FromAisle + "-" + e.ToAisle + "@" + e.AtBay
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
