package cloudevents_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/adapters/kafka/cloudevents"
)

func TestTypeAndDataSchema(t *testing.T) {
	if got, want := cloudevents.Type("locationslot", "LocationSlotRegistered"), "com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered"; got != want {
		t.Errorf("Type = %q, want %q", got, want)
	}
	if got, want := cloudevents.DataSchema(cloudevents.StreamAnalytics, "ZoneRegistered", 1), "urn:warehouse:facility-layout:analytics:ZoneRegistered:v1"; got != want {
		t.Errorf("DataSchema = %q, want %q", got, want)
	}
	if cloudevents.Source != "/warehouse/facility-layout" {
		t.Errorf("Source = %q", cloudevents.Source)
	}
}

func TestNew_GoldenJSON(t *testing.T) {
	at := time.Date(2026, 9, 30, 12, 0, 0, 0, time.FixedZone("BRT", -3*3600))
	b, err := cloudevents.New(cloudevents.Spec{
		ID:        "11111111-1111-4111-8111-111111111111",
		Entity:    "zone",
		EventName: "ZoneRegistered",
		Subject:   "WH1-STOR-AMB",
		Time:      at,
		Stream:    cloudevents.StreamEvents,
		Data:      map[string]any{"zoneId": "WH1-STOR-AMB"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var got, want map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	_ = json.Unmarshal([]byte(`{
		"specversion":"1.0",
		"id":"11111111-1111-4111-8111-111111111111",
		"source":"/warehouse/facility-layout",
		"type":"com.warehouse.wms.facility-layout.zone.ZoneRegistered",
		"subject":"WH1-STOR-AMB",
		"time":"2026-09-30T15:00:00Z",
		"datacontenttype":"application/json",
		"dataschema":"urn:warehouse:facility-layout:events:ZoneRegistered:v1",
		"data":{"zoneId":"WH1-STOR-AMB"}
	}`), &want)
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Errorf("golden mismatch\n got: %s\nwant: %s", gb, wb)
	}
}

func TestNew_RejectsEmptySubject(t *testing.T) {
	_, err := cloudevents.New(cloudevents.Spec{ID: "x", Entity: "site", EventName: "SiteRegistered", Time: time.Now(), Stream: cloudevents.StreamEvents})
	if err == nil {
		t.Fatal("expected empty subject to be rejected")
	}
}

func TestNew_RejectsEmptyID(t *testing.T) {
	_, err := cloudevents.New(cloudevents.Spec{Entity: "site", EventName: "SiteRegistered", Subject: "WH1", Time: time.Now(), Stream: cloudevents.StreamEvents, Data: map[string]any{}})
	if err == nil {
		t.Fatal("expected empty id to fail validation")
	}
}

func TestDecode_RoundTrip(t *testing.T) {
	b, err := cloudevents.New(cloudevents.Spec{ID: "id-1", Entity: "site", EventName: "SiteRegistered", Subject: "WH1", Time: time.Now(), Stream: cloudevents.StreamEvents, Data: map[string]any{"siteCode": "WH1"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e, err := cloudevents.Decode(b)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if e.ID() != "id-1" || e.Subject() != "WH1" || e.Type() != cloudevents.Type("site", "SiteRegistered") {
		t.Errorf("unexpected decoded event: %v", e)
	}
	var data struct {
		SiteCode string `json:"siteCode"`
	}
	if err := e.DataAs(&data); err != nil || data.SiteCode != "WH1" {
		t.Errorf("DataAs = %+v, %v", data, err)
	}
}

func TestDecode_RejectsLegacyFlatEnvelopeAndGarbage(t *testing.T) {
	for name, raw := range map[string]string{
		"legacy flat":  `{"event_id":"e1","event_type":"com.warehouse.wms.facility-layout.site.SiteRegistered","occurred_at":"2026-01-01T00:00:00Z","source":"facility-layout","data":{"siteCode":"WH1"}}`,
		"not json":     `not-json`,
		"wrong spec":   `{"specversion":"0.3","id":"1","source":"/x","type":"t"}`,
		"missing type": `{"specversion":"1.0","id":"1","source":"/x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := cloudevents.Decode([]byte(raw)); !errors.Is(err, cloudevents.ErrNotCloudEvent) {
				t.Fatalf("err = %v, want ErrNotCloudEvent", err)
			}
		})
	}
}

func TestContentTypeHeader(t *testing.T) {
	h := cloudevents.ContentTypeHeader()
	if h.Key != "content-type" || string(h.Value) != "application/cloudevents+json; charset=UTF-8" {
		t.Errorf("header = %s: %s", h.Key, h.Value)
	}
}
