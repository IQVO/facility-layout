package kafka_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// fakeWriter captures the messages handed to WriteMessages so a test can
// assert on the published envelope without a live broker.
type fakeWriter struct {
	msgs []kafkago.Message
	err  error
}

func (w *fakeWriter) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	if w.err != nil {
		return w.err
	}
	w.msgs = append(w.msgs, msgs...)
	return nil
}

func mustLocationCode(t *testing.T) shared.LocationCode {
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

// integrationEventCase is one row of the integration-publisher table: a
// domain event plus the envelope shape it must be published under.
type integrationEventCase struct {
	name      string
	event     shared.DomainEvent
	wantType  string
	wantKey   string
	wantField string // a json field expected in data
	wantValue any
}

func TestPublisher_PublishesEachEventType(t *testing.T) {
	at := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	code := mustLocationCode(t)

	tests := []integrationEventCase{
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
			wantField: "zoneId",
			wantValue: "z-1",
		},
		{
			name:      "AisleRegistered",
			event:     shared.NewAisleRegistered(at, "a-1", "z-1", "A07", 7, shared.TwoWay),
			wantType:  "com.warehouse.wms.facility-layout.aisle.AisleRegistered",
			wantKey:   "a-1",
			wantField: "aisleId",
			wantValue: "a-1",
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
			assertIntegrationEventPublished(t, tt, at)
		})
	}
}

// assertIntegrationEventPublished publishes one table row's event through a
// fresh Publisher and checks the resulting Envelope v1 wire shape.
func assertIntegrationEventPublished(t *testing.T, tt integrationEventCase, at time.Time) {
	t.Helper()
	w := &fakeWriter{}
	p := outboundkafka.NewPublisher(nil, func() string { return "evt-fixed" })
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

	var env outboundkafka.Envelope
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

func TestPublisher_PropagatesWriteError(t *testing.T) {
	boom := errors.New("broker down")
	p := outboundkafka.NewPublisher(nil, func() string { return "evt" })
	p.Writer = &fakeWriter{err: boom}

	err := p.Publish(context.Background(), shared.NewSiteRegistered(time.Now(), "WH1", "Main"))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped broker down", err)
	}
}
