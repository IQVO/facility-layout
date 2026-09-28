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
