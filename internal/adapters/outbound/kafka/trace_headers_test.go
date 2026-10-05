package kafka

import (
	"context"
	"strings"
	"testing"

	"github.com/segmentio/kafka-go"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// TestInjectTraceContext pins ADR-0009's closed follow-up: outgoing Kafka
// messages carry the W3C trace context of the publishing context.
func TestInjectTraceContext(t *testing.T) {
	// Install the same composite propagator telemetry.Setup installs, and
	// restore whatever was there before so other tests are unaffected.
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })

	// A real (no-op-exporter) tracer provider: the noop tracer records no
	// remote span context, so traceparent would stay empty under it.
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	newCtx := func(t *testing.T) context.Context {
		t.Helper()
		ctx, span := tp.Tracer("test").Start(context.Background(), "publish")
		t.Cleanup(func() { span.End() })
		return ctx
	}

	t.Run("injects traceparent when a span is active", func(t *testing.T) {
		ctx := newCtx(t)

		headers := injectTraceContext(ctx, []kafka.Header{{Key: "content-type", Value: []byte("application/cloudevents+json")}})

		var traceparent string
		for _, hdr := range headers {
			if hdr.Key == "traceparent" {
				traceparent = string(hdr.Value)
			}
		}
		if traceparent == "" {
			t.Fatalf("expected a traceparent header, got %+v", headers)
		}
		// The W3C traceparent is version-traceid(32)-spanid(16)-flags(2)
		// plus three dashes: 55 chars.
		if len(traceparent) != 55 {
			t.Fatalf("expected a 55-char traceparent, got %q", traceparent)
		}
		if strings.Count(traceparent, "-") != 3 {
			t.Fatalf("expected 4 dashed fields in traceparent, got %q", traceparent)
		}
		// The caller's own headers survive untouched.
		if headers[0].Key != "content-type" || string(headers[0].Value) != "application/cloudevents+json" {
			t.Fatalf("expected the original headers preserved, got %+v", headers)
		}
	})

	t.Run("is a no-op without an active span", func(t *testing.T) {
		headers := injectTraceContext(context.Background(), []kafka.Header{{Key: "content-type", Value: []byte("application/cloudevents+json")}})
		if len(headers) != 1 {
			t.Fatalf("expected the input headers unchanged, got %+v", headers)
		}
	})

	t.Run("does not duplicate an existing traceparent", func(t *testing.T) {
		ctx := newCtx(t)

		headers := injectTraceContext(ctx, []kafka.Header{{Key: "traceparent", Value: []byte("stale")}})
		count := 0
		var value string
		for _, hdr := range headers {
			if hdr.Key == "traceparent" {
				count++
				value = string(hdr.Value)
			}
		}
		if count != 1 {
			t.Fatalf("expected exactly one traceparent header, got %d in %+v", count, headers)
		}
		if value == "stale" {
			t.Fatal("expected the fresh trace context to replace the stale value")
		}
	})
}
