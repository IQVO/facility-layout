package kafka_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	inboundkafka "github.com/claudioed/facility-layout/internal/adapters/inbound/kafka"
)

// call captures one projection-store method invocation.
type call struct {
	method    string
	eventId   string
	scope     string
	submitted int
	imported  int
	rejected  int
	at        time.Time
}

// fakeProjection records the calls the consumer makes so a test can assert the
// envelope was routed to the right method with the right scope.
type fakeProjection struct {
	calls []call
}

func (f *fakeProjection) ApplySiteRegistered(_ context.Context, eventId, scope string, at time.Time) error {
	f.calls = append(f.calls, call{method: "site", eventId: eventId, scope: scope, at: at})
	return nil
}
func (f *fakeProjection) ApplyZoneRegistered(_ context.Context, eventId, scope string, at time.Time) error {
	f.calls = append(f.calls, call{method: "zone", eventId: eventId, scope: scope, at: at})
	return nil
}
func (f *fakeProjection) ApplyAisleRegistered(_ context.Context, eventId, scope string, at time.Time) error {
	f.calls = append(f.calls, call{method: "aisle", eventId: eventId, scope: scope, at: at})
	return nil
}
func (f *fakeProjection) ApplyLocationTypeRegistered(_ context.Context, eventId, scope string, at time.Time) error {
	f.calls = append(f.calls, call{method: "loctype", eventId: eventId, scope: scope, at: at})
	return nil
}
func (f *fakeProjection) ApplyPlacementRuleDefined(_ context.Context, eventId, scope string, at time.Time) error {
	f.calls = append(f.calls, call{method: "rule", eventId: eventId, scope: scope, at: at})
	return nil
}
func (f *fakeProjection) ApplyLocationSlotRegistered(_ context.Context, eventId, scope string, at time.Time) error {
	f.calls = append(f.calls, call{method: "slot", eventId: eventId, scope: scope, at: at})
	return nil
}
func (f *fakeProjection) ApplyLocationSlotDecommissioned(_ context.Context, eventId, scope string, at time.Time) error {
	f.calls = append(f.calls, call{method: "decomm", eventId: eventId, scope: scope, at: at})
	return nil
}
func (f *fakeProjection) ApplyFacilityLayoutImported(_ context.Context, eventId, scope string, submitted, imported, rejected int, at time.Time) error {
	f.calls = append(f.calls, call{method: "import", eventId: eventId, scope: scope, submitted: submitted, imported: imported, rejected: rejected, at: at})
	return nil
}

// fakeProcessed is an in-memory ProcessedEvents.
type fakeProcessed struct {
	seen map[string]bool
}

func newFakeProcessed() *fakeProcessed { return &fakeProcessed{seen: map[string]bool{}} }

func (p *fakeProcessed) IsProcessed(_ context.Context, eventId string) (bool, error) {
	return p.seen[eventId], nil
}

func (p *fakeProcessed) MarkProcessed(_ context.Context, eventId string) (bool, error) {
	if p.seen[eventId] {
		return false, nil
	}
	p.seen[eventId] = true
	return true, nil
}

