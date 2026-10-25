//go:build integration

package kafka_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"

	"github.com/claudioed/facility-layout/internal/adapters/kafka/cloudevents"
	adapter "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// This file proves the real Kafka Writer's partition placement, not a fake
// Writer's captured messages. A fake-writer unit test (publisher_test.go)
// can only assert that Message.Key is SET; it cannot catch a Writer whose
// Balancer ignores Key entirely for routing -- which is exactly the bug
// this test guards against (see ADR 0021, mirroring order-management's
// ADR 0027 / PR #111, both prompted by warehouse-infra PR #42's 1->8
// partition scaleup).
//
// createTopicWithPartitions dials the broker directly rather than relying
// on AllowAutoTopicCreation, because auto-created topics get the broker's
// default partition count (1), not the 8 this test needs to reproduce the
// fleet's real scaleup.
func createTopicWithPartitions(t *testing.T, ctx context.Context, brokers []string, topic string, numPartitions int) {
	t.Helper()
	conn, err := kafkago.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		t.Fatalf("dial Kafka broker: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.CreateTopics(kafkago.TopicConfig{Topic: topic, NumPartitions: numPartitions, ReplicationFactor: 1}); err != nil {
		t.Fatalf("create Kafka topic: %v", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		partitions, err := conn.ReadPartitions(topic)
		if err == nil && len(partitions) >= numPartitions {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("Kafka topic %q never became ready with %d partitions", topic, numPartitions)
}

// TestPublisherKeysMessagesForSameAggregateOntoTheSamePartition is the
// real-Kafka-level guarantee behind the Hash balancer switch: on an
// 8-partition topic (mirroring the Phase 3 partition scaleup,
// warehouse-infra PR #42), every event published for the SAME aggregate
// (here, a ZoneRegistered/ZoneRegistered pair sharing a ZoneID -- this
// package's aggregateKey for ZoneRegistered) must land on the SAME
// partition, while a different aggregate's event is free to land
// elsewhere. This is exactly what Kafka's default (key-hash) partitioner
// provides once a non-nil Key is set with a key-aware Balancer -- and
// exactly what this package's writers did NOT provide under the old
// &kafkago.LeastBytes{} balancer, even though Message.Key was already
// being set correctly for every event (verified via aggregateKey).
func TestPublisherKeysMessagesForSameAggregateOntoTheSamePartition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	container, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1",
		tckafka.WithClusterID(fmt.Sprintf("facility-layout-kafka-itest-partitioning-%d", time.Now().UnixNano())),
	)
	if err != nil {
		t.Fatalf("start Kafka container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate Kafka container: %v", err)
		}
	})

	brokers, err := container.Brokers(ctx)
	if err != nil {
		t.Fatalf("resolve Kafka brokers: %v", err)
	}
	const numPartitions = 8
	topic := fmt.Sprintf("warehouse.facility.events.itest-part-%d", time.Now().UnixNano())
	createTopicWithPartitions(t, ctx, brokers, topic, numPartitions)

	publisher := &adapter.Publisher{
		Writer: &kafkago.Writer{
			Addr:                   kafkago.TCP(brokers...),
			Topic:                  topic,
			Balancer:               &kafkago.Hash{},
			AllowAutoTopicCreation: false,
		},
		NewId: func() string { return "evt-" + time.Now().Format(time.RFC3339Nano) },
	}
	t.Cleanup(func() { _ = publisher.Close() })

	occurredAt := time.Now().UTC().Truncate(time.Second)
	const sameZone = "zone-itest-same-partition"
	const otherZone = "zone-itest-other-partition"

	// Publish 3 events for sameZone (mirrors the real fleet ordering
	// concern: several events for one aggregate over time) plus 1 for a
	// different aggregate, to prove the key -- not accident -- drives
	// partition placement.
	events := []struct {
		key   string
		event shared.DomainEvent
	}{
		{sameZone, shared.NewZoneRegistered(occurredAt, sameZone, "WH1", "A", "A-01", shared.Ambient, false)},
		{sameZone, shared.NewZoneRegistered(occurredAt.Add(time.Second), sameZone, "WH1", "A", "A-02", shared.Ambient, false)},
		{sameZone, shared.NewZoneRegistered(occurredAt.Add(2*time.Second), sameZone, "WH1", "A", "A-03", shared.Ambient, false)},
		{otherZone, shared.NewZoneRegistered(occurredAt, otherZone, "WH1", "B", "B-01", shared.Ambient, false)},
	}
	for _, e := range events {
		if err := publisher.Publish(ctx, e.event); err != nil {
			t.Fatalf("publish event for %s: %v", e.key, err)
		}
	}

	// Read every message back with its partition, one reader per
	// partition (a single Reader without an explicit Partition only
	// sees whatever partition it happens to be assigned, not all of
	// them).
	partitionOf := map[string]int{}
	countByKeyPartition := map[string]int{}
	for p := 0; p < numPartitions; p++ {
		reader := kafkago.NewReader(kafkago.ReaderConfig{
			Brokers:   brokers,
			Topic:     topic,
			Partition: p,
			MaxWait:   2 * time.Second,
		})
		func() {
			defer func() { _ = reader.Close() }()
			readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
			defer readCancel()
			for {
				msg, err := reader.ReadMessage(readCtx)
				if err != nil {
					return // timeout: no more messages on this partition
				}
				key := string(msg.Key)
				assertCloudEventOnWire(t, msg, key)
				partitionOf[key] = p
				countByKeyPartition[fmt.Sprintf("%s|%d", key, p)]++
			}
		}()
	}

	if len(partitionOf) != 2 {
		t.Fatalf("observed keys->partition = %v, want exactly 2 distinct keys (sameZone, otherZone)", partitionOf)
	}
	samePartition, ok := partitionOf[sameZone]
	if !ok {
		t.Fatalf("no message observed with key %q; partitionOf = %v", sameZone, partitionOf)
	}
	if _, ok := partitionOf[otherZone]; !ok {
		t.Fatalf("no message observed with key %q; partitionOf = %v", otherZone, partitionOf)
	}

	// All 3 of sameZone's messages must be on samePartition, proving the
	// guarantee isn't a one-message coincidence.
	gotCount := countByKeyPartition[fmt.Sprintf("%s|%d", sameZone, samePartition)]
	if gotCount != 3 {
		t.Errorf("found %d of %s's 3 messages on partition %d, want 3 (all events for one aggregate must share a partition)", gotCount, sameZone, samePartition)
	}
}

