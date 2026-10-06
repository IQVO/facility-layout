package events_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

var update = flag.Bool("update", false, "rewrite testdata golden files")

func mustCode(t *testing.T) shared.LocationCode {
	t.Helper()
	code, err := shared.ParseLocationCode("WH1-STOR-AMB-A07-03-02-B")
	if err != nil {
		t.Fatalf("ParseLocationCode: %v", err)
	}
	return code
}

func mustCapacity(t *testing.T, w, v float64) shared.Capacity {
	t.Helper()
	c, err := shared.NewCapacity(w, v)
	if err != nil {
		t.Fatalf("NewCapacity: %v", err)
	}
	return c
}

func mustPoint(t *testing.T, x, y, z float64) shared.Point3D {
	t.Helper()
	p, err := shared.NewPoint3D(x, y, z)
	if err != nil {
		t.Fatalf("NewPoint3D: %v", err)
	}
	return p
}

func mustDims(t *testing.T, w, d, h float64) shared.Dimensions {
	t.Helper()
	dm, err := shared.NewDimensions(w, d, h)
	if err != nil {
		t.Fatalf("NewDimensions: %v", err)
	}
	return dm
}

// TestLogPublisher_PayloadByteIdenticalToGolden pins the JSON the log
// publisher emits as `payload`. The golden files were captured from the
// domain events' own struct-tag serialisation before the JSON shape moved
// into the adapter DTOs; the log payload must stay the same bytes as the
// CloudEvent `data` on the wire.
func TestLogPublisher_PayloadByteIdenticalToGolden(t *testing.T) {
	at := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	code := mustCode(t)
	seq := 4
	cases := []struct {
		label string
		event shared.DomainEvent
	}{
		{"SiteRegistered", shared.NewSiteRegistered(at, "WH1", "Main")},
		{"SiteCapabilityChanged", shared.NewSiteCapabilityChanged(at, "WH1", true, false, 42)},
		{"LocationTypeRegistered", shared.NewLocationTypeRegistered(at, "PalletRack", "Storage", mustCapacity(t, 1000, 2))},
		{"LocationSlotRegistered_dock", shared.NewLocationSlotRegistered(at, code, "Dock", "Dock", "Inbound", []string{"Receiving", "Putaway"}, shared.Capacity{})},
		{"LocationGeometryUpdated", shared.NewLocationGeometryUpdated(at, code, mustPoint(t, 1, 2, 3), mustDims(t, 1.2, 1, 1.5), &seq)},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			var buf bytes.Buffer
			p := events.NewLogPublisher(slog.New(slog.NewJSONHandler(&buf, nil)))
			if err := p.Publish(context.Background(), tc.event); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			var line struct {
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
				t.Fatalf("log line is not JSON: %v", err)
			}
			assertPayloadGolden(t, filepath.Join("testdata", tc.label+".json"), line.Payload)
		})
	}
}

// assertPayloadGolden compares got with the golden file at path
// byte-for-byte, or rewrites the file when -update is set.
func assertPayloadGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to capture a baseline): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("log payload changed\n got: %s\nwant: %s", got, want)
	}
}
