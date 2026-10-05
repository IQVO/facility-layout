package kafka

import (
	"context"
	"strings"
	"testing"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// traceHeadersFixture installs the composite propagator telemetry.Setup
// installs plus a real sampling tracer provider, and restores both, so a
// test gets an active, propagatable span context like production does.
type traceHeadersFixture struct {
	tp *sdktrace.TracerProvider
}

func newTraceHeadersFixture(t *testing.T) *traceHeadersFixture {
	t.Helper()
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTextMapPropagator(previous)
	})
	return &traceHeadersFixture{tp: tp}
}

// spanCtx starts a span the fixture's provider sampled and returns its
// context; the span ends with the test.
func (f *traceHeadersFixture) spanCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, span := f.tp.Tracer("test").Start(context.Background(), "publish")
	t.Cleanup(func() { span.End() })
	return ctx
}

// headerValue returns the last value of key, or "" when absent.
func headerValue(headers []kafka.Header, key string) string {
	value := ""
	for _, hdr := range headers {
		if hdr.Key == key {
			value = string(hdr.Value)
		}
	}
	return value
}

// TestInjectTraceContextWithSpan pins the happy path: an active span's
// W3C trace context lands in the message headers alongside the caller's
// own headers, untouched.
func TestInjectTraceContextWithSpan(t *testing.T) {
	f := newTraceHeadersFixture(t)
	ctx := f.spanCtx(t)

	headers := injectTraceContext(ctx, []kafka.Header{{Key: "content-type", Value: []byte("application/cloudevents+json")}})

	traceparent := headerValue(headers, "traceparent")
	if traceparent == "" {
		t.Fatalf("expected a traceparent header, got %+v", headers)
	}
	// The W3C traceparent is version-traceid(32)-spanid(16)-flags(2)
	// plus three dashes: 55 chars.
	if len(traceparent) != 55 || strings.Count(traceparent, "-") != 3 {
		t.Fatalf("expected a well-formed traceparent, got %q", traceparent)
	}
	if headers[0].Key != "content-type" || string(headers[0].Value) != "application/cloudevents+json" {
		t.Fatalf("expected the original headers preserved, got %+v", headers)
	}
}

// TestInjectTraceContextWithoutSpan pins the no-op: with no active span
// the input headers come back unchanged.
func TestInjectTraceContextWithoutSpan(t *testing.T) {
	newTraceHeadersFixture(t)

	headers := injectTraceContext(context.Background(), []kafka.Header{{Key: "content-type", Value: []byte("application/cloudevents+json")}})
	if len(headers) != 1 {
		t.Fatalf("expected the input headers unchanged, got %+v", headers)
	}
}

// TestInjectTraceContextReplacesStaleHeader pins that a pre-existing
// traceparent is replaced, not duplicated, by the fresh context.
func TestInjectTraceContextReplacesStaleHeader(t *testing.T) {
	f := newTraceHeadersFixture(t)
	ctx := f.spanCtx(t)

	headers := injectTraceContext(ctx, []kafka.Header{{Key: "traceparent", Value: []byte("stale")}})

	count := 0
	for _, hdr := range headers {
		if hdr.Key == "traceparent" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one traceparent header, got %d in %+v", count, headers)
	}
	if headerValue(headers, "traceparent") == "stale" {
		t.Fatal("expected the fresh trace context to replace the stale value")
	}
}
