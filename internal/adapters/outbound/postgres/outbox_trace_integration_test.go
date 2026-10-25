//go:build integration

// D27 / gap 15d (ADR-0038, after order-management ADR 0041): the W3C trace
// context of the transaction that wrote an outbox row crosses the outbox to
// Kafka. Real Postgres and a real Kafka broker (testcontainers), never
// KAFKA_BROKERS or localhost.
package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/claudioed/facility-layout/internal/domain/site"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/application/usecases"
)

// installW3CTracing installs a real SDK tracer provider and the W3C
// trace-context propagator globally (what telemetry.Setup does in
// production), restoring the previous globals when the test ends.
func installW3CTracing(t *testing.T) trace.Tracer {
	t.Helper()
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	return tp.Tracer("outbox-trace-itest")
}

// startTraceKafka boots a disposable broker and creates the topics up front,
// so the relay's first write never meets a cold broker's "Unknown Topic Or
// Partition".
func startTraceKafka(t *testing.T, ctx context.Context, topics ...string) []string {
	t.Helper()
	container, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1",
		tckafka.WithClusterID(fmt.Sprintf("facility-layout-outbox-trace-itest-%d", time.Now().UnixNano())))
	if err != nil {
		t.Fatalf("start Kafka container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate Kafka container: %v", err)
		}
	})
	brokers, err := container.Brokers(ctx)
	if err != nil {
		t.Fatalf("resolve Kafka brokers: %v", err)
	}
	conn, err := kafkago.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		t.Fatalf("dial Kafka: %v", err)
	}
	defer func() { _ = conn.Close() }()
	for _, topic := range topics {
		if err := conn.CreateTopics(kafkago.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}); err != nil {
			t.Fatalf("create topic %s: %v", topic, err)
		}
	}
	return brokers
}

func readOneMessage(t *testing.T, ctx context.Context, brokers []string, topic string) kafkago.Message {
	t.Helper()
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers: brokers, Topic: topic, StartOffset: kafkago.FirstOffset, MaxWait: 2 * time.Second,
	})
	defer func() { _ = reader.Close() }()
	msg, err := reader.ReadMessage(ctx)
	if err != nil {
		t.Fatalf("read %s: %v", topic, err)
	}
	return msg
}

func traceHeaderValue(headers []kafkago.Header, key string) (string, bool) {
	for _, h := range headers {
		if h.Key == key {
			return string(h.Value), true
		}
	}
	return "", false
}

