---
id: 0024-cloudevents-mandatory-envelope
slug: /adr/0024-cloudevents-mandatory-envelope
title: 24. CloudEvents 1.0 as the mandatory event envelope
sidebar_label: 24. CloudEvents 1.0 mandatory envelope
sidebar_position: 24
description: "Every Kafka message facility-layout produces or consumes — the warehouse.facility.events Published Language AND the warehouse.facility.analytics stream — is a CloudEvents 1.0 event in structured content mode, built and validated with the official sdk-go event package. The flat event_id/event_type/occurred_at envelope and the analytics schema_version field are removed with no coexistence. Supersedes the envelope sections of ADR-0009 and ADR-0010."
---

# 24. CloudEvents 1.0 as the mandatory event envelope

## Status

**Accepted** — fleet-wide standard, 2026-09-30. Implemented in the same change
that introduces this record.

Supersedes the envelope format of [ADR-0009](./0009-kafka-integration-publisher.md)
(the flat integration `Envelope`) and of [ADR-0010](./0010-analytical-data-product.md)
(the analytics "Envelope v1" with `schema_version`). Everything else those
records decide (one integration topic carrying the whole Published Language,
the separate analytics topic, data product, analytical database) stays in
force.

## Context

facility-layout published every domain event inside a hand-rolled, flat
"CloudEvents-like" envelope: `event_id`, `event_type`, `occurred_at`,
`source: "facility-layout"`, `data`. The analytics topic used the same shape
plus `schema_version`. Other fleet services drifted into their own variants,
and two of them (fulfillment-execution ADR-0027, wes-work-planning ADR-0021)
started dual-envelope migrations with an `EVENT_ENVELOPE_MODE` toggle. A
consumer in one context therefore had to know which hand-rolled shape each
producer used, and nothing validated any of them.

The fleet adopted one rule instead: **CloudEvents 1.0 is the only event
envelope, for every Kafka message, with no coexistence.** This record is
facility-layout's binding copy of that standard.

## Decision

### 1. Scope

Every message facility-layout writes to or reads from Kafka is a CloudEvents
1.0 event: the integration topic `warehouse.facility.events` (the whole
Published Language), the analytics topic `warehouse.facility.analytics`, and
this service's own analytics consumer (`cmd/facility-projector`). There is no
flat envelope, no dual-write, no dual-read and no envelope toggle.

### 2. Encoding

- CloudEvents **Kafka protocol binding, structured content mode**: the Kafka
  message value is the JSON event format.
- Every produced message carries the Kafka header
  `content-type: application/cloudevents+json; charset=UTF-8` — on the direct
  publish path and on the outbox relay path (`RelaySink`).
- The Kafka **key is unchanged** (the raising aggregate's identity, or the
  event type for the few events that historically keyed on it) and the writers
  keep `kafkago.Hash{}` ([ADR-0021](./0021-kafka-writer-balancer-hash.md)).
- Events are built, validated and (un)marshalled with
  `github.com/cloudevents/sdk-go/v2/event` (v2.16.2) only. Transport stays
  `segmentio/kafka-go`; the sdk-go protocol/client packages are not used.
- The single home for all of this is
  `internal/adapters/kafka/cloudevents` (`New`, `Decode`, `ContentTypeHeader`,
  `Type`, `DataSchema`). No other code builds an envelope.

### 3. Context attributes (all required)

| attribute | value |
|---|---|
| `specversion` | `1.0` |
| `id` | UUID v4 minted **once** per domain event by `OutboxPublisher` and persisted inside the outbox row's pre-encoded value, so a relay redelivery carries the same `id`; the same id is shared by the event's integration and analytics rows. `(source, id)` is the consumer idempotency key. |
| `source` | `/warehouse/facility-layout` |
| `type` | `com.warehouse.wms.facility-layout.<entity>.<EventName>` — byte-identical to the domain event's own `EventType()` |
| `subject` | id of the aggregate instance (see table below) |
| `time` | the domain event's occurred-at, UTC, RFC 3339 |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:warehouse:facility-layout:<events\|analytics>:<EventName>:v1` |

`data` is the domain event's own JSON, byte-for-byte the payload shape that was
published before this change (including its `eventName`/`eventType`/
`occurredAt` fields). No extension attributes. The analytics `schema_version`
field is removed; `dataschema` replaces it.

### 4. Published types and subjects

| `type` | `subject` | Kafka key |
|---|---|---|
| `com.warehouse.wms.facility-layout.site.SiteRegistered` | `siteCode` | `siteCode` |
| `com.warehouse.wms.facility-layout.zone.ZoneRegistered` | `zoneId` | `zoneId` |
| `com.warehouse.wms.facility-layout.aisle.AisleRegistered` | `aisleId` | `aisleId` |
| `com.warehouse.wms.facility-layout.aisle.AisleGeometryUpdated` | `aisleId` | event type (unchanged) |
| `com.warehouse.wms.facility-layout.locationtype.LocationTypeRegistered` | `locationType` | `locationType` |
| `com.warehouse.wms.facility-layout.placementrule.PlacementRuleDefined` | `ruleId` | `ruleId` |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` | `locationCode` | `locationCode` |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned` | `locationCode` | `locationCode` |
| `com.warehouse.wms.facility-layout.locationslot.LocationGeometryUpdated` | `locationCode` | event type (unchanged) |
| `com.warehouse.wms.facility-layout.locationslot.FacilityLayoutImported` | `layout-import` (a batch, no single aggregate) | event type (unchanged) |
| `com.warehouse.wms.facility-layout.structure.FixedStructureRegistered` | `structureId` | event type (unchanged) |
| `com.warehouse.wms.facility-layout.crossaisle.CrossAisleRegistered` | `<zoneId>/<fromAisle>-<toAisle>@<atBay>` | event type (unchanged) |

