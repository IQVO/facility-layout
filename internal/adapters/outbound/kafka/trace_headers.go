package kafka

import (
	"context"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// headerCarrier adapts a *[]kafka.Header to the OTel TextMapCarrier
// interface, so the process-wide W3C trace-context propagator (installed
// by telemetry.Setup) can inject `traceparent` / `tracestate` / `baggage`
// into outgoing Kafka headers. It holds a pointer because Inject may add
// keys the slice does not carry yet.
//
// kafka-go represents a header value as []byte; the carrier converts via
// string, matching how every W3C field is textual.
type headerCarrier struct {
	headers *[]kafka.Header
}

var _ propagation.TextMapCarrier = headerCarrier{}

func (c headerCarrier) Get(key string) string {
	for _, hdr := range *c.headers {
		if hdr.Key == key {
			return string(hdr.Value)
		}
	}
	return ""
}

func (c headerCarrier) Set(key, value string) {
	for i, hdr := range *c.headers {
		if hdr.Key == key {
			(*c.headers)[i].Value = []byte(value)
			return
		}
	}
	*c.headers = append(*c.headers, kafka.Header{Key: key, Value: []byte(value)})
}

func (c headerCarrier) Keys() []string {
	keys := make([]string, 0, len(*c.headers))
	for _, hdr := range *c.headers {
		keys = append(keys, hdr.Key)
	}
	return keys
}

// injectTraceContext returns headers plus the W3C trace context of ctx
// (traceparent, tracestate, baggage — whatever the process-wide
// propagator carries), closing ADR-0009's follow-up "W3C trace context
// not injected into Kafka headers": a consumer that extracts the context
// links its spans to the producer's, so a publish→consume hop is one
// trace.
//
// It is a no-op when no trace is active in ctx or no propagator has been
// installed (unit tests, in-memory runs) — the returned slice then equals
// the input and existing messages are unchanged on the wire.
func injectTraceContext(ctx context.Context, headers []kafka.Header) []kafka.Header {
	out := headers
	otel.GetTextMapPropagator().Inject(ctx, headerCarrier{headers: &out})
	return out
}
