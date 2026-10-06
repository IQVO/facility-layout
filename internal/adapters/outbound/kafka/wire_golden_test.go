package kafka_test

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// update rewrites the byte-exact wire golden files. It exists only to
// capture a baseline deliberately; CI never passes it.
var update = flag.Bool("update", false, "rewrite testdata/wire golden files")

// wireCase is one domain event whose complete CloudEvent bytes are pinned.
type wireCase struct {
	label string
	event shared.DomainEvent
}

// wireCases covers every published event type plus the shapes that exercise
// optional (`omitempty`) and non-UTC time handling, so a DTO that drifts from
// the original struct-tag wire shape fails here byte-for-byte.
func wireCases(t *testing.T) []wireCase {
	t.Helper()
	var out []wireCase
	for _, tc := range goldenCases(t) {
		out = append(out, wireCase{label: tc.name, event: tc.event})
	}
	at := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	code := mustLocationCode(t)
	zone := time.FixedZone("UTC+3", 3*60*60)
	out = append(out,
		wireCase{"SiteRegistered_nonUTC_nanos", shared.NewSiteRegistered(time.Date(2026, 2, 3, 4, 5, 6, 123456789, zone), "WH1", "Main")},
		wireCase{"LocationTypeRegistered_noCapacity", shared.NewLocationTypeRegistered(at, "Dock", "Dock", shared.Capacity{})},
		wireCase{"LocationSlotRegistered_dock", shared.NewLocationSlotRegistered(at, code, "Dock", "Dock", "Inbound", []string{"Receiving", "Putaway"}, shared.Capacity{})},
		wireCase{"LocationGeometryUpdated_noPickSequence", shared.NewLocationGeometryUpdated(at, code, mustPoint(t, 1, 2, 3), mustDims(t, 1.2, 1, 1.5), nil)},
		wireCase{"SiteCapabilityChanged_allOff", shared.NewSiteCapabilityChanged(at, "WH1", false, false, 0)},
		wireCase{"FacilityLayoutImported_zero", shared.NewFacilityLayoutImported(at, 0, 0, 0)},
	)
	return out
}

// TestWireFormat_ByteIdenticalToGolden pins the complete structured-mode
// CloudEvent bytes (envelope + `data`) of every event type on both streams.
// The golden files were captured from the domain events' own struct-tag
// serialisation BEFORE the JSON shape moved into the adapter DTOs
// (cloudevents/wire.go); this test proves the move changed no byte.
func TestWireFormat_ByteIdenticalToGolden(t *testing.T) {
	streams := []struct {
		dir string
		enc outboundkafka.Encoder
	}{
		{"events", &outboundkafka.Publisher{}},
		{"analytics", &outboundkafka.AnalyticsPublisher{}},
	}
	for _, s := range streams {
		for _, tc := range wireCases(t) {
			t.Run(s.dir+"/"+tc.label, func(t *testing.T) {
				enc, err := s.enc.Encode(context.Background(), tc.event, goldenID)
				if err != nil {
					t.Fatalf("Encode: %v", err)
				}
				path := filepath.Join("testdata", "wire", s.dir, tc.label+".json")
				if *update {
					if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
						t.Fatalf("mkdir: %v", err)
					}
					if err := os.WriteFile(path, enc.Value, 0o600); err != nil {
						t.Fatalf("write golden: %v", err)
					}
					return
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read golden (run with -update to capture a baseline): %v", err)
				}
				if !bytes.Equal(enc.Value, want) {
					t.Errorf("wire bytes changed\n got: %s\nwant: %s", enc.Value, want)
				}
			})
		}
	}
}
