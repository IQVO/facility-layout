package kafka

import (
	"context"
	"testing"

	kafkago "github.com/segmentio/kafka-go"
)

// traceRecordingWriter captures the messages a RelaySink writes.
type traceRecordingWriter struct {
	msgs []kafkago.Message
}

func (w *traceRecordingWriter) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	w.msgs = append(w.msgs, msgs...)
	return nil
}

// TestTraceHeadersCapturesTheActiveSpan pins the capture point of ADR-0038:
// the outbox publisher stores exactly what the global propagator injects for
// the use case's ctx, and nothing when no span is active.
func TestTraceHeadersCapturesTheActiveSpan(t *testing.T) {
	f := newTraceHeadersFixture(t)
	headers := TraceHeaders(f.spanCtx(t))
	if headerValue(headers, "traceparent") == "" {
		t.Fatalf("expected a traceparent, got %+v", headers)
	}
	if headerValue(headers, "content-type") != "" {
		t.Fatalf("TraceHeaders must carry trace context only, got %+v", headers)
	}
	if got := TraceHeaders(context.Background()); len(got) != 0 {
		t.Fatalf("no active span must capture nothing, got %+v", got)
	}
}

// TestRelaySinkSendsTheStoredTraceContextNotTheRelays pins ADR-0038 on the
// relay side: the message carries the CloudEvents content-type plus the
// trace headers stored on the row, and the relay pass's own context (here a
// different active span) never replaces them.
func TestRelaySinkSendsTheStoredTraceContextNotTheRelays(t *testing.T) {
	f := newTraceHeadersFixture(t)
	stored := TraceHeaders(f.spanCtx(t))
	relayCtx := f.spanCtx(t)
	w := &traceRecordingWriter{}
	sink := &RelaySink{Writer: w}

	if err := sink.Send(relayCtx, Encoded{Topic: "t", EventType: "e", Key: []byte("k"), Value: []byte("v"), Headers: stored}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(w.msgs) != 1 {
		t.Fatalf("got %d messages", len(w.msgs))
	}
	got := w.msgs[0].Headers
	if headerValue(got, "content-type") == "" {
		t.Fatalf("content-type lost: %+v", got)
	}
	if want := headerValue(stored, "traceparent"); headerValue(got, "traceparent") != want {
		t.Fatalf("traceparent = %q, want the stored %q", headerValue(got, "traceparent"), want)
	}
	count := 0
	for _, h := range got {
		if h.Key == "traceparent" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("exactly one traceparent expected, got %+v", got)
	}
}

// TestRelaySinkRowWithoutStoredTraceRelaysWithContentTypeOnly pins that a
// row with no stored trace context (written with no active span, or before
// migration 0010) relays unchanged: content-type only, no traceparent a
// consumer could mis-parent onto, even when the relay context has a span.
func TestRelaySinkRowWithoutStoredTraceRelaysWithContentTypeOnly(t *testing.T) {
	f := newTraceHeadersFixture(t)
	w := &traceRecordingWriter{}
	sink := &RelaySink{Writer: w}
	if err := sink.Send(f.spanCtx(t), Encoded{Topic: "t", EventType: "e", Key: []byte("k"), Value: []byte("v")}); err != nil {
		t.Fatalf("send: %v", err)
	}
	got := w.msgs[0].Headers
	if len(got) != 1 || got[0].Key != "content-type" {
		t.Fatalf("headers = %+v, want content-type only", got)
	}
	if string(w.msgs[0].Key) != "k" || string(w.msgs[0].Value) != "v" || w.msgs[0].Topic != "t" {
		t.Fatalf("message changed: %+v", w.msgs[0])
	}
}