The same `type` is used on both topics; `dataschema` tells the integration
payload from the analytics payload.

**Cross-service contract** — these strings are consumed by inventory-storage
and must stay byte-identical on both sides:

    com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered
    com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned
    com.warehouse.wms.facility-layout.zone.ZoneRegistered

**Fleet cross-service type catalogue** (from the fleet standard; consumers
dispatch on these exact strings — facility-layout itself consumes none of
the other services' types):

| `type` | consumed by |
|---|---|
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` | inventory-storage |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned` | inventory-storage |
| `com.warehouse.wms.facility-layout.zone.ZoneRegistered` | inventory-storage |
| `com.warehouse.wms.inventory-storage.reservation.StockReserved` | wes-work-planning |
| `com.warehouse.wms.inventory-storage.reservation.ReservationRevoked` | wes-work-planning |
| `com.warehouse.wes.order-management.order.OrderAllocated` | wes-work-planning |
| `com.warehouse.wes.order-management.order.OrderPartiallyAllocated` | wes-work-planning |
| `com.warehouse.wes.order-management.order.OrderRepromised` | (published contract) |
| `com.warehouse.wes.process-path-management.processpath.ProcessPathCreated` | fulfillment-execution, wes-work-planning, workforce-management, order-management |
| `com.warehouse.wes.process-path-management.processpath.ProcessPathUpdated` | same four |
| `com.warehouse.wes.process-path-management.processpath.ProcessPathDeactivated` | same four |
| `com.warehouse.wes.process-path-management.cptschedule.CPTScheduleChanged` | order-management |
| `com.warehouse.wes.labor-performance.performance.TaskPerformanceRecorded` | workforce-management, labor-performance |
| `com.warehouse.wes.workforce-management.shiftplan.ShiftPlanCommitted` | wes-work-planning |
| `com.warehouse.wes.fulfillment-execution.task.TaskCompleted` | wes-work-planning, labor-performance |
| `com.warehouse.wes.fulfillment-execution.task.TaskCPTMissed` | order-management |
| `com.warehouse.wes.fulfillment-execution.package.PackageManifested` | order-management |
| `com.warehouse.wes.work-planning.workunit.WorkReleased` | fulfillment-execution |
| `com.warehouse.wes.work-planning.workpool.PathCapacityChanged` | order-management |

### 5. Consumer rules

facility-layout's one consumer is the analytics projector
(`internal/adapters/inbound/kafka/analytics_consumer.go`). It:

1. decodes with `cloudevents.Decode` (SDK unmarshal + `Validate()`); anything
   that is not a valid CloudEvents 1.0 event — including a legacy flat
   message — is deterministic poison: it is sent **straight** to the existing
   `<topic>.dlq` (no in-process retries) and the offset is committed. It never
   crashes, never blocks the partition, never falls back to a flat parse;
2. dispatches on the **full** `type` string (no suffix or short-name match);
   unknown types are ignored;
3. dedupes on the CloudEvents `id` (the `analytics_*` tables keep their
   `event_id` column, now populated from `id`);
4. reads `time` from the context attributes and the payload via `DataAs`.

### 6. Versioning

Additive payload changes keep the type and dataschema. A breaking payload
change requires a new `dataschema` version **and** a new type with a `.v2`
suffix, published as a new event — never a mutation of the old one.

### 7. Cutover

No coexistence: all fleet PRs merge and deploy as one set. Before deploying,
drain the outbox (rows written before this change hold flat-shaped values),
then delete and recreate `warehouse.facility.events` and
`warehouse.facility.analytics` so no flat message remains for a FirstOffset
replay. See warehouse-infra `docs/cloudevents-cutover.md`.

## Consequences

**Easier**

- One validated, spec-defined envelope across the fleet; any CloudEvents
  tooling can read the topics.
- The analytics and integration payloads are told apart by `dataschema`, not
  by a home-grown version integer.
- A legacy or malformed message is rejected loudly to the DLQ instead of
  being half-parsed.

**Harder**

- A **breaking wire change**: every consumer of `warehouse.facility.events`
  (inventory-storage today) must deploy the matching change at the same time.
- Outbox rows written before the cutover are not re-encoded; the outbox must
  be drained before deploy, and topics recreated.
- `data` still repeats `eventName`/`eventType`/`occurredAt` from the domain
  event's struct tags. That duplication is kept deliberately (the payload
  shape must not change in an envelope migration) and can only be removed as
  a `.v2` type.
- The sdk-go event package becomes a runtime dependency of the adapters.
