package kafka

import (
	"context"
	"fmt"

	kafkago "github.com/segmentio/kafka-go"
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
func NewRelaySink(brokers []string) *RelaySink {
	return &RelaySink{
		Writer: &kafkago.Writer{
			Addr:                   kafkago.TCP(brokers...),
			Balancer:               &kafkago.LeastBytes{},
			AllowAutoTopicCreation: true,
		},
	}
}

// Send writes one already-encoded outbox row to its own topic.
func (s *RelaySink) Send(ctx context.Context, enc Encoded) error {
	msg := kafkago.Message{Topic: enc.Topic, Key: enc.Key, Value: enc.Value}
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
