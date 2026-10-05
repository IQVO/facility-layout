---
id: 0031-kafka-writer-durability
title: "31. Kafka writer durability — RequireAll acks with a 10ms batch timeout"
sidebar_label: 31. Kafka writer durability
sidebar_position: 31
description: "Every synchronous kafka-go Writer in this service — integration publisher, analytics publisher, relay sink, DLQ writer — pins RequiredAcks=RequireAll and BatchTimeout=10ms, so a reported-successful publish is actually stored by the broker before the caller proceeds."
---

# 31. Kafka writer durability: RequireAll acks with a 10ms batch timeout

## Status

**Accepted.** Consolidates and extends ADR-0021 (balancer); the
durability half was applied piecemeal (writer_config.go, then the DLQ
writer fix in this change set) without a record of the rule itself.

## Context

kafka-go's `Writer` defaults are throughput-oriented: `RequiredAcks`
defaults to `RequireNone`, meaning `WriteMessages` returns success as
soon as the bytes are handed to the connection — the broker may never
store them. For a fire-and-forget metric feed that is a reasonable
default. For this service it is not:

- The integration publisher carries the Published Language; a
  "successful" publish that the broker lost breaks the at-least-once
  contract every Conformist consumer relies on (ADR-0009/0013).
- The outbox relay marks a row PUBLISHED after a successful send; with
  RequireNone a broker-side loss is silent and permanent (ADR-0018's
  whole point defeated).
- The DLQ writer runs after a message exhausted its retries; losing the
  DLQ publish and then committing the source offset destroys the poison
  message (ADR-0020).

## Decision

Every synchronous `kafkago.Writer` this service constructs is pinned in
one place (`internal/adapters/outbound/kafka/writer_config.go`, plus
`NewDeadLetterWriter` for the DLQ):

- `RequiredAcks: kafkago.RequireAll` — a write returns success only
  after all in-sync replicas have stored it.
- `BatchTimeout: 10 * time.Millisecond` — a small batch window so
  RequireAll's per-batch round trip does not degrade to one-RTT-per-
  message under light load.
- `Balancer: &kafkago.Hash{}` — the ADR-0021 rule (key-stable
  partitioning), unchanged here.

Async/high-throughput writers (none exist today) would be a separate,
explicitly-justified decision.

## Consequences

- Publish latency includes a broker round trip on every batch; the
  10ms window keeps batches from forming one message at a time under
  load. Measured cost in this service's volumes is negligible.
- A broker that cannot achieve ISR consensus fails the write — the
  caller sees the error and the row stays unpublished / the offset
  uncommitted. That is the correct failure mode: loud, retried, never
  silently lost.
- Unit tests pin the configuration on every construction path
  (`writer_config_test.go`, the DLQ writer config test), so a
  "helpful" future default change cannot regress it silently.

## Related

- ADR-0009 — the integration publisher.
- ADR-0018 — the outbox whose relay depends on this.
- ADR-0020 — the DLQ path whose durability fix prompted this record.
- ADR-0021 — the balancer half of the writer conventions.
