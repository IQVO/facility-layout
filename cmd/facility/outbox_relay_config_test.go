package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
)

// TestOutboxMaxAttempts pins OUTBOX_MAX_ATTEMPTS (gap fix D12, ADR 0036):
// unset means the fleet default of 20, 0 means unlimited, and an invalid or
// negative value falls back to the default with a warning instead of
// silently disabling the cap.
func TestOutboxMaxAttempts(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
		warn bool
	}{
		{name: "unset uses the default", raw: "", want: 20},
		{name: "explicit value", raw: "5", want: 5},
		{name: "zero means unlimited", raw: "0", want: 0},
		{name: "negative falls back", raw: "-1", want: 20, warn: true},
		{name: "garbage falls back", raw: "many", want: 20, warn: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OUTBOX_MAX_ATTEMPTS", tt.raw)
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, nil))
			if got := outboxMaxAttempts(logger); got != tt.want {
				t.Fatalf("outboxMaxAttempts(%q) = %d, want %d", tt.raw, got, tt.want)
			}
			if warned := strings.Contains(buf.String(), "level=WARN"); warned != tt.warn {
				t.Fatalf("warned = %v, want %v (log: %s)", warned, tt.warn, buf.String())
			}
		})
	}
}

// TestDefaultOutboxMaxAttemptsIsTwenty keeps the fleet default in one
// place: the postgres package constant cmd/facility falls back to.
func TestDefaultOutboxMaxAttemptsIsTwenty(t *testing.T) {
	if postgres.DefaultOutboxMaxAttempts != 20 {
		t.Fatalf("DefaultOutboxMaxAttempts = %d, want 20 (fleet convention, ADR 0036)", postgres.DefaultOutboxMaxAttempts)
	}
}
