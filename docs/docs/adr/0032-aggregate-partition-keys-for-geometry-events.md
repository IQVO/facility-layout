---
id: 0032-aggregate-partition-keys-for-geometry-events
title: "32. Key geometry, structure and cross-aisle events by aggregate identity"
sidebar_label: 32. Aggregate partition keys for geometry events
sidebar_position: 32
description: "LocationGeometryUpdated, AisleGeometryUpdated, FixedStructureRegistered and CrossAisleRegistered were keyed by their event-type string, so every occurrence of a type shared one partition. They are now keyed by their aggregate identity like every other event; FacilityLayoutImported, a batch outcome with no aggregate, is keyed by its CloudEvents id."
---

# 32. Key geometry, structure and cross-aisle events by aggregate identity

## Status

**Accepted.** Extends ADR-0021 (the Hash balancer made the key decisive for
partition placement) and ADR-0024 (CloudEvents envelope). It changes the
*value* of the Kafka message key for five event types; payloads, `type`,
`subject`, `dataschema` and topics are unchanged.

## Context

`aggregateKey` (`internal/adapters/outbound/kafka/publisher.go`) returned the
aggregate identity for the registration/decommission events, but fell back to
`event.EventType()` for the five events added later by ADR-0016/0017 and the
bulk import: `LocationGeometryUpdated`, `AisleGeometryUpdated`,
`FixedStructureRegistered`, `CrossAisleRegistered` and
`FacilityLayoutImported`. With the Hash balancer (ADR-0021) a constant key
means **every occurrence of one event type lands on one partition**, on both
`warehouse.facility.events` and `warehouse.facility.analytics`:

- partition skew: a site-wide geometry load (thousands of
  `LocationGeometryUpdated`) piles onto a single partition of eight;
- no ordering between a location's geometry update and its
  `LocationSlotRegistered` (different keys → different partitions), so a
  future consumer could see geometry for a location it has not yet learned.

The `subject` already named the real aggregate; only the key did not.

### Consumers checked before changing the key

Read from `origin/develop` of each consumer:

| Consumer | What it consumes | Ordering assumption on the five types |
|---|---|---|
| `inventory-storage` `facilitycache.Consumer` | `ZoneRegistered`, `LocationSlotRegistered`, `LocationSlotDecommissioned`; everything else ignored (still counted toward readiness via `observe`) | none — idempotent upserts keyed by aggregate; zones and slots deliberately held in separate maps because they carry no cross-aggregate order |
| `warehouse-planning` `StorageCapacityConsumer` | `LocationSlotRegistered`, `LocationSlotDecommissioned`; other types return nil | none — and the two types it reads are not re-keyed |
| facility-layout `AnalyticsConsumer` (`cmd/facility-projector`) | the eight catalog-change types; `FacilityLayoutImported` is projected, the four geometry types are ignored | `FacilityLayoutImported` is an additive per-event-id counter (`IsProcessed`/`MarkProcessed`) — order-independent |
| `e2e-tests` `warehouse-day` | lists the topic name only | none |

No consumer relies on the previous (event-type) keying, and none reads the
four geometry/structure types today. Re-keying is therefore not a behavioural
change for any existing consumer.

## Decision

1. `aggregateKey` returns the aggregate identity for the four geometry-family
   events:
   - `LocationGeometryUpdated` → `locationCode` (same key as the location's
     `LocationSlotRegistered`/`Decommissioned`, so they are ordered together);
   - `AisleGeometryUpdated` → `aisleId` (same key as `AisleRegistered`);
   - `FixedStructureRegistered` → `structureId`;
   - `CrossAisleRegistered` → the cross-aisle's composite identity
     `<zoneId>/<fromAisle>-<toAisle>@<atBay>` (its aggregate identity, equal to
     its CloudEvents `subject`).
   For these events the key now equals the `subject`.
2. `FacilityLayoutImported` summarises a whole `ImportFacilityLayout` call. It
   has no aggregate, and its payload carries no site or zone, so there is no
   identity to key on without inventing one. Its key is its **CloudEvents
   `id`** (`partitionKey`): import batches spread over the partitions, and an
   outbox redelivery of the same event keeps the same `id` and therefore the
   same partition. Its `subject` stays the fixed `layout-import`. No ordering
   is promised between an import summary and the per-slot events it
   accompanies; no consumer needs one.
3. The event-type fallback remains in `aggregateKey` only as a guard for a
   future event added without a case; a unit test rejects an event-type key for
   every event in the golden catalogue.
4. Wire format, `type`, `subject`, `dataschema`, payloads and topics are
   **unchanged**; this is backward-compatible for every consumer, which may
   never depend on the key value of an event type it does not consume.

## Consequences

- Geometry bulk loads spread over the partitions; per-location and per-aisle
  order now includes the geometry events.
- Messages already in the topics (and any outbox rows not yet relayed — the
  key is stored in the outbox row at insert time) keep their old key. Only the
  order *across* the cut-over for one aggregate's geometry event is not
  guaranteed, which no consumer reads today. Partition assignment of existing
  history is not rewritten.
- If a future consumer needs site-scoped ordering of imports, that requires an
  additive `siteCode` field on `FacilityLayoutImported` (a contract change and
  a product decision) — out of scope here.
- Proven by `TestPublisher_GoldenCloudEventPerType` /
  `TestPublisher_KeysAreAggregateIdentityNotEventType` (unit) and
  `TestPublisherKeysGeometryEventsByAggregateOntoTheirLifecyclePartition`
  (testcontainers Kafka, 8 partitions).

## Related

- ADR-0009 — the integration publisher; ADR-0017 — the geometry events.
- ADR-0018 — the outbox (key persisted with the row).
- ADR-0021 — Hash balancer; ADR-0024 — CloudEvents envelope.
