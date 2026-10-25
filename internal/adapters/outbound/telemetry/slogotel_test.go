package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/telemetry"
)

// spanContext builds a valid, non-recording span context — enough for the
// handler under test, which only reads the ids.
func spanContext(t *testing.T) trace.SpanContext {
	t.Helper()
	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
}

// logAndDecode runs one record through a JSON handler wrapped in the trace
// context handler, and returns the decoded log line.
func logAndDecode(t *testing.T, ctx context.Context) map[string]any {
	t.Helper()

	var buf bytes.Buffer
	logger := slog.New(telemetry.NewTraceContextHandler(
		slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
	))
	logger.InfoContext(ctx, "http request", "status", 201)

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not valid JSON (%q): %v", buf.String(), err)
	}
	return line
}

func TestTraceContextHandlerAttachesIDsInsideASpan(t *testing.T) {
	sc := spanContext(t)
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	line := logAndDecode(t, ctx)

	if got := line["trace_id"]; got != sc.TraceID().String() {
		t.Fatalf("expected trace_id %q, got %v", sc.TraceID(), got)
	}
	if got := line["span_id"]; got != sc.SpanID().String() {
		t.Fatalf("expected span_id %q, got %v", sc.SpanID(), got)
	}
	if got := line["msg"]; got != "http request" {
		t.Fatalf("the wrapped handler must still see the record: got msg %v", got)
	}
}

func TestTraceContextHandlerOmitsIDsOutsideASpan(t *testing.T) {
	line := logAndDecode(t, context.Background())

	if _, ok := line["trace_id"]; ok {
		t.Fatalf("expected no trace_id without an active span, got %v", line)
	}
	if _, ok := line["span_id"]; ok {
		t.Fatalf("expected no span_id without an active span, got %v", line)
	}
}

func TestTraceContextHandlerPreservesAttrsAndGroups(t *testing.T) {
	var buf bytes.Buffer
	handler := telemetry.NewTraceContextHandler(
		slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
	)
	logger := slog.New(handler).With("service", "facility-layout").WithGroup("http")
	logger.Info("ready", "addr", ":8080")

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not valid JSON (%q): %v", buf.String(), err)
	}
	if line["service"] != "facility-layout" {
		t.Fatalf("WithAttrs was dropped: %v", line)
	}
	group, ok := line["http"].(map[string]any)
	if !ok || group["addr"] != ":8080" {
		t.Fatalf("WithGroup was dropped: %v", line)
	}
}

func TestTraceContextHandlerEnabledDelegates(t *testing.T) {
	handler := telemetry.NewTraceContextHandler(
		slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn}),
	)
	if handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("expected info to be disabled by the wrapped handler's level")
	}
	if !handler.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("expected error to be enabled")
	}
}

func TestNewLoggerWritesJSONAtTheConfiguredLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := telemetry.NewLogger(&buf, "warn", "facility-layout")

	logger.Info("dropped")
	if buf.Len() != 0 {
		t.Fatalf("expected info to be filtered out at warn, got %q", buf.String())
	}

	logger.Warn("kept", "reason", "test")
	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
		t.Fatalf("log line is not valid JSON (%q): %v", buf.String(), err)
	}
	if line["msg"] != "kept" || line["reason"] != "test" {
		t.Fatalf("unexpected log line %v", line)
	}
}

func TestNewLoggerCorrelatesWithTheActiveSpan(t *testing.T) {
	var buf bytes.Buffer
	logger := telemetry.NewLogger(&buf, "info", "facility-layout")

	sc := spanContext(t)
	logger.InfoContext(trace.ContextWithSpanContext(context.Background(), sc), "http request")

	if !strings.Contains(buf.String(), sc.TraceID().String()) {
		t.Fatalf("expected the trace id in the log line, got %q", buf.String())
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{" Info ", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"WARNING", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},
		{"nonsense", slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := telemetry.ParseLevel(tt.in); got != tt.want {
				t.Fatalf("ParseLevel(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
