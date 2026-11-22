package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// crlfForged is appended to every event-derived value the log-hygiene tests
// feed in: a raw CR/LF that reached a log attribute would let a crafted event
// forge a whole log line.
const crlfForged = "\r\nlevel=ERROR msg=forged"

// crlfRecorder is a slog.Handler that keeps every record so a test can inspect
// attribute VALUES: text and JSON handlers escape line breaks in their output,
// so asserting on rendered output would never fail.
type crlfRecorder struct {
	mu      *sync.Mutex
	records *[]slog.Record
	attrs   []slog.Attr
}

func newCRLFRecorder() *crlfRecorder {
	return &crlfRecorder{mu: &sync.Mutex{}, records: &[]slog.Record{}}
}

func (h *crlfRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (h *crlfRecorder) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	r = r.Clone()
	r.AddAttrs(h.attrs...)
	*h.records = append(*h.records, r)
	return nil
}

func (h *crlfRecorder) WithAttrs(as []slog.Attr) slog.Handler {
	return &crlfRecorder{mu: h.mu, records: h.records, attrs: append(append([]slog.Attr{}, h.attrs...), as...)}
}

func (h *crlfRecorder) WithGroup(string) slog.Handler { return h }

// assertNoLineBreaks fails when fewer than minRecords records were logged or
// when any attribute value (groups included) holds a CR or LF.
func (h *crlfRecorder) assertNoLineBreaks(t *testing.T, minRecords int) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(*h.records) < minRecords {
		t.Fatalf("got %d log records, want at least %d", len(*h.records), minRecords)
	}
	var bad []string
	for _, r := range *h.records {
		r.Attrs(func(a slog.Attr) bool {
			bad = crlfCheckAttr(bad, r.Message, a)
			return true
		})
	}
	if len(bad) > 0 {
		t.Errorf("%d log attribute(s) carry a line break, e.g.:\n%s", len(bad), strings.Join(bad[:min(len(bad), 5)], "\n"))
	}
}

func crlfCheckAttr(bad []string, msg string, a slog.Attr) []string {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		for _, g := range v.Group() {
			bad = crlfCheckAttr(bad, msg, g)
		}
		return bad
	}
	if s := v.String(); strings.ContainsAny(s, "\r\n") {
		bad = append(bad, fmt.Sprintf("%q: %q = %q", msg, a.Key, s))
	}
	return bad
}
