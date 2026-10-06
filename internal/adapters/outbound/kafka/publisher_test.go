package kafka_test

import (
	"bytes"
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
// assert on the published CloudEvent without a live broker.
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

const (
	goldenID   = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
	goldenTime = "2026-02-03T04:05:06Z"
	ctHeader   = "application/cloudevents+json; charset=UTF-8"
)

// goldenCase is one published event type: the domain event, its exact
// CloudEvents `type`, subject, Kafka key, and the exact `data` JSON.
type goldenCase struct {
	name    string
	event   shared.DomainEvent
	ceType  string
	subject string
	key     string
	data    string
}

func goldenCases(t *testing.T) []goldenCase {
	t.Helper()
	at := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	code := mustLocationCode(t)
	seq := 4
	segment, err := shared.NewSegment(mustPoint(t, 0, 0, 0), mustPoint(t, 0, 10, 0))
	if err != nil {
		t.Fatalf("NewSegment: %v", err)
	}
	rect, err := shared.NewRect(mustPoint(t, 1, 2, 0), mustDims(t, 3, 4, 5))
	if err != nil {
		t.Fatalf("NewRect: %v", err)
	}
	const p = "com.warehouse.wms.facility-layout."
	base := func(entity, name string) string {
		return `"eventName":"` + name + `","eventType":"` + p + entity + "." + name + `","occurredAt":"` + goldenTime + `"`
	}
	return []goldenCase{
		{"SiteRegistered", shared.NewSiteRegistered(at, "WH1", "Main"),
			p + "site.SiteRegistered", "WH1", "WH1",
			`{` + base("site", "SiteRegistered") + `,"siteCode":"WH1","siteName":"Main"}`},
		{"SiteCapabilityChanged", shared.NewSiteCapabilityChanged(at, "WH1", true, false, 42),
			p + "site.SiteCapabilityChanged", "WH1", "WH1",
			`{` + base("site", "SiteCapabilityChanged") + `,"site_code":"WH1","transfer_origin_enabled":true,"transfer_destination_enabled":false,"capability_revision":42}`},
		{"ZoneRegistered", shared.NewZoneRegistered(at, "WH1-STOR-AMB", "WH1", "STOR", "AMB", shared.Ambient, false),
			p + "zone.ZoneRegistered", "WH1-STOR-AMB", "WH1-STOR-AMB",
			`{` + base("zone", "ZoneRegistered") + `,"zoneId":"WH1-STOR-AMB","siteCode":"WH1","areaCode":"STOR","zoneCode":"AMB","temperatureClass":"Ambient","hazmat":false}`},
		{"AisleRegistered", shared.NewAisleRegistered(at, "WH1-STOR-AMB-A07", "WH1-STOR-AMB", "A07", 7, shared.TwoWay),
			p + "aisle.AisleRegistered", "WH1-STOR-AMB-A07", "WH1-STOR-AMB-A07",
			`{` + base("aisle", "AisleRegistered") + `,"aisleId":"WH1-STOR-AMB-A07","zoneId":"WH1-STOR-AMB","aisleCode":"A07","sequenceHint":7,"direction":"TwoWay"}`},
		{"LocationTypeRegistered", shared.NewLocationTypeRegistered(at, "PalletRack", "Storage", mustCapacity(t, 1000, 2)),
			p + "locationtype.LocationTypeRegistered", "PalletRack", "PalletRack",
			`{` + base("locationtype", "LocationTypeRegistered") + `,"locationType":"PalletRack","role":"Storage","maxWeightKg":1000,"maxVolumeM3":2}`},
		{"PlacementRuleDefined", shared.NewPlacementRuleDefined(at, "r-1", "PalletRack", "ALLOW", "zone.hazmat==true"),
			p + "placementrule.PlacementRuleDefined", "r-1", "r-1",
			`{` + base("placementrule", "PlacementRuleDefined") + `,"ruleId":"r-1","locationType":"PalletRack","effect":"ALLOW","predicate":"zone.hazmat==true"}`},
		{"LocationSlotRegistered", shared.NewLocationSlotRegistered(at, code, "PalletRack", "Storage", "", nil, mustCapacity(t, 500, 1)),
			p + "locationslot.LocationSlotRegistered", code.String(), code.String(),
			`{` + base("locationslot", "LocationSlotRegistered") + `,"locationCode":"WH1-STOR-AMB-A07-03-02-B","aisleId":"WH1-STOR-AMB-A07","zoneId":"WH1-STOR-AMB","locationType":"PalletRack","role":"Storage","maxWeightKg":500,"maxVolumeM3":1}`},
		{"LocationSlotDecommissioned", shared.NewLocationSlotDecommissioned(at, code),
			p + "locationslot.LocationSlotDecommissioned", code.String(), code.String(),
			`{` + base("locationslot", "LocationSlotDecommissioned") + `,"locationCode":"WH1-STOR-AMB-A07-03-02-B"}`},
		{"FacilityLayoutImported", shared.NewFacilityLayoutImported(at, 10, 9, 1),
			p + "locationslot.FacilityLayoutImported", outboundkafka.ImportSubject, goldenID,
			`{` + base("locationslot", "FacilityLayoutImported") + `,"rowsSubmitted":10,"slotsImported":9,"rowsRejected":1}`},
		{"LocationGeometryUpdated", shared.NewLocationGeometryUpdated(at, code, mustPoint(t, 1, 2, 3), mustDims(t, 1.2, 1, 1.5), &seq),
			p + "locationslot.LocationGeometryUpdated", code.String(), code.String(),
			`{` + base("locationslot", "LocationGeometryUpdated") + `,"locationCode":"WH1-STOR-AMB-A07-03-02-B","xM":1,"yM":2,"zM":3,"widthM":1.2,"depthM":1,"heightM":1.5,"pickSequence":4}`},
		{"AisleGeometryUpdated", shared.NewAisleGeometryUpdated(at, "WH1-STOR-AMB-A07", segment),
			p + "aisle.AisleGeometryUpdated", "WH1-STOR-AMB-A07", "WH1-STOR-AMB-A07",
			`{` + base("aisle", "AisleGeometryUpdated") + `,"aisleId":"WH1-STOR-AMB-A07","startXM":0,"startYM":0,"startZM":0,"endXM":0,"endYM":10,"endZM":0,"lengthM":10}`},
		{"FixedStructureRegistered", shared.NewFixedStructureRegistered(at, "s-1", "WH1", "Column", rect, "C1"),
			p + "structure.FixedStructureRegistered", "s-1", "s-1",
			`{` + base("structure", "FixedStructureRegistered") + `,"structureId":"s-1","siteCode":"WH1","kind":"Column","xM":1,"yM":2,"zM":0,"widthM":3,"depthM":4,"heightM":5,"label":"C1"}`},
		{"CrossAisleRegistered", shared.NewCrossAisleRegistered(at, "WH1-STOR-AMB", "A07", "A08", "03"),
			p + "crossaisle.CrossAisleRegistered", "WH1-STOR-AMB/A07-A08@03", "WH1-STOR-AMB/A07-A08@03",
			`{` + base("crossaisle", "CrossAisleRegistered") + `,"zoneId":"WH1-STOR-AMB","fromAisle":"A07","toAisle":"A08","atBay":"03"}`},
	}
}

// goldenEvent renders the exact structured-mode CloudEvent expected on the
// wire for one case and stream.
func goldenEvent(tc goldenCase, stream string) string {
	return `{"specversion":"1.0","id":"` + goldenID + `","source":"/warehouse/facility-layout","type":"` + tc.ceType +
		`","subject":"` + tc.subject + `","time":"` + goldenTime + `","datacontenttype":"application/json","dataschema":"urn:warehouse:facility-layout:` +
		stream + `:` + tc.name + `:v1","data":` + tc.data + `}`
}

// compact canonicalises JSON so key order/whitespace never matter but every
// attribute and value does.
func compact(t *testing.T, raw []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("invalid JSON %s: %v", raw, err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	return string(b)
}

func assertGolden(t *testing.T, msg kafkago.Message, tc goldenCase, stream string) {
	t.Helper()
	if string(msg.Key) != tc.key {
		t.Errorf("key = %q, want %q", msg.Key, tc.key)
	}
	if got, want := compact(t, msg.Value), compact(t, []byte(goldenEvent(tc, stream))); got != want {
		t.Errorf("CloudEvent mismatch\n got: %s\nwant: %s", got, want)
	}
	var ct []byte
	for _, h := range msg.Headers {
		if h.Key == "content-type" {
			ct = h.Value
		}
	}
	if !bytes.Equal(ct, []byte(ctHeader)) {
		t.Errorf("content-type header = %q, want %q", ct, ctHeader)
	}
}

// TestPublisher_GoldenCloudEventPerType asserts the exact CloudEvent (every
// attribute, the type string, the data payload) and the content-type header
// for every event type on the integration topic.
func TestPublisher_GoldenCloudEventPerType(t *testing.T) {
	for _, tc := range goldenCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			w := &fakeWriter{}
			p := outboundkafka.NewPublisher(nil, func() string { return goldenID })
			p.Writer = w
			if err := p.Publish(context.Background(), tc.event); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			if len(w.msgs) != 1 {
				t.Fatalf("expected 1 message, got %d", len(w.msgs))
			}
			assertGolden(t, w.msgs[0], tc, "events")
		})
	}
}

