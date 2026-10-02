package kafka

import "time"

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
