package mcp

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// lhForged is a request-derived value that tries to start a forged log line.
const lhForged = "x-1\r\nlevel=INFO msg=forged"

// lhRecorder keeps the string form of every attribute value of every record.
type lhRecorder struct{ values *[]string }

func (h lhRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h lhRecorder) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h lhRecorder) WithGroup(string) slog.Handler            { return h }
func (h lhRecorder) Handle(_ context.Context, r slog.Record) error {
	r.Attrs(func(a slog.Attr) bool {
		*h.values = append(*h.values, a.Value.String())
		return true
	})
	return nil
}

// lhRecordLogs routes the default logger to a recorder for the rest of the test.
func lhRecordLogs(t *testing.T) *[]string {
	t.Helper()
	var values []string
	prev := slog.Default()
	slog.SetDefault(slog.New(lhRecorder{values: &values}))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &values
}

// lhAssertOneLine fails when nothing was logged or an attribute holds CR or LF.
func lhAssertOneLine(t *testing.T, values []string, what string) {
	t.Helper()
	if len(values) == 0 {
		t.Fatalf("%s logged nothing", what)
	}
	for _, v := range values {
		if strings.ContainsAny(v, "\r\n") {
			t.Fatalf("%s: logged attribute %q holds a line break", what, v)
		}
	}
}

// An unexpected use-case error on a request path can carry request text; it must be logged on one line.
func TestLogHygiene_McpUnexpectedErrorIsLoggedOnOneLine(t *testing.T) {
	values := lhRecordLogs(t)
	_ = mapError(errors.New(lhForged))
	lhAssertOneLine(t, *values, "an unexpected MCP tool error")
}
