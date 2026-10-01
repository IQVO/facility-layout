package kafka

import (
	"context"
	"fmt"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/facility-layout/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// AnalyticsTopic is the dedicated topic the analytics data product consumes. It
// is separate from the integration topic (Topic) so the OLTP integration
// contract and the analytical read-model stream evolve independently (ADR-0010).
const AnalyticsTopic = "warehouse.facility.analytics"

// AnalyticsPublisher publishes facility-layout domain events onto
// AnalyticsTopic as CloudEvents 1.0 (ADR-0024). The `type` is the same as on
// the integration topic (it names the occurrence); `dataschema`
// urn:warehouse:facility-layout:analytics:<EventName>:v1 names the analytics
// stream's shape (it replaces the retired schema_version field). `data` is
// the domain event's own JSON. It satisfies ports.EventPublisher and
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
// AnalyticsTopic on brokers. newId mints the CloudEvents `id` (a UUID).
//
// Balancer is kafkago.Hash, not LeastBytes -- see Publisher.NewPublisher's
// doc comment (publisher.go) for why LeastBytes silently ignores Key for
// partition routing; this writer had the same latent gap (ADR 0021).
func NewAnalyticsPublisher(brokers []string, newId func() string) *AnalyticsPublisher {
	return &AnalyticsPublisher{
		Writer: &kafkago.Writer{
			Addr:                   kafkago.TCP(brokers...),
			Topic:                  AnalyticsTopic,
			Balancer:               &kafkago.Hash{},
			AllowAutoTopicCreation: true,
		},
		NewId: newId,
	}
}

// Encode translates event into its Kafka wire form on AnalyticsTopic,
// without sending it. eventId is supplied by the caller so the outbox can
// persist the same id it will later publish under.
func (p *AnalyticsPublisher) Encode(_ context.Context, event shared.DomainEvent, eventId string) (Encoded, error) {
	return encodeCloudEvent(AnalyticsTopic, cloudevents.StreamAnalytics, event, eventId)
}

// Publish emits event onto AnalyticsTopic as a CloudEvent. The
// message key is the event's aggregate identity (so all events for one
// aggregate land on the same partition, preserving per-aggregate order).
func (p *AnalyticsPublisher) Publish(ctx context.Context, event shared.DomainEvent) error {
	enc, err := p.Encode(ctx, event, p.NewId())
	if err != nil {
		return err
	}
	if err := p.Send(ctx, enc); err != nil {
		return fmt.Errorf("kafka: publish %s analytics event: %w", event.EventName(), err)
	}
	return nil
}

// Send writes one already-encoded message to AnalyticsTopic (the Writer's
// fixed topic; see Publisher.send's doc comment for why enc.Topic is not
// applied here).
func (p *AnalyticsPublisher) Send(ctx context.Context, enc Encoded) error {
	msg := kafkago.Message{Key: enc.Key, Value: enc.Value, Headers: []kafkago.Header{cloudevents.ContentTypeHeader()}}
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