// TestPublisher_KeysAreAggregateIdentityNotEventType guards the partition-key
// contract: no event is keyed by its event-type string (which would pin every
// occurrence of that type to one partition), events about one aggregate share
// its key with that aggregate's lifecycle events, and the batch-outcome
// FacilityLayoutImported (no aggregate) is keyed by its CloudEvents id so
// batches spread over partitions while an outbox redelivery stays put.
func TestPublisher_KeysAreAggregateIdentityNotEventType(t *testing.T) {
	encode := func(t *testing.T, ev shared.DomainEvent, id string) string {
		t.Helper()
		enc, err := outboundkafka.NewPublisher(nil, func() string { return id }).Encode(context.Background(), ev, id)
		if err != nil {
			t.Fatalf("Encode %s: %v", ev.EventName(), err)
		}
		return string(enc.Key)
	}
	for _, tc := range goldenCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			key := encode(t, tc.event, goldenID)
			if key == tc.event.EventType() || key == "" {
				t.Fatalf("key = %q: must be a non-empty aggregate identity, not the event type", key)
			}
		})
	}

	at := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	code := mustLocationCode(t)
	capacity, err := shared.NewCapacity(500, 1)
	if err != nil {
		t.Fatalf("NewCapacity: %v", err)
	}
	slotKey := encode(t, shared.NewLocationSlotRegistered(at, code, "PalletRack", "Storage", "", nil, capacity), "a")
	geoKey := encode(t, shared.NewLocationGeometryUpdated(at, code, mustPoint(t, 1, 2, 3), mustDims(t, 1, 1, 1), nil), "b")
	if slotKey != geoKey {
		t.Errorf("LocationGeometryUpdated key %q != LocationSlotRegistered key %q for the same location", geoKey, slotKey)
	}

	imp := shared.NewFacilityLayoutImported(at, 1, 1, 0)
	if k1, k2 := encode(t, imp, "id-1"), encode(t, imp, "id-2"); k1 == k2 {
		t.Errorf("two import batches share key %q: they must spread across partitions", k1)
	}
	if k1, k2 := encode(t, imp, "id-1"), encode(t, imp, "id-1"); k1 != k2 {
		t.Errorf("redelivery of one import changed key: %q vs %q", k1, k2)
	}
}

