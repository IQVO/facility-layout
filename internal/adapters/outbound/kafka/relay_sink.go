package kafka

import (
	"context"
	"fmt"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/facility-layout/internal/adapters/kafka/cloudevents"
)

// RelaySink adapts a topic-less *kafkago.Writer to postgres.OutboxRelay's
// Sink port. Unlike Publisher/AnalyticsPublisher (which pin one fixed
// topic on their Writer), the relay drains rows for BOTH the integration
// topic and the analytics topic through one writer, so the topic must
// travel per-message via Encoded.Topic instead. kafka-go rejects a message
// with Topic set when the Writer also has one set, and vice versa — this
// is exactly why RelaySink's Writer carries no fixed Topic.
type RelaySink struct {
	Writer Writer
}

// NewRelaySink constructs a RelaySink writing to brokers with no fixed
// topic.
//
// Balancer is kafkago.Hash, not LeastBytes -- see Publisher.NewPublisher's
// doc comment (publisher.go) for why LeastBytes silently ignores Key for
// partition routing; this writer had the same latent gap (ADR 0021).
func NewRelaySink(brokers []string) *RelaySink {
	return &RelaySink{
		Writer: &kafkago.Writer{
			BatchTimeout:           syncWriterBatchTimeout,
			RequiredAcks:           syncWriterRequiredAcks,
			Addr:                   kafkago.TCP(brokers...),
			Balancer:               &kafkago.Hash{},
			AllowAutoTopicCreation: true,
		},
	}
}

// Send writes one already-encoded outbox row to its own topic. Every row's
// value is a structured-mode CloudEvent (encoded once by Publisher/
// AnalyticsPublisher.Encode, with the id persisted in the row), so the
// CloudEvents content-type header is attached here on the way out. The
// W3C trace context of ctx is injected into the message headers
// (ADR-0009): the relay runs outside the request that wrote the row, so
// the trace it starts here is the producer side of the publish→consume
// hop.
func (s *RelaySink) Send(ctx context.Context, enc Encoded) error {
	msg := kafkago.Message{Topic: enc.Topic, Key: enc.Key, Value: enc.Value, Headers: injectTraceContext(ctx, []kafkago.Header{cloudevents.ContentTypeHeader()})}
	if err := s.Writer.WriteMessages(ctx, msg); err != nil {
		return fmt.Errorf("kafka: relay send %s on %s: %w", enc.EventType, enc.Topic, err)
	}
	return nil
}

// Close releases the underlying Kafka writer.
func (s *RelaySink) Close() error {
	if w, ok := s.Writer.(*kafkago.Writer); ok {
		return w.Close()
	}
	return nil
}
