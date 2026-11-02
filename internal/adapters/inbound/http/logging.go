package http

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// sanitizeForLog strips CR/LF from an attacker-controlled value (here,
// the raw request path) before it is written to a log line. Without this,
// a crafted path segment containing an encoded newline could forge a fake
// log entry that appears to be a separate, legitimate line (CWE-117 log
// injection) once the log record reaches a downstream viewer/aggregator
// that doesn't preserve slog's JSON string escaping.
func sanitizeForLog(s string) string {
	return strings.NewReplacer("\n", "", "\r", "").Replace(s)
}

// RequestLogger returns chi middleware that logs one structured line per
// request: method, path, status, duration, response size, and the chi
// request ID (so a request can be traced across the router's own
// middleware.Recoverer output and the application logs). Responses with a
// 5xx status are logged at Error; everything else at Info.
//
// The line is logged with the request's context, so when the tracing
// middleware upstream has opened a span the record also carries its
// trace_id/span_id — that is what joins this log line to its trace.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()

			next.ServeHTTP(ww, r)

			duration := time.Since(start)
			attrs := []any{
				"method", r.Method,
				"path", sanitizeForLog(r.URL.Path),
				statusField, ww.Status(),
				"duration_ms", duration.Milliseconds(),
				"bytes", ww.BytesWritten(),
				"request_id", middleware.GetReqID(r.Context()),
			}

			if ww.Status() >= http.StatusInternalServerError {
				logger.ErrorContext(r.Context(), "http request", attrs...)
				return
			}
			logger.InfoContext(r.Context(), "http request", attrs...)
		})
	}
}
