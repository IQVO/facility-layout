package kafka

import (
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// syncWriterBatchTimeout is the BatchTimeout of every synchronous Kafka
// writer in this package (integration publisher, analytics publisher and
// the transactional-outbox relay sink).
//
// kafka-go's default BatchTimeout is 1s: a synchronous WriteMessages waits
// up to a full second for a batch to fill before flushing. The outbox relay
// sends one row per WriteMessages call, so the default capped each service
// at ~1 event/s -- observed live in the warehouse-day simulation, where a
// day's ~2,600 fulfillment events sat in outbox_events for an hour and
// downstream contexts (WES completions, order-management manifests, labor
// scorecards) never saw most of them. 10ms keeps writes batched under load
// while flushing a lone event almost immediately.
const syncWriterBatchTimeout = 10 * time.Millisecond

// syncWriterRequiredAcks makes every write wait for the broker's
// acknowledgement. kafka-go's default is RequireNone: WriteMessages returns
// nil without waiting, so the transactional-outbox relay marked rows
// published that the broker never stored -- silently at-most-once, the
// opposite of the outbox's guarantee. The 1s default BatchTimeout masked
// it; with prompt flushing, probes against a fresh 8-partition topic lost
// whole batches in 3 of 6 runs, and 0 of 6 with RequireAll. RequireAll is
// the durable choice; on the single-broker kind cluster it equals the
// leader's ack.
const syncWriterRequiredAcks = kafkago.RequireAll

// NewDeadLetterWriter builds a dead-letter writer with the same durability
// settings as every other synchronous writer in this package (the fleet
// pattern; see fulfillment-execution's identically-named helper): RequireAll
// — a DLQ publish that reports success before the broker stores the message,
// after which the consumer commits the source offset, is a silently LOST
// poison message, exactly what the DLQ exists to prevent — a 10ms
// BatchTimeout (kafka-go's 1s default caps dead-lettering at ~1 msg/s), and
// the Hash balancer so the original message key still decides the partition
// (per-key order on the .dlq topic, ADR-0021). dlqTopic is the full topic
// name ("<source>.dlq").
func NewDeadLetterWriter(brokers []string, dlqTopic string) *kafkago.Writer {
	return &kafkago.Writer{
		Addr:                   kafkago.TCP(brokers...),
		Topic:                  dlqTopic,
		Balancer:               &kafkago.Hash{},
		RequiredAcks:           syncWriterRequiredAcks,
		BatchTimeout:           syncWriterBatchTimeout,
		AllowAutoTopicCreation: true,
	}
}
