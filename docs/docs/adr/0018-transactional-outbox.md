---
id: 0018-transactional-outbox
title: 18. Transactional outbox + relay for atomic event publishing
sidebar_label: 18. Transactional outbox
sidebar_position: 18
description: "Aggregate saves and their domain event(s) now commit in one Postgres transaction via a per-event outbox_events table; a background relay drains it onto Kafka (both the integration and analytics topics), closing the dual-write gap ADR-0009 left open when it moved to direct publishing."
---

# 18. Transactional outbox + relay for atomic event publishing

## Status

**Accepted.**

## Context

ADR-0009 replaced the never-drained Postgres `events` table with a direct
`kafka.Publisher` because a durable-but-undrained outbox is worse than a
working direct publisher: no event had ever reached a downstream consumer.
That fixed reachability, but reopened the atomicity gap the original outbox
existed to close: `Repo.Save` and `Events.Publish` are two independent
network calls (Postgres, then Kafka) with no shared transaction. A crash, a
broker hiccup, or a deploy between them either loses an event the aggregate
change implies happened, or (with retries) publishes one for a write that
never committed. `inventory-storage`, `wes-work-planning`,
`workforce-management`, and `fulfillment-execution` all Conform to this
service's Published Language (ADR-0009) and have no way to tell such a gap
apart from a real state change.

`process-path-management` already solved exactly this with a
`ports.UnitOfWork` port, a Postgres `OutboxPublisher`/`OutboxRelay` pair, and
an `atomically` helper in its use cases (its ADR 0003). This ADR adopts that
shape for facility-layout, adapted to two differences already fixed by prior
ADRs here: `ports.EventPublisher.Publish` takes one event at a time (not
variadic), and this service fans every event out to TWO topics — the
integration topic (ADR-0009) and the analytics topic (ADR-0010) — which the
outbox and relay must preserve without letting the two streams diverge.

## Decision

1. **`ports.UnitOfWork`** — one new port, `Execute(ctx, func(ctx) error) error`,
   brackets a use case's repository `Save` and `Events.Publish` calls in one
   Postgres transaction. It is optional on every use case (a nil `UnitOfWork`
   falls back to running the wrapped function directly), so use cases work
   identically over the in-memory adapters and over Postgres without a
   database.

2. **`outbox_events` table** replaces the old, never-drained `events` table
   (migration 0005). One row is written **per (event × topic)**: `Publish`
   enqueues the already Kafka-encoded message for both the integration and
   the analytics topic in the same INSERT batch, inside the same transaction
   as the aggregate write. This is what keeps the two topics from disagreeing
   about what happened — they are written atomically together, not by two
   independent publish attempts.

3. **`kafka` package: split Encode from Send.** `Publisher.Encode` and
   `AnalyticsPublisher.Encode` turn a domain event into its wire-ready
   `Encoded` (topic, key, JSON value) without touching the network;
   `Publish` calls `Encode` then sends. `postgres.OutboxPublisher` calls only
   `Encode` (via the new `kafka.Encoder` interface), so the outbox and the
   direct-publish path share one encoding implementation and can never
   produce different bytes for the same event.

4. **`postgres.OutboxRelay`** polls `outbox_events` for unpublished rows
   (`FOR UPDATE SKIP LOCKED`, oldest first), sends each through a `Sink`
   (`kafka.RelaySink`, a topic-less writer that reads the topic per message
   off `Encoded.Topic`), and marks it published in the same row-locking
   transaction. At-least-once delivery: a crash between Send and the mark
   redelivers on the next pass. `FOR UPDATE SKIP LOCKED` lets two overlapping
   relay instances (a rolling deploy) drain concurrently without duplicate
   sends racing each other for the same row.

5. **Composition root** (`cmd/facility/main.go`): when both `DATABASE_URL`
   and `EVENT_PUBLISHER=kafka` are set, `EventPublisher` is the
   `OutboxPublisher` and a relay goroutine runs alongside the HTTP server,
   sharing its shutdown signal (`OUTBOX_RELAY_INTERVAL` tunes its poll
   interval). Every other combination (no database, or no Kafka) is
   unchanged from ADR-0009/ADR-0010's direct fan-out — there is no
   transaction to bind an outbox to without both being present.

The old `postgres/event_publisher.go` (the ADR-0009-era dead outbox) is
deleted outright rather than kept alongside the new table: two `events`-like
tables competing for the same job would be a standing source of confusion
about which one anything reads from.

## Consequences

- An aggregate save and its event(s) reaching Kafka are now atomic from the
  caller's perspective: either both persist (aggregate row + outbox row(s))
  or neither does, and the relay guarantees an outbox row that committed
  eventually reaches the broker.
- A crash between the relay's Send and its UPDATE redelivers that event.
  Consumers on both topics were already required to be idempotent as
  Conformists to an at-least-once Published Language (ADR-0009); this ADR
  does not change that requirement, only makes redelivery detectable via the
  stable `event_id` the outbox and the relay share.
- The relay adds one background goroutine and one extra table to operate;
  `outbox_events` needs the estate's usual retention/archival policy for a
  table that grows unbounded on `published_at`, same as
  `process-path-management`'s. *(That retention now exists:
  [ADR-0026](./0026-housekeeping-sweeper.md)'s sweeper deletes PUBLISHED
  rows past a retention, default 7d; unpublished rows are never swept.)*
- `EVENT_PUBLISHER=kafka` with no `DATABASE_URL` (an in-memory-repos,
  direct-Kafka deployment) is intentionally left non-transactional: there is
  no Postgres transaction to bind the publish to, so this mode is unchanged
  from ADR-0009/ADR-0010 and callers should treat it as best-effort, same as
  before this ADR.
