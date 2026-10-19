//go:build integration

package kafka_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"

	inboundkafka "github.com/claudioed/facility-layout/internal/adapters/inbound/kafka"
)

// blockingProjection parks every apply call until the handler's context is
// cancelled, then reports that cancellation -- exactly what a real
// projection store does when the pod is told to stop mid-write.
type blockingProjection struct {
	entered chan struct{}
}

func (p *blockingProjection) block(ctx context.Context) error {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}

func (p *blockingProjection) ApplySiteRegistered(ctx context.Context, _, _ string, _ time.Time) error {
	return p.block(ctx)
}
func (p *blockingProjection) ApplyZoneRegistered(ctx context.Context, _, _ string, _ time.Time) error {
	return p.block(ctx)
}
func (p *blockingProjection) ApplyAisleRegistered(ctx context.Context, _, _ string, _ time.Time) error {
	return p.block(ctx)
}
func (p *blockingProjection) ApplyLocationTypeRegistered(ctx context.Context, _, _ string, _ time.Time) error {
	return p.block(ctx)
}
func (p *blockingProjection) ApplyPlacementRuleDefined(ctx context.Context, _, _ string, _ time.Time) error {
	return p.block(ctx)
}
func (p *blockingProjection) ApplyLocationSlotRegistered(ctx context.Context, _, _ string, _ time.Time) error {
	return p.block(ctx)
}
func (p *blockingProjection) ApplyLocationSlotDecommissioned(ctx context.Context, _, _ string, _ time.Time) error {
	return p.block(ctx)
}
func (p *blockingProjection) ApplyFacilityLayoutImported(ctx context.Context, _, _ string, _, _, _ int, _ time.Time) error {
	return p.block(ctx)
}

// startOwnBroker starts a broker private to this test (testcontainers), so
// the shutdown it provokes can never disturb the package's shared broker.
func startOwnBroker(ctx context.Context, t *testing.T) []string {
	t.Helper()
	container, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1",
		tckafka.WithClusterID(fmt.Sprintf("facility-analytics-shutdown-itest-%d", time.Now().UnixNano())))
	if err != nil {
		t.Fatalf("start Kafka container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Errorf("terminate Kafka container: %v", err)
		}
	})
	brokers, err := container.Brokers(ctx)
	if err != nil {
		t.Fatalf("resolve Kafka brokers: %v", err)
	}
	return brokers
}

// TestAnalyticsConsumer_ShutdownMidHandle_IsCleanAndDeadLettersNothing pins
// the shutdown contract: cancelling Run's context while a healthy message is
// being handled is a clean stop (Run returns nil), and that message is NOT
// dead-lettered -- its offset stays uncommitted, so it is redelivered on the
// next start and the processed-events check absorbs the replay. Before the
// fix the retry wrapper's cancellation error was routed into the dead-letter
// path as if the message were poison, and Run surfaced the failed DLQ
// publish / commit on the cancelled context as an error.
func TestAnalyticsConsumer_ShutdownMidHandle_IsCleanAndDeadLettersNothing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	brokers := startOwnBroker(ctx, t)
	topic := uniqueTopic(t)
	dlqTopic := topic + ".dlq"
	createTopic(t, brokers, topic)
	createTopic(t, brokers, dlqTopic)

	projection := &blockingProjection{entered: make(chan struct{}, 1)}
	consumer := inboundkafka.NewAnalyticsConsumer(brokers, topic, topic+"-group", projection, newItestProcessed(), slog.Default())
	defer func() { _ = consumer.Close() }()

	consumeCtx, consumeCancel := context.WithCancel(ctx)
	defer consumeCancel()
	runErr := make(chan error, 1)
	go func() { runErr <- consumer.Run(consumeCtx) }()

	writer := &kafkago.Writer{Addr: kafkago.TCP(brokers...), Topic: topic}
	defer func() { _ = writer.Close() }()
	eventID := fmt.Sprintf("evt-shutdown-%d", time.Now().UnixNano())
	// A just-created topic's metadata can lag on the shared broker; retry the
	// transient "unknown topic" briefly rather than flake.
	var werr error
	for attempt := 0; attempt < 20; attempt++ {
		if werr = writer.WriteMessages(ctx, kafkago.Message{
			Key:   []byte(eventID),
			Value: siteRegisteredCloudEventJSON(t, eventID, "WH-SHUTDOWN"),
		}); werr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if werr != nil {
		t.Fatalf("publish SiteRegistered: %v", werr)
	}

	select {
	case <-projection.entered:
	case <-time.After(60 * time.Second):
		t.Fatal("the consumer never started applying the message")
	}
	consumeCancel()

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run after its context was cancelled mid-handle = %v, want nil (clean shutdown)", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("consumer did not stop after context cancellation")
	}

	// Close flushes any DLQ batch a cancelled WriteMessages had already
	// enqueued, so a dead-letter that "failed" on the cancelled context but
	// still reached the broker is caught below.
	_ = consumer.Close()

	dlqReader := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: dlqTopic, Partition: 0, MinBytes: 1, MaxBytes: 10e6})
	defer func() { _ = dlqReader.Close() }()
	readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readCancel()
	if msg, err := dlqReader.ReadMessage(readCtx); err == nil {
		t.Fatalf("a healthy message was dead-lettered on shutdown: key=%q headers=%v", string(msg.Key), msg.Headers)
	}
}
