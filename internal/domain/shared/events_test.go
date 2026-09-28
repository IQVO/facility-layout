package shared_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// domainEventCase is one row of the event-identity table: an event plus the
// name and CloudEvents type it must carry.
type domainEventCase struct {
	name     string
	event    shared.DomainEvent
	wantName string
	wantType string
}

func TestDomainEventsCarryNameTypeAndTime(t *testing.T) {
	at := time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC)
	code, err := shared.ParseLocationCode("WH1-STOR-AMB-A07-03-02-B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	capacity, err := shared.NewCapacity(1200, 2.4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []domainEventCase{
		{
			name:     "SiteRegistered",
			event:    shared.NewSiteRegistered(at, "WH1", "Fulfilment Centre One"),
			wantName: "SiteRegistered",
			wantType: "com.warehouse.wms.facility-layout.site.SiteRegistered",
		},
		{
			name:     "ZoneRegistered",
			event:    shared.NewZoneRegistered(at, "WH1-STOR-AMB", "WH1", "STOR", "AMB", shared.Ambient, false),
			wantName: "ZoneRegistered",
			wantType: "com.warehouse.wms.facility-layout.zone.ZoneRegistered",
		},
		{
			name:     "AisleRegistered",
			event:    shared.NewAisleRegistered(at, "WH1-STOR-AMB-A07", "WH1-STOR-AMB", "A07", 7, shared.TwoWay),
			wantName: "AisleRegistered",
			wantType: "com.warehouse.wms.facility-layout.aisle.AisleRegistered",
		},
		{
			name:     "LocationTypeRegistered",
			event:    shared.NewLocationTypeRegistered(at, "PalletRack", "Storage", capacity),
			wantName: "LocationTypeRegistered",
			wantType: "com.warehouse.wms.facility-layout.locationtype.LocationTypeRegistered",
		},
		{
			name:     "PlacementRuleDefined",
			event:    shared.NewPlacementRuleDefined(at, "RULE-HAZ-1", "PalletRack", "Allow", "zoneCode=HAZ"),
			wantName: "PlacementRuleDefined",
			wantType: "com.warehouse.wms.facility-layout.placementrule.PlacementRuleDefined",
		},
		{
			name:     "LocationSlotRegistered",
			event:    shared.NewLocationSlotRegistered(at, code, "PalletRack", "Storage", "", nil, capacity),
			wantName: "LocationSlotRegistered",
			wantType: "com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered",
		},
		{
			name:     "LocationSlotDecommissioned",
			event:    shared.NewLocationSlotDecommissioned(at, code),
			wantName: "LocationSlotDecommissioned",
			wantType: "com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned",
		},
		{
			name:     "FacilityLayoutImported",
			event:    shared.NewFacilityLayoutImported(at, 500, 497, 3),
			wantName: "FacilityLayoutImported",
			wantType: "com.warehouse.wms.facility-layout.locationslot.FacilityLayoutImported",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertDomainEventIdentity(t, tc, at)
		})
	}
}

// assertDomainEventIdentity exercises one table row: the event's name,
// CloudEvents type, occurredAt, and its JSON-serialized envelope.
func assertDomainEventIdentity(t *testing.T, tc domainEventCase, at time.Time) {
	t.Helper()
	if got := tc.event.EventName(); got != tc.wantName {
		t.Fatalf("expected event name %q, got %q", tc.wantName, got)
	}
	if got := tc.event.EventType(); got != tc.wantType {
		t.Fatalf("expected CloudEvents type %q, got %q", tc.wantType, got)
	}
	if !tc.event.OccurredAt().Equal(at) {
		t.Fatalf("expected occurredAt %v, got %v", at, tc.event.OccurredAt())
	}
	payload, err := json.Marshal(tc.event)
	if err != nil {
		t.Fatalf("event must be JSON-serializable: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded["eventName"] != tc.wantName || decoded["eventType"] != tc.wantType {
		t.Fatalf("serialized envelope lost its identity: %s", payload)
	}
}

func TestLocationSlotRegisteredCarriesResolvedParents(t *testing.T) {
	at := time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC)
	code, err := shared.ParseLocationCode("WH1-STOR-FRZ-A02-01-03-A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	capacity, err := shared.NewCapacity(800, 1.2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	event := shared.NewLocationSlotRegistered(at, code, "Shelf", "Storage", "", nil, capacity)
	if event.LocationCode != "WH1-STOR-FRZ-A02-01-03-A" {
		t.Fatalf("unexpected location code: %q", event.LocationCode)
	}
	if event.ZoneID != "WH1-STOR-FRZ" || event.AisleID != "WH1-STOR-FRZ-A02" {
		t.Fatalf("unexpected parents: zone=%q aisle=%q", event.ZoneID, event.AisleID)
	}
	if event.MaxWeightKg != 800 || event.MaxVolumeM3 != 1.2 {
		t.Fatalf("unexpected capacity: %v/%v", event.MaxWeightKg, event.MaxVolumeM3)
	}
}