func envelope(t *testing.T, eventId, eventType string, at time.Time, data map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	env := map[string]any{
		"event_id":       eventId,
		"event_type":     eventType,
		"occurred_at":    at.Format(time.RFC3339Nano),
		"source":         "facility-layout",
		"schema_version": 1,
		"data":           json.RawMessage(raw),
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return b
}

const prefix = "com.warehouse.wms.facility-layout."

func TestAnalyticsConsumer_RoutesEachEventTypeWithScope(t *testing.T) {
	at := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		eventType  string
		data       map[string]any
		wantMethod string
		wantScope  string
	}{
		{"site", prefix + "site.SiteRegistered", map[string]any{"siteCode": "WH1"}, "site", "WH1"},
		{"zone", prefix + "zone.ZoneRegistered", map[string]any{"zoneId": "WH1-STOR-AMB", "siteCode": "WH1"}, "zone", "WH1"},
		{"aisle", prefix + "aisle.AisleRegistered", map[string]any{"aisleId": "WH1-STOR-AMB-A07", "zoneId": "WH1-STOR-AMB"}, "aisle", "WH1-STOR-AMB"},
		{"loctype", prefix + "locationtype.LocationTypeRegistered", map[string]any{"locationType": "PalletRack"}, "loctype", ""},
		{"rule", prefix + "placementrule.PlacementRuleDefined", map[string]any{"ruleId": "r-1"}, "rule", ""},
		{"slot", prefix + "locationslot.LocationSlotRegistered", map[string]any{"locationCode": "WH1-STOR-AMB-A07-03-02-B", "zoneId": "WH1-STOR-AMB"}, "slot", "WH1-STOR-AMB"},
		{"decomm", prefix + "locationslot.LocationSlotDecommissioned", map[string]any{"locationCode": "WH1-STOR-AMB-A07-03-02-B"}, "decomm", "WH1-STOR-AMB"},
		{"import", prefix + "locationslot.FacilityLayoutImported", map[string]any{"rowsSubmitted": 10, "slotsImported": 9, "rowsRejected": 1}, "import", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proj := &fakeProjection{}
			c := &inboundkafka.AnalyticsConsumer{Projection: proj, Processed: newFakeProcessed(), Logger: slog.Default()}

			raw := envelope(t, "e-"+tt.name, tt.eventType, at, tt.data)
			if err := c.HandleMessage(context.Background(), raw); err != nil {
				t.Fatalf("HandleMessage: %v", err)
			}
			if len(proj.calls) != 1 {
				t.Fatalf("calls = %d, want 1", len(proj.calls))
			}
			got := proj.calls[0]
			if got.method != tt.wantMethod {
				t.Errorf("method = %q, want %q", got.method, tt.wantMethod)
			}
			if got.scope != tt.wantScope {
				t.Errorf("scope = %q, want %q", got.scope, tt.wantScope)
			}
			if !got.at.Equal(at) {
				t.Errorf("at = %v, want %v", got.at, at)
			}
			if tt.name == "import" && (got.submitted != 10 || got.imported != 9 || got.rejected != 1) {
				t.Errorf("import tallies = %+v", got)
			}
		})
	}
}

func TestAnalyticsConsumer_Idempotent(t *testing.T) {
	at := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	proj := &fakeProjection{}
	c := &inboundkafka.AnalyticsConsumer{Projection: proj, Processed: newFakeProcessed(), Logger: slog.Default()}

	raw := envelope(t, "dup", prefix+"locationslot.LocationSlotRegistered", at, map[string]any{"locationCode": "WH1-STOR-AMB-A07-03-02-B"})
	for range 2 {
		if err := c.HandleMessage(context.Background(), raw); err != nil {
			t.Fatalf("HandleMessage: %v", err)
		}
	}
	if len(proj.calls) != 1 {
		t.Fatalf("expected 1 apply for duplicate delivery, got %d", len(proj.calls))
	}
}

func TestAnalyticsConsumer_IgnoresUnknownEventType(t *testing.T) {
	proj := &fakeProjection{}
	processed := newFakeProcessed()
	c := &inboundkafka.AnalyticsConsumer{Projection: proj, Processed: processed, Logger: slog.Default()}

	raw := envelope(t, "e1", prefix+"slot.SomethingElseHappened", time.Now(), map[string]any{"foo": "bar"})
	if err := c.HandleMessage(context.Background(), raw); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if len(proj.calls) != 0 {
		t.Fatalf("expected unknown event to make no call, got %d", len(proj.calls))
	}
	// An event with no projection method must NOT be marked processed, so a
	// later contract change could reprocess it.
	if processed.seen["e1"] {
		t.Error("non-projecting event should not be marked processed")
	}
}
