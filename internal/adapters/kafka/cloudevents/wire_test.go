package cloudevents_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

func marshalWire(t *testing.T, e shared.DomainEvent) (map[string]any, []byte) {
	t.Helper()
	data, err := cloudevents.WireData(e)
	if err != nil {
		t.Fatalf("WireData: %v", err)
	}
	payload, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return decoded, payload
}

func TestWireData_EnvelopeCarriesEventIdentity(t *testing.T) {
	at := time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC)
	e := shared.NewSiteRegistered(at, "WH1", "Fulfilment Centre One")
	decoded, payload := marshalWire(t, e)
	if decoded["eventName"] != e.EventName() || decoded["eventType"] != e.EventType() || decoded["occurredAt"] != "2026-08-22T09:00:00Z" {
		t.Fatalf("wire envelope lost the event identity: %s", payload)
	}
}

func TestWireData_SiteCapabilityChangedCarriesFullLWWState(t *testing.T) {
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	decoded, payload := marshalWire(t, shared.NewSiteCapabilityChanged(at, "WH1", true, false, 42))
	for _, field := range []string{"site_code", "transfer_origin_enabled", "transfer_destination_enabled", "capability_revision"} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("payload missing %q: %s", field, payload)
		}
	}
	if _, leaksPII := decoded["site_name"]; leaksPII {
		t.Errorf("capability event must not contain site_name: %s", payload)
	}
}

// fakeEvent is a DomainEvent with no wire DTO.
type fakeEvent struct{}

func (fakeEvent) EventName() string     { return "Fake" }
func (fakeEvent) EventType() string     { return "x.Fake" }
func (fakeEvent) OccurredAt() time.Time { return time.Time{} }

// An event type without a DTO must fail loudly instead of leaking its Go
// field names onto the wire.
func TestWireData_UnknownEventTypeIsAnError(t *testing.T) {
	if _, err := cloudevents.WireData(fakeEvent{}); err == nil {
		t.Fatal("expected an error for a domain event with no wire DTO")
	}
}
