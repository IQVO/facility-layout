//go:build integration

package kafka_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"

	inboundkafka "github.com/claudioed/facility-layout/internal/adapters/inbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/kafka/cloudevents"
)

// These tests drive the real DLQ/retry logic (ADR-0020, mirroring
// order-management's ADR-0025 §DLQ verbatim) against a REAL Kafka broker the
// test itself starts, via testcontainers. Nothing external is assumed: no
// KAFKA_BROKERS, no localhost:9092, no cluster — CI's integration job
// provisions Postgres only and never runs Kafka, and pointing tests at the
// shared fleet cluster broker risks consumer-group collisions with live
// deployments elsewhere in this fleet.

var (
	sharedBrokers   []string
	sharedContainer testcontainers.Container
)

// TestMain owns the package-wide broker lifecycle: one container is started
// for the whole package (containers are slow to boot) and torn down once,
// after every test has run. Isolation between tests comes from each one
// using its own unique topic.
func TestMain(m *testing.M) {
	code := m.Run()
	if sharedContainer != nil {
		if err := testcontainers.TerminateContainer(sharedContainer); err != nil {
			fmt.Fprintf(os.Stderr, "terminate kafka container: %v\n", err)
		}
	}
	os.Exit(code)
}

func startBroker(t *testing.T) []string {
	t.Helper()
	if sharedBrokers != nil {
		return sharedBrokers
	}

	ctx := context.Background()
	container, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1",
		tckafka.WithClusterID(fmt.Sprintf("facility-analytics-dlq-itest-%d", time.Now().UnixNano())),
	)
	if err != nil {
		t.Fatalf("start kafka container: %v", err)
	}
	sharedContainer = container

	brokers, err := container.Brokers(ctx)
	if err != nil {
		t.Fatalf("resolve kafka brokers: %v", err)
	}
	sharedBrokers = brokers
	return brokers
}

func uniqueTopic(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("warehouse.facility.analytics.itest-%d", time.Now().UnixNano())
}