// TestPublisherKeysGeometryEventsByAggregateOntoTheirLifecyclePartition proves,
// against a real 8-partition broker, that the geometry / structure /
// cross-aisle / import events are no longer keyed by their event-type string
// (ADR-0032): each LocationGeometryUpdated lands on the SAME partition as its
// location's LocationSlotRegistered, the geometry events of many locations
// spread over several partitions (under the old type key they all shared one),
// and FacilityLayoutImported batches spread too.
func TestPublisherKeysGeometryEventsByAggregateOntoTheirLifecyclePartition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	container, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1",
		tckafka.WithClusterID(fmt.Sprintf("facility-layout-kafka-itest-geokeys-%d", time.Now().UnixNano())),
	)
	if err != nil {
		t.Fatalf("start Kafka container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate Kafka container: %v", err)
		}
	})
	brokers, err := container.Brokers(ctx)
	if err != nil {
		t.Fatalf("resolve Kafka brokers: %v", err)
	}
	const numPartitions = 8
	topic := fmt.Sprintf("warehouse.facility.events.itest-geokeys-%d", time.Now().UnixNano())
	createTopicWithPartitions(t, ctx, brokers, topic, numPartitions)

	var n int
	publisher := &adapter.Publisher{
		Writer: &kafkago.Writer{Addr: kafkago.TCP(brokers...), Topic: topic, Balancer: &kafkago.Hash{}},
		NewId:  func() string { n++; return fmt.Sprintf("evt-%d", n) },
	}
	t.Cleanup(func() { _ = publisher.Close() })

	at := time.Now().UTC().Truncate(time.Second)
	capacity := mustCapacity(t, 500, 1)
	const locations = 16
	for i := 1; i <= locations; i++ {
		code, err := shared.ParseLocationCode(fmt.Sprintf("WH1-STOR-AMB-A07-%02d-02-B", i))
		if err != nil {
			t.Fatalf("ParseLocationCode: %v", err)
		}
		for _, ev := range []shared.DomainEvent{
			shared.NewLocationSlotRegistered(at, code, "PalletRack", "Storage", "", nil, capacity),
			shared.NewLocationGeometryUpdated(at, code, mustPoint(t, float64(i), 0, 0), mustDims(t, 1, 1, 1), nil),
			shared.NewFacilityLayoutImported(at, 1, 1, 0),
		} {
			if err := publisher.Publish(ctx, ev); err != nil {
				t.Fatalf("publish %s: %v", ev.EventName(), err)
			}
		}
	}

	slotPartition := map[string]int{} // locationCode -> partition of its LocationSlotRegistered
	geoPartitions := map[int]int{}    // partition -> LocationGeometryUpdated count
	importPartitions := map[int]int{} // partition -> FacilityLayoutImported count
	geoByCode := map[string][]int{}   // locationCode -> partitions of its geometry events
	for p := 0; p < numPartitions; p++ {
		reader := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: topic, Partition: p, MaxWait: 2 * time.Second})
		func() {
			defer func() { _ = reader.Close() }()
			readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
			defer readCancel()
			for {
				msg, err := reader.ReadMessage(readCtx)
				if err != nil {
					return
				}
				evt, err := cloudevents.Decode(msg.Value)
				if err != nil {
					t.Errorf("not a CloudEvent: %v", err)
					continue
				}
				switch evt.Type() {
				case "com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered":
					slotPartition[evt.Subject()] = p
				case "com.warehouse.wms.facility-layout.locationslot.LocationGeometryUpdated":
					geoPartitions[p]++
					geoByCode[evt.Subject()] = append(geoByCode[evt.Subject()], p)
				case "com.warehouse.wms.facility-layout.locationslot.FacilityLayoutImported":
					importPartitions[p]++
				}
			}
		}()
	}

	if len(slotPartition) != locations {
		t.Fatalf("read back %d slot registrations, want %d", len(slotPartition), locations)
	}
	for code, sp := range slotPartition {
		got := geoByCode[code]
		if len(got) != 1 || got[0] != sp {
			t.Errorf("%s: LocationGeometryUpdated on partitions %v, want exactly its LocationSlotRegistered partition %d", code, got, sp)
		}
	}
	if len(geoPartitions) < 2 {
		t.Errorf("all %d LocationGeometryUpdated events share partitions %v: they must be keyed by location, not event type", locations, geoPartitions)
	}
	if len(importPartitions) < 2 {
		t.Errorf("all %d FacilityLayoutImported events share partitions %v: they must spread", locations, importPartitions)
	}
}

// assertCloudEventOnWire checks a message read back from a real broker is a
// structured-mode CloudEvents 1.0 event (ADR-0024) with the content-type
// header and the aggregate id as subject.
func assertCloudEventOnWire(t *testing.T, msg kafkago.Message, key string) {
	t.Helper()
	evt, err := cloudevents.Decode(msg.Value)
	if err != nil {
		t.Errorf("message on wire is not a CloudEvent: %v", err)
		return
	}
	if evt.Type() != "com.warehouse.wms.facility-layout.zone.ZoneRegistered" || evt.Subject() != key {
		t.Errorf("type=%q subject=%q, want ZoneRegistered/%q", evt.Type(), evt.Subject(), key)
	}
	ct := ""
	for _, h := range msg.Headers {
		if h.Key == "content-type" {
			ct = string(h.Value)
		}
	}
	if ct != cloudevents.MediaType {
		t.Errorf("content-type header = %q, want %q", ct, cloudevents.MediaType)
	}
}
