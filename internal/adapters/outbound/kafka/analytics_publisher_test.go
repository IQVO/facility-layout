package kafka_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// analyticsEventCase is one row of the analytics-publisher table: a domain
// event plus the analytics envelope shape it must be published under.
type analyticsEventCase struct {
	name      string
	event     shared.DomainEvent
	wantType  string
	wantKey   string
	wantField string
	wantValue any
}

func TestAnalyticsPublisher_PublishesEachEventType(t *testing.T) {
	at := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	code := mustLocationCode(t)

	tests := []analyticsEventCase{
		{
			name:      "SiteRegistered",
			event:     shared.NewSiteRegistered(at, "WH1", "Main"),
			wantType:  "com.warehouse.wms.facility-layout.site.SiteRegistered",
			wantKey:   "WH1",
			wantField: "siteCode",
			wantValue: "WH1",
		},
		{
			name:      "ZoneRegistered",
			event:     shared.NewZoneRegistered(at, "z-1", "WH1", "STOR", "AMB", shared.Ambient, false),
			wantType:  "com.warehouse.wms.facility-layout.zone.ZoneRegistered",
			wantKey:   "z-1",
			wantField: "siteCode",
			wantValue: "WH1",
		},
		{
			name:      "AisleRegistered",
			event:     shared.NewAisleRegistered(at, "a-1", "z-1", "A07", 7, shared.TwoWay),
			wantType:  "com.warehouse.wms.facility-layout.aisle.AisleRegistered",
			wantKey:   "a-1",
			wantField: "zoneId",
			wantValue: "z-1",
		},
		{
			name:      "LocationTypeRegistered",
			event:     shared.NewLocationTypeRegistered(at, "PalletRack", "Storage", mustCapacity(t, 1000, 2)),
			wantType:  "com.warehouse.wms.facility-layout.locationtype.LocationTypeRegistered",
			wantKey:   "PalletRack",
			wantField: "locationType",
			wantValue: "PalletRack",
		},
		{
			name:      "PlacementRuleDefined",
			event:     shared.NewPlacementRuleDefined(at, "r-1", "PalletRack", "ALLOW", "zone.hazmat==true"),
			wantType:  "com.warehouse.wms.facility-layout.placementrule.PlacementRuleDefined",
			wantKey:   "r-1",
			wantField: "ruleId",
			wantValue: "r-1",
		},
		{
			name:      "LocationSlotRegistered",
			event:     shared.NewLocationSlotRegistered(at, code, "PalletRack", "Storage", "", nil, mustCapacity(t, 500, 1)),
			wantType:  "com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered",
			wantKey:   code.String(),
			wantField: "locationCode",
			wantValue: code.String(),
		},
		{
			name:      "LocationSlotDecommissioned",
			event:     shared.NewLocationSlotDecommissioned(at, code),
			wantType:  "com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned",
			wantKey:   code.String(),
			wantField: "locationCode",
			wantValue: code.String(),
		},
		{
			name:      "FacilityLayoutImported",
			event:     shared.NewFacilityLayoutImported(at, 10, 9, 1),
			wantType:  "com.warehouse.wms.facility-layout.locationslot.FacilityLayoutImported",
			wantKey:   "com.warehouse.wms.facility-layout.locationslot.FacilityLayoutImported",
			wantField: "slotsImported",
			wantValue: float64(9),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertAnalyticsEventPublished(t, tt, at)
		})
	}
}

// assertAnalyticsEventPublished publishes one table row's event through a
// fresh AnalyticsPublisher and checks the resulting analytics envelope's
// wire shape (including the schema_version the analytics topic carries).
func assertAnalyticsEventPublished(t *testing.T, tt analyticsEventCase, at time.Time) {
	t.Helper()
	w := &fakeWriter{}
	p := outboundkafka.NewAnalyticsPublisher(nil, func() string { return "evt-fixed" })
	p.Writer = w

	if err := p.Publish(context.Background(), tt.event); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(w.msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(w.msgs))
	}
	msg := w.msgs[0]
	if string(msg.Key) != tt.wantKey {
		t.Errorf("key = %q, want %q", string(msg.Key), tt.wantKey)
	}

	var env outboundkafka.AnalyticsEnvelope
	if err := json.Unmarshal(msg.Value, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.EventType != tt.wantType {
		t.Errorf("event_type = %q, want %q", env.EventType, tt.wantType)
	}
	if env.EventId != "evt-fixed" {
		t.Errorf("event_id = %q, want evt-fixed", env.EventId)
	}
	if env.Source != "facility-layout" {
		t.Errorf("source = %q, want facility-layout", env.Source)
	}
	if env.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", env.SchemaVersion)
	}
	if !env.OccurredAt.Equal(at) {
		t.Errorf("occurred_at = %v, want %v", env.OccurredAt, at)
	}

	var data map[string]any
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if got := data[tt.wantField]; got != tt.wantValue {
		t.Errorf("data[%q] = %v (%T), want %v (%T)", tt.wantField, got, got, tt.wantValue, tt.wantValue)
	}
}

func TestAnalyticsPublisher_PropagatesWriteError(t *testing.T) {
	boom := errors.New("broker down")
	p := outboundkafka.NewAnalyticsPublisher(nil, func() string { return "evt" })
	p.Writer = &fakeWriter{err: boom}

	err := p.Publish(context.Background(), shared.NewSiteRegistered(time.Now(), "WH1", "Main"))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped broker down", err)
	}
}
