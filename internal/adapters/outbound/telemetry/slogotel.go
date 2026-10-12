package telemetry

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/trace"
)

// TraceContextHandler wraps a slog.Handler and stamps every record emitted
// inside an active span with that span's trace_id and span_id. That is what
// makes a log line and a trace joinable: given a trace in Jaeger you can
// find its logs, and given a log line you can open its trace.
//
// Records logged with a context that carries no valid span context pass
// through untouched — no empty trace_id fields cluttering startup logs.
type TraceContextHandler struct {
	inner slog.Handler
}

// NewTraceContextHandler wraps inner with trace correlation.
func NewTraceContextHandler(inner slog.Handler) *TraceContextHandler {
	return &TraceContextHandler{inner: inner}
}

// Enabled reports whether the wrapped handler handles records at level.
func (h *TraceContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle appends trace_id/span_id when ctx carries a valid span context,
// then delegates to the wrapped handler.
func (h *TraceContextHandler) Handle(ctx context.Context, record slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		record = record.Clone()
		record.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.inner.Handle(ctx, record)
}

// WithAttrs returns a handler whose records also carry attrs.
func (h *TraceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &TraceContextHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup returns a handler whose records are nested under name.
func (h *TraceContextHandler) WithGroup(name string) slog.Handler {
	return &TraceContextHandler{inner: h.inner.WithGroup(name)}
}

// fanoutHandler broadcasts every record to several handlers, so the same
// log line reaches both the container's stdout (as JSON) and the OTel
// Collector (as an OTLP log record) without the call sites knowing.
type fanoutHandler struct {
	handlers []slog.Handler
}

func (h *fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, inner := range h.handlers {
		if inner.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *fanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, inner := range h.handlers {
		if !inner.Enabled(ctx, record.Level) {
			continue
		}
		if err := inner.Handle(ctx, record.Clone()); err != nil {
			return err
		}
	}
	return nil
}

func (h *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, 0, len(h.handlers))
	for _, inner := range h.handlers {
		next = append(next, inner.WithAttrs(attrs))
	}
	return &fanoutHandler{handlers: next}
}

func (h *fanoutHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, 0, len(h.handlers))
	for _, inner := range h.handlers {
		next = append(next, inner.WithGroup(name))
	}
	return &fanoutHandler{handlers: next}
}

// NewLogger builds the process-wide structured logger: JSON to w at the
// level named by level (debug|info|warn|warning|error, case-insensitive,
// default info), fanned out to the OTel log bridge, and trace-correlated so any
// record emitted inside a span carries trace_id/span_id.
//
// The OTLP side resolves its LoggerProvider lazily through the OTel log
// global, so NewLogger may safely be called before Setup — records simply
// go nowhere until a provider is installed.
func NewLogger(w io.Writer, level, serviceName string) *slog.Logger {
	lvl := ParseLevel(level)

	handler := &fanoutHandler{handlers: []slog.Handler{
		slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl}),
		otelslog.NewHandler(serviceName),
	}}

	return slog.New(NewTraceContextHandler(handler))
}

// ParseLevel maps a LOG_LEVEL string onto a slog.Level, case-insensitively,
// defaulting to info for anything unrecognised.
func ParseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
