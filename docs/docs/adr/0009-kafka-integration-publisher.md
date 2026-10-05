---
id: 0009-kafka-integration-publisher
title: 9. Kafka integration publisher for the Published Language
sidebar_label: 9. Kafka integration publisher
sidebar_position: 9
description: "facility-layout publishes its full Published Language (every domain event) to the warehouse.facility.events Kafka topic via a new outbound adapter, so downstream Conformist services actually receive the events this Open Host Service promises — replacing the never-drained transactional outbox as the live integration mechanism."
---

# 9. Kafka integration publisher for the Published Language

## Status

**Accepted; envelope format superseded by [ADR-0024](./0024-cloudevents-mandatory-envelope.md).**
The topic, the "publish the whole Published Language" decision and the
aggregate-id key still stand. The flat `Envelope` described below
(`event_id`/`event_type`/`occurred_at`/`source`/`data`) was removed:
every message is now a CloudEvents 1.0 event (Superseded by ADR-0024).

## Context

CLAUDE.md's Strategic classification section declares this service an **Open
Host Service** with a **Published Language**: its domain events ARE its
integration contract, and `inventory-storage`, `wes-work-planning`,
`workforce-management`, and `fulfillment-execution` are all **Conformists** to
them.

Until now, that Published Language never actually left the service. The
`EventPublisher` port had two implementations: a `LogPublisher` (local/dev)
and a Postgres `EventPublisher` that appends each event to an `events` table —
a **transactional outbox**. An outbox is only half of the pattern: it makes the
event durable in the same transaction as the state change, but something must
**drain** it to the broker. No such relay exists, so no event has ever reached
a downstream consumer. The other five services in the estate publish directly
to a `warehouse.<context>.events` Kafka topic; facility-layout is the only one
without a live integration path.

The forces:

- **Estate consistency.** Five services already use a direct
  `outbound/kafka` publisher writing to `warehouse.<context>.events`. A single
  broker/topic convention across the mesh is worth more than facility-layout
  being individually "more correct" with a bespoke outbox-relay.
- **Open Host Service semantics.** Unlike a service that forwards a single
  enriched event, an OHS publishes its **whole** Published Language. Every one
  of the eight domain events is part of the contract, so the publisher must
  emit all of them, not a curated subset.
- **The events already self-serialize.** Each event carries JSON struct tags
  and a stable `EventType()` (the CloudEvents-style
  `com.warehouse.wms.facility-layout.<entity>.<Event>`). The envelope can carry
  the event's own JSON verbatim as `data`; no per-event marshalling switch is
  needed.
- **~~This service has no OTel wiring.~~** *(Resolved 2026-10.)* The other
  services inject W3C trace context into Kafka headers; facility-layout
  originally had no observability package, so trace propagation across
  the message boundary was deliberately out of scope and left as a
  follow-up. That follow-up is now closed: the outbound Kafka adapters
  (`Publisher`, `AnalyticsPublisher`, `RelaySink`) inject the W3C trace
  context of the publishing context into every message's headers via the
  telemetry package's process-wide propagator, so a publish→consume hop
  is one trace. It is additive and does not change the contract.

## Decision

Add a new outbound adapter `internal/adapters/outbound/kafka` with a
`Publisher` that satisfies `ports.EventPublisher` and publishes **every** domain
event to the topic **`warehouse.facility.events`**, each wrapped in the shared
CloudEvents-like `Envelope` (`event_id`, `event_type`, `occurred_at`, `source`,
`data`) — *superseded by [ADR-0024](./0024-cloudevents-mandatory-envelope.md):
now a CloudEvents 1.0 event in structured mode*. The message key is the raising aggregate's identity (site code, zone
id, aisle id, location code, …) so per-aggregate order is preserved on a
partition.

The publisher is selected by `EVENT_PUBLISHER=kafka`, independently of the
repository choice, so the Published Language reaches the broker whether the
store is Postgres or in-memory. With `EVENT_PUBLISHER` unset the prior behaviour
is unchanged: the Postgres outbox when a database is configured, the log
publisher otherwise.

The domain and application layers are **not** modified: this is purely a new
adapter plus a composition-root wiring branch. `EventPublisher` already takes a
single event, and every use case already calls it.

## Consequences

- Downstream Conformists can finally consume facility-layout's Published
  Language over Kafka, unblocking their planned integrations and this service's
  own future analytical data product (the per-service analytics mesh).
- The `events` outbox table remains available but is no longer the live
  integration path when `EVENT_PUBLISHER=kafka`. A true outbox-relay (drain the
  table to Kafka for exactly-once, crash-safe delivery) remains a viable future
  upgrade behind the same port, should at-least-once direct publishing prove
  insufficient.
- Trace propagation across the Kafka boundary is a known follow-up: once this
  service gains an observability package, the publisher should inject W3C trace
  context into message headers as the other services do.
- Cold-start caveat (verified live): with `AllowAutoTopicCreation`, the first
  publish to a not-yet-existing topic can fail while the broker creates it, and
  because the use cases propagate a publish error, the triggering HTTP write
  returns 500 until the topic exists. This is inherent to the estate's inline-
  publish design (all services share it) and is mitigated in real deployments
  by pre-creating topics rather than relying on auto-creation.
- `github.com/segmentio/kafka-go` and `github.com/google/uuid` become direct
  dependencies (the versions already pinned across the estate).
