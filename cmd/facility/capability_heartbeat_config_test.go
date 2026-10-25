package main

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
)

// TestSiteCapabilityHeartbeatIntervalFollowsTheHousekeepingConvention pins
// SITE_CAPABILITY_HEARTBEAT_INTERVAL (ADR 0041): unset, empty or malformed
// is the 5m default, an explicit 0 disables the loop, anything else is
// taken as written.
func TestSiteCapabilityHeartbeatIntervalFollowsTheHousekeepingConvention(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{"empty uses the default", "", 5 * time.Minute},
		{"explicit value", "90s", 90 * time.Second},
		{"zero disables", "0", 0},
		{"zero with unit disables", "0s", 0},
		{"malformed falls back to the default", "often", 5 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SITE_CAPABILITY_HEARTBEAT_INTERVAL", tt.raw)
			if got := publisherConfigFromEnv().siteCapabilityHeartbeatInterval; got != tt.want {
				t.Fatalf("interval = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultSiteCapabilityHeartbeatIntervalIsHalfTheConsumerWindow(t *testing.T) {
	// network-inventory-planning treats a capability fact older than 10
	// minutes as stale; the loop's worst gap is 1.2x the interval.
	if defaultSiteCapabilityHeartbeatInterval != 5*time.Minute {
		t.Fatalf("default = %v, want 5m", defaultSiteCapabilityHeartbeatInterval)
	}
}

func TestCapabilityHeartbeatWorkerIsNilWhenDisabled(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	clock := memory.NewFixedClock(time.Now())
	for _, interval := range []time.Duration{0, -time.Second} {
		if w := capabilityHeartbeat(adapterSet{}, clock, interval, logger); w != nil {
			t.Fatalf("interval %v: want no worker, got %T", interval, w)
		}
	}
	if w := capabilityHeartbeat(adapterSet{}, clock, time.Minute, logger); w == nil {
		t.Fatal("a positive interval must yield a worker")
	}
}
