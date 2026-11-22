package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
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

// The method and the client-chosen request id are request-derived; the access log must keep them on one line.
func TestLogHygiene_AccessLogIsOneLine(t *testing.T) {
	values := lhRecordLogs(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	r := httptest.NewRequest(http.MethodGet, "/resources", nil)
	r.Method = "GET" + lhForged
	r = r.WithContext(context.WithValue(r.Context(), middleware.RequestIDKey, "req-1"+lhForged))
	RequestLogger(slog.Default())(next).ServeHTTP(httptest.NewRecorder(), r)
	lhAssertOneLine(t, *values, "an access log record")
}

// A dependency error can carry connection text; the readiness log must keep it on one line.
func TestLogHygiene_ReadinessFailureIsLoggedOnOneLine(t *testing.T) {
	values := lhRecordLogs(t)
	g := NewReadiness(0, 0, Dependency{Name: "postgres", Check: func(context.Context) error { return errors.New(lhForged) }})
	_ = g.Failing(context.Background())
	lhAssertOneLine(t, *values, "a failed readiness dependency")
}