// createTopic creates topic explicitly rather than relying on
// auto-creation, then waits until the partition leader is resolvable so the
// first read/write after this call never races topic creation.
func createTopic(t *testing.T, brokerList []string, topic string) {
	t.Helper()
	conn, err := kafkago.Dial("tcp", brokerList[0])
	if err != nil {
		t.Fatalf("dial %s: %v", brokerList[0], err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.CreateTopics(kafkago.TopicConfig{
		Topic: topic, NumPartitions: 1, ReplicationFactor: 1,
	}); err != nil {
		t.Fatalf("create topic %s: %v", topic, err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		partitions, err := conn.ReadPartitions(topic)
		if err == nil && len(partitions) > 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("topic %s never became readable", topic)
}

// poisonProjection is a report.ProjectionStore test double whose
// ApplySiteRegistered method, when called with an event id equal to
// exactly poisonEventID, ALWAYS fails ApplySiteRegistered (the injected
// failure never has a side effect to undo across retries — mirroring
// order-management's own DLQ integration test, which injects the failure
// at the very first call the use case makes). Every other event id is
// delegated to a real in-memory-style tally so the "well-formed message
// processed without delay" assertion has something concrete to check.
//
// applied and its guarding mutex mu are touched from two goroutines in
// these tests: the AnalyticsConsumer.Run goroutine (writer, via
// ApplySiteRegistered) and the test's own polling goroutine (reader, via
// Applied()) — both are required so `go test -race` (part of this fleet's
// CI integration job) stays clean.
type poisonProjection struct {
	poisonEventID string

	mu      sync.Mutex
	applied []string
}

func (p *poisonProjection) ApplySiteRegistered(_ context.Context, eventId, _ string, _ time.Time) error {
	if eventId == p.poisonEventID {
		return fmt.Errorf("simulated poison-message infrastructure failure for event %s", eventId)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.applied = append(p.applied, eventId)
	return nil
}

// Applied returns a snapshot copy of the event ids applied so far, safe to
// read concurrently with ApplySiteRegistered's writes.
func (p *poisonProjection) Applied() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.applied...)
}
func (p *poisonProjection) ApplyZoneRegistered(context.Context, string, string, time.Time) error {
	return nil
}
func (p *poisonProjection) ApplyAisleRegistered(context.Context, string, string, time.Time) error {
	return nil
}
func (p *poisonProjection) ApplyLocationTypeRegistered(context.Context, string, string, time.Time) error {
	return nil
}
func (p *poisonProjection) ApplyPlacementRuleDefined(context.Context, string, string, time.Time) error {
	return nil
}
func (p *poisonProjection) ApplyLocationSlotRegistered(context.Context, string, string, time.Time) error {
	return nil
}
func (p *poisonProjection) ApplyLocationSlotDecommissioned(context.Context, string, string, time.Time) error {
	return nil
}
func (p *poisonProjection) ApplyFacilityLayoutImported(context.Context, string, string, int, int, int, time.Time) error {
	return nil
}

// itestProcessed is a minimal in-memory ProcessedEvents for this test file
// (analytics_consumer_test.go's fakeProcessed is unexported to the sibling
// _test.go in the same package, but this file uses the integration build
// tag and a distinct package name, so it needs its own copy).
type itestProcessed struct {
	seen map[string]bool
}

func newItestProcessed() *itestProcessed { return &itestProcessed{seen: map[string]bool{}} }

func (p *itestProcessed) IsProcessed(_ context.Context, eventId string) (bool, error) {
	return p.seen[eventId], nil
}

func (p *itestProcessed) MarkProcessed(_ context.Context, eventId string) (bool, error) {
	if p.seen[eventId] {
		return false, nil
	}
	p.seen[eventId] = true
	return true, nil
}

func siteRegisteredCloudEventJSON(t *testing.T, eventID, siteCode string) []byte {
	t.Helper()
	b, err := cloudevents.New(cloudevents.Spec{
		ID:        eventID,
		Entity:    "site",
		EventName: "SiteRegistered",
		Subject:   siteCode,
		Time:      time.Now().UTC(),
		Stream:    cloudevents.StreamAnalytics,
		Data:      map[string]any{"siteCode": siteCode},
	})
	if err != nil {
		t.Fatalf("build CloudEvent: %v", err)
	}
	return b
}

func headerValue(headers []kafkago.Header, key string) string {
	for _, h := range headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

// TestAnalyticsConsumer_PoisonMessage_GoesToDeadLetterTopicWithoutBlockingPartition
// is the ADR-0020 §DLQ acceptance test: a message whose handler ALWAYS
// fails must, after exactly maxHandlerAttempts (3) in-process retries, land
// on "<topic>.dlq" with the raw original payload plus error-context
// headers, and the consumer must commit past it and keep processing — a
// well-formed message published right after the poison one must be
// applied without delay, proving the partition was never blocked on the
// one bad message.
func TestAnalyticsConsumer_PoisonMessage_GoesToDeadLetterTopicWithoutBlockingPartition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	brokers := startBroker(t)
	topic := uniqueTopic(t)
	dlqTopic := topic + ".dlq"
	createTopic(t, brokers, topic)
	createTopic(t, brokers, dlqTopic)

	poisonEventID := fmt.Sprintf("evt-dlq-poison-%d", time.Now().UnixNano())
	healthyEventID := fmt.Sprintf("evt-dlq-good-%d", time.Now().UnixNano())

	projection := &poisonProjection{poisonEventID: poisonEventID}
	processed := newItestProcessed()

	consumer := inboundkafka.NewAnalyticsConsumer(brokers, topic, projection, processed, slog.Default())
	defer func() { _ = consumer.Close() }()

	consumeCtx, consumeCancel := context.WithCancel(ctx)
	runErr := make(chan error, 1)
	go func() { runErr <- consumer.Run(consumeCtx) }()
	t.Cleanup(func() {
		select {
		case err := <-runErr:
			t.Logf("consumer.Run returned: %v", err)
		default:
		}
	})

	// Start reading the DLQ topic BEFORE publishing, so the poison
	// message's eventual dead-letter write is never missed to a race.
	dlqReader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:     brokers,
		Topic:       dlqTopic,
		GroupID:     fmt.Sprintf("dlq-reader-%d", time.Now().UnixNano()),
		StartOffset: kafkago.FirstOffset,
	})
	defer func() { _ = dlqReader.Close() }()

	writer := &kafkago.Writer{Addr: kafkago.TCP(brokers...), Topic: topic}
	defer func() { _ = writer.Close() }()
	if err := writer.WriteMessages(ctx, kafkago.Message{
		Key:   []byte(poisonEventID),
		Value: siteRegisteredCloudEventJSON(t, poisonEventID, "WH-POISON"),
	}); err != nil {
		t.Fatalf("publish poison SiteRegistered: %v", err)
	}

	dlqCtx, dlqCancel := context.WithTimeout(ctx, 60*time.Second)
	defer dlqCancel()
	dlqMsg, err := dlqReader.ReadMessage(dlqCtx)
	if err != nil {
		t.Fatalf("read DLQ message: %v", err)
	}
	if string(dlqMsg.Key) != poisonEventID {
		t.Errorf("DLQ message key = %q, want %q (raw key preserved)", string(dlqMsg.Key), poisonEventID)
	}
	var dlqPayload map[string]any
	if err := json.Unmarshal(dlqMsg.Value, &dlqPayload); err != nil {
		t.Fatalf("DLQ message value is not the raw original JSON payload: %v", err)
	}
	if dlqPayload["id"] != poisonEventID {
		t.Errorf("DLQ payload id = %v, want %q -- payload must be byte-identical to the original for manual replay", dlqPayload["event_id"], poisonEventID)
	}
	if got := headerValue(dlqMsg.Headers, "x-dlq-source-topic"); got != topic {
		t.Errorf("x-dlq-source-topic = %q, want %q", got, topic)
	}
	if h := headerValue(dlqMsg.Headers, "x-dlq-error"); h == "" {
		t.Error("DLQ message missing x-dlq-error header with failure context")
	}
	if h := headerValue(dlqMsg.Headers, "x-dlq-failed-at"); h == "" {
		t.Error("DLQ message missing x-dlq-failed-at header")
	}

	// Now publish a well-formed message right after the poison one, and
	// confirm it is applied without delay -- proving the partition was
	// not blocked behind the poison message.
	if err := writer.WriteMessages(ctx, kafkago.Message{
		Key:   []byte(healthyEventID),
		Value: siteRegisteredCloudEventJSON(t, healthyEventID, "WH-HEALTHY"),
	}); err != nil {
		t.Fatalf("publish well-formed SiteRegistered: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 30*time.Second)
	defer waitCancel()
	for {
		found := false
		for _, id := range projection.Applied() {
			if id == healthyEventID {
				found = true
				break
			}
		}
		if found {
			break
		}
		select {
		case <-waitCtx.Done():
			t.Fatal("healthy message was never applied -- partition appears blocked behind the poison message")
		case <-time.After(200 * time.Millisecond):
		}
	}

	consumeCancel()
	select {
	case err := <-runErr:
		if err != nil && ctx.Err() == nil {
			t.Errorf("run consumer: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("consumer did not stop after context cancellation")
	}

	// The poison event was never applied -- the DLQ path commits the
	// offset (so the partition advances) without ever having driven a
	// successful projection apply.
	for _, id := range projection.Applied() {
		if id == poisonEventID {
			t.Errorf("poison event %s should never have been applied to the projection", poisonEventID)
		}
	}
}

// TestAnalyticsConsumer_LegacyFlatEnvelope_DeadLetteredWithoutRetry proves a
// retired flat-envelope message is deterministic poison (ADR-0024): it is
// dead-lettered unparsed, never applied, and the next valid CloudEvent on
// the same partition is still applied.
func TestAnalyticsConsumer_LegacyFlatEnvelope_DeadLetteredWithoutRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	brokers := startBroker(t)
	topic := uniqueTopic(t)
	dlqTopic := topic + ".dlq"
	createTopic(t, brokers, topic)
	createTopic(t, brokers, dlqTopic)

	healthyEventID := fmt.Sprintf("evt-ce-good-%d", time.Now().UnixNano())
	projection := &poisonProjection{}
	consumer := inboundkafka.NewAnalyticsConsumer(brokers, topic, projection, newItestProcessed(), slog.Default())
	defer func() { _ = consumer.Close() }()

	consumeCtx, consumeCancel := context.WithCancel(ctx)
	defer consumeCancel()
	go func() { _ = consumer.Run(consumeCtx) }()

	dlqReader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:     brokers,
		Topic:       dlqTopic,
		GroupID:     fmt.Sprintf("dlq-legacy-reader-%d", time.Now().UnixNano()),
		StartOffset: kafkago.FirstOffset,
	})
	defer func() { _ = dlqReader.Close() }()

	legacy := []byte(`{"event_id":"legacy-1","event_type":"com.warehouse.wms.facility-layout.site.SiteRegistered","occurred_at":"2026-05-01T08:00:00Z","source":"facility-layout","schema_version":1,"data":{"siteCode":"WH-LEGACY"}}`)
	writer := &kafkago.Writer{Addr: kafkago.TCP(brokers...), Topic: topic}
	defer func() { _ = writer.Close() }()
	msgs := []kafkago.Message{
		{Key: []byte("legacy-1"), Value: legacy},
		{Key: []byte(healthyEventID), Value: siteRegisteredCloudEventJSON(t, healthyEventID, "WH-HEALTHY")},
	}
	// A just-created topic's metadata can lag on the broker; retry the
	// transient "unknown topic" briefly rather than flake.
	var werr error
	for attempt := 0; attempt < 20; attempt++ {
		if werr = writer.WriteMessages(ctx, msgs...); werr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if werr != nil {
		t.Fatalf("publish: %v", werr)
	}

	dlqCtx, dlqCancel := context.WithTimeout(ctx, 60*time.Second)
	defer dlqCancel()
	dlqMsg, err := dlqReader.ReadMessage(dlqCtx)
	if err != nil {
		t.Fatalf("read DLQ message: %v", err)
	}
	if string(dlqMsg.Value) != string(legacy) {
		t.Errorf("DLQ value = %s, want the raw legacy payload", dlqMsg.Value)
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 30*time.Second)
	defer waitCancel()
	for {
		applied := projection.Applied()
		if len(applied) == 1 && applied[0] == healthyEventID {
			break
		}
		if len(applied) > 1 {
			t.Fatalf("unexpected applies: %v", applied)
		}
		select {
		case <-waitCtx.Done():
			t.Fatalf("healthy CloudEvent never applied after the legacy message (applied=%v)", projection.Applied())
		case <-time.After(200 * time.Millisecond):
		}
	}
}
