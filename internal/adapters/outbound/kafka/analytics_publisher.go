package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// AnalyticsTopic is the dedicated topic the analytics data product consumes. It
// is separate from the integration topic (Topic) so the OLTP integration
// contract and the analytical read-model stream evolve independently (ADR-0010).
const AnalyticsTopic = "warehouse.facility.analytics"

// analyticsSchemaVersion is the schema version stamped onto every analytics
// envelope this publisher emits.
const analyticsSchemaVersion = 1

// AnalyticsEnvelope is the Envelope v1 wrapper for the analytics stream. Like
// the integration Envelope it carries the domain event's own JSON as its data
// field: facility-layout's events already serialize themselves to their wire
// shape, so no per-event marshalling switch is needed. The only additions over
// the integration Envelope are the CloudEvents-style schema_version and the
// snake_case field naming the estate's analytics contract fixes.
type AnalyticsEnvelope struct {
	EventId       string          `json:"event_id"`
	EventType     string          `json:"event_type"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Source        string          `json:"source"`
	SchemaVersion int             `json:"schema_version"`
	Data          json.RawMessage `json:"data"`
}

// AnalyticsPublisher publishes facility-layout domain events onto
// AnalyticsTopic as an AnalyticsEnvelope. It satisfies ports.EventPublisher and
// is a SEPARATE adapter from Publisher: the integration publisher (publisher.go,
// ADR-0009) publishes the same events to warehouse.facility.events and is left
// untouched. The composition root fans out to BOTH so the integration and
// analytics streams stay independent.
//
// Consistent with the ADR-0009 integration publisher, this adapter is
// trace-free: facility-layout has no observability/OTel package for the
// analytics processes, so no producer span is opened and no trace headers are
// injected.
type AnalyticsPublisher struct {
	Writer Writer
	NewId  func() string
}

// NewAnalyticsPublisher constructs an AnalyticsPublisher writing to
// AnalyticsTopic on brokers. newId mints the envelope event_id (e.g. a UUID).
func NewAnalyticsPublisher(brokers []string, newId func() string) *AnalyticsPublisher {
	return &AnalyticsPublisher{
		Writer: &kafkago.Writer{
			Addr:                   kafkago.TCP(brokers...),
			Topic:                  AnalyticsTopic,
			Balancer:               &kafkago.LeastBytes{},
			AllowAutoTopicCreation: true,
		},
		NewId: newId,
	}
}

// Encode translates event into its Kafka wire form on AnalyticsTopic,
// without sending it. eventId is supplied by the caller so the outbox can
// persist the same id it will later publish under.
func (p *AnalyticsPublisher) Encode(_ context.Context, event shared.DomainEvent, eventId string) (Encoded, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return Encoded{}, fmt.Errorf("kafka: marshal analytics event data: %w", err)
	}
	env := AnalyticsEnvelope{
		EventId:       eventId,
		EventType:     event.EventType(),
		OccurredAt:    event.OccurredAt(),
		Source:        "facility-layout",
		SchemaVersion: analyticsSchemaVersion,
		Data:          data,
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return Encoded{}, fmt.Errorf("kafka: marshal analytics envelope: %w", err)
	}
	return Encoded{Topic: AnalyticsTopic, EventType: event.EventType(), Key: []byte(aggregateKey(event)), Value: payload}, nil
}

// Publish emits event onto AnalyticsTopic wrapped in an AnalyticsEnvelope. The
// message key is the event's aggregate identity (so all events for one
// aggregate land on the same partition, preserving per-aggregate order).
func (p *AnalyticsPublisher) Publish(ctx context.Context, event shared.DomainEvent) error {
	enc, err := p.Encode(ctx, event, p.NewId())
	if err != nil {
		return err
	}
	if err := p.send(ctx, enc); err != nil {
		return fmt.Errorf("kafka: publish %s analytics event: %w", event.EventName(), err)
	}
	return nil
}

// send writes one already-encoded message to AnalyticsTopic (the Writer's
// fixed topic; see Publisher.send's doc comment for why enc.Topic is not
// applied here).
func (p *AnalyticsPublisher) send(ctx context.Context, enc Encoded) error {
	msg := kafkago.Message{Key: enc.Key, Value: enc.Value}
	return p.Writer.WriteMessages(ctx, msg)
}

// Close releases the underlying Kafka writer.
func (p *AnalyticsPublisher) Close() error {
	if w, ok := p.Writer.(*kafkago.Writer); ok {
		return w.Close()
	}
	return nil
}

// Compile-time assertion that AnalyticsPublisher satisfies the outbound
// event-publishing port.
var _ ports.EventPublisher = (*AnalyticsPublisher)(nil)