// TestPublisher_CrossServiceContractTypes pins the exact strings
// inventory-storage dispatches on.
func TestPublisher_CrossServiceContractTypes(t *testing.T) {
	want := map[string]string{
		"LocationSlotRegistered":     "com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered",
		"LocationSlotDecommissioned": "com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned",
		"ZoneRegistered":             "com.warehouse.wms.facility-layout.zone.ZoneRegistered",
	}
	seen := 0
	for _, tc := range goldenCases(t) {
		w, ok := want[tc.name]
		if !ok {
			continue
		}
		seen++
		enc, err := (&outboundkafka.Publisher{}).Encode(context.Background(), tc.event, goldenID)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		var ev struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(enc.Value, &ev); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if ev.Type != w {
			t.Errorf("%s type = %q, want %q", tc.name, ev.Type, w)
		}
	}
	if seen != len(want) {
		t.Fatalf("checked %d contract types, want %d", seen, len(want))
	}
}

// TestPublisher_EncodeUsesCallerID proves the outbox path's id is the one
// carried on the wire (minted once, stable across redelivery).
func TestPublisher_EncodeUsesCallerID(t *testing.T) {
	p := outboundkafka.NewPublisher(nil, func() string { return "must-not-be-used" })
	enc, err := p.Encode(context.Background(), shared.NewSiteRegistered(time.Now(), "WH1", "Main"), "outbox-id")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var ev struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(enc.Value, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.ID != "outbox-id" || enc.Topic != outboundkafka.Topic {
		t.Errorf("id = %q topic = %q", ev.ID, enc.Topic)
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

func TestRelaySink_SendsWithTopicAndContentTypeHeader(t *testing.T) {
	w := &fakeWriter{}
	s := &outboundkafka.RelaySink{Writer: w}
	enc := outboundkafka.Encoded{Topic: outboundkafka.AnalyticsTopic, EventType: "x", Key: []byte("k"), Value: []byte(`{}`)}
	if err := s.Send(context.Background(), enc); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(w.msgs) != 1 || w.msgs[0].Topic != outboundkafka.AnalyticsTopic {
		t.Fatalf("unexpected messages: %+v", w.msgs)
	}
	if len(w.msgs[0].Headers) != 1 || string(w.msgs[0].Headers[0].Value) != ctHeader {
		t.Errorf("headers = %+v", w.msgs[0].Headers)
	}
}