// storedTraceparents returns the traceparent stored in outbox_events.headers
// for every row of eventType ("" for a row with none).
func storedTraceparents(t *testing.T, ctx context.Context, pool *pgxpool.Pool, eventType string) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT headers FROM outbox_events WHERE event_type = $1 ORDER BY id`, eventType)
	if err != nil {
		t.Fatalf("read stored headers: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan stored headers: %v", err)
		}
		var hs []map[string]string
		if raw != nil {
			if err := json.Unmarshal(raw, &hs); err != nil {
				t.Fatalf("decode stored headers %s: %v", raw, err)
			}
		}
		tp := ""
		for _, h := range hs {
			if h["key"] == "traceparent" {
				tp = h["value"]
			}
		}
		out = append(out, tp)
	}
	return out
}

// TestOutboxTrace_TraceparentCrossesTheOutboxToKafka: a use case runs inside
// a request span; every outbox row it writes (integration AND analytics)
// stores that span's traceparent; a relay pass on a context WITHOUT the span
// sends it to a real broker; extracting the consumed message's headers
// yields the request's trace and span as a remote parent.
func TestOutboxTrace_TraceparentCrossesTheOutboxToKafka(t *testing.T) {
	tracer := installW3CTracing(t)
	pool := outboxDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	brokers := startTraceKafka(t, ctx, outboundkafka.Topic, outboundkafka.AnalyticsTopic)

	newID := func() string { return "evt-trace-1" }
	register := &usecases.RegisterSite{
		Sites:      postgres.NewSiteRepo(pool),
		Events:     postgres.NewOutboxPublisher(pool, newID, &outboundkafka.Publisher{NewId: newID}, &outboundkafka.AnalyticsPublisher{NewId: newID}),
		Clock:      fixedClock{t: time.Now().UTC().Truncate(time.Microsecond)},
		UnitOfWork: postgres.NewUnitOfWork(pool),
	}

	reqCtx, reqSpan := tracer.Start(ctx, "POST /sites", trace.WithSpanKind(trace.SpanKindServer))
	if _, err := register.Execute(reqCtx, "WH1", "Fulfilment Centre One", site.RoleUnspecified); err != nil {
		t.Fatalf("register: %v", err)
	}
	reqSpan.End()
	want := reqSpan.SpanContext()
	wantTraceparent := fmt.Sprintf("00-%s-%s-01", want.TraceID(), want.SpanID())

	const registered = "com.warehouse.wms.facility-layout.site.SiteRegistered"
	stored := storedTraceparents(t, ctx, pool, registered)
	if len(stored) != 2 || stored[0] != wantTraceparent || stored[1] != wantTraceparent {
		t.Fatalf("outbox rows' traceparents = %q, want both %q", stored, wantTraceparent)
	}

	sink := outboundkafka.NewRelaySink(brokers)
	t.Cleanup(func() { _ = sink.Close() })
	relay := postgres.NewOutboxRelay(pool, sink, slog.Default(), postgres.WithMaxAttempts(20))
	// The relay runs without the request's context: continuity must come
	// from the stored row, not from the caller.
	if n, err := relay.RelayOnce(context.Background()); err != nil || n != 2 {
		t.Fatalf("relay: n=%d err=%v", n, err)
	}

	msg := readOneMessage(t, ctx, brokers, outboundkafka.Topic)
	got, ok := traceHeaderValue(msg.Headers, "traceparent")
	if !ok || got != wantTraceparent {
		t.Fatalf("Kafka traceparent = %q (present=%v), want %q", got, ok, wantTraceparent)
	}
	if ct, _ := traceHeaderValue(msg.Headers, "content-type"); ct == "" {
		t.Fatalf("content-type header lost on the outbox path: %+v", msg.Headers)
	}

	consumerCtx := otel.GetTextMapPropagator().Extract(context.Background(), kafkaHeaderCarrier(msg.Headers))
	parent := trace.SpanContextFromContext(consumerCtx)
	if parent.TraceID() != want.TraceID() || parent.SpanID() != want.SpanID() || !parent.IsRemote() {
		t.Fatalf("consumer would parent on %s/%s (remote=%v), want the request span %s/%s",
			parent.TraceID(), parent.SpanID(), parent.IsRemote(), want.TraceID(), want.SpanID())
	}
}

// TestOutboxTrace_RowWithoutTraceHeadersRelaysUnchanged: a row with no
// stored trace context (NULL headers: written with no active span, or
// before migration 0010) relays with the same key and value and the
// content-type header only, never a traceparent a consumer could
// mis-parent onto.
func TestOutboxTrace_RowWithoutTraceHeadersRelaysUnchanged(t *testing.T) {
	installW3CTracing(t)
	pool := outboxDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	topic := fmt.Sprintf("warehouse.facility.itest-no-trace-%d", time.Now().UnixNano())
	brokers := startTraceKafka(t, ctx, topic)

	if _, err := pool.Exec(ctx, `INSERT INTO outbox_events (topic, event_type, key, value) VALUES ($1, 'com.example.Untraced', 'site-untraced', '{}')`, topic); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	if got := storedTraceparents(t, ctx, pool, "com.example.Untraced"); len(got) != 1 || got[0] != "" {
		t.Fatalf("legacy row stored traceparent %q", got)
	}

	sink := outboundkafka.NewRelaySink(brokers)
	t.Cleanup(func() { _ = sink.Close() })
	relay := postgres.NewOutboxRelay(pool, sink, slog.Default(), postgres.WithMaxAttempts(20))
	if n, err := relay.RelayOnce(ctx); err != nil || n != 1 {
		t.Fatalf("relay: n=%d err=%v", n, err)
	}

	msg := readOneMessage(t, ctx, brokers, topic)
	if string(msg.Key) != "site-untraced" || string(msg.Value) != `{}` {
		t.Fatalf("row relayed changed: key=%q value=%q", msg.Key, msg.Value)
	}
	if _, ok := traceHeaderValue(msg.Headers, "traceparent"); ok || len(msg.Headers) != 1 {
		t.Fatalf("a row without stored trace context must relay with content-type only, got %+v", msg.Headers)
	}
}

// TestOutboxTrace_NoActiveSpanStoresNoHeaders: a use case run with no span
// writes NULL headers, so the relay has nothing to forward.
func TestOutboxTrace_NoActiveSpanStoresNoHeaders(t *testing.T) {
	installW3CTracing(t)
	pool := outboxDB(t)
	ctx := context.Background()
	newID := func() string { return "evt-untraced" }
	register := &usecases.RegisterSite{
		Sites:      postgres.NewSiteRepo(pool),
		Events:     postgres.NewOutboxPublisher(pool, newID, &outboundkafka.Publisher{NewId: newID}),
		Clock:      fixedClock{t: time.Now().UTC()},
		UnitOfWork: postgres.NewUnitOfWork(pool),
	}
	if _, err := register.Execute(ctx, "WH2", "Two", site.RoleUnspecified); err != nil {
		t.Fatalf("register: %v", err)
	}
	var isNull bool
	if err := pool.QueryRow(ctx, `SELECT headers IS NULL FROM outbox_events`).Scan(&isNull); err != nil || !isNull {
		t.Fatalf("headers IS NULL = %v, err=%v", isNull, err)
	}
}

// kafkaHeaderCarrier adapts kafka-go headers to the OTel text-map carrier,
// as a consumer that starts a span does before Start.
type kafkaHeaderCarrier []kafkago.Header

func (c kafkaHeaderCarrier) Get(key string) string {
	v, _ := traceHeaderValue(c, key)
	return v
}

func (c kafkaHeaderCarrier) Set(string, string) {}

func (c kafkaHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for _, h := range c {
		keys = append(keys, h.Key)
	}
	return keys
}
