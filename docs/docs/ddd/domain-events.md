---
id: domain-events
title: Domain events
sidebar_label: Domain events
description: The twelve past-tense facts this context publishes, their CloudEvents type convention, their payload shapes, and who consumes them.
---

# Domain events

This bounded context emits twelve past-tense domain events. Together they are
its **Published Language** — the vocabulary downstream Conformists key off.

:::info[Published to Kafka, specified in AsyncAPI]
With `EVENT_PUBLISHER=kafka`, every domain event is published to the
`warehouse.facility.events` integration topic
([ADR 0009](../adr/0009-kafka-integration-publisher.md)) and, in the same
call, to the separate `warehouse.facility.analytics` topic that feeds the
[analytical data product](../analytics/catalog-growth-report.md)
([ADR 0010](../adr/0010-analytical-data-product.md)). The integration topic,
its CloudEvents 1.0 envelope
([ADR 0024](../adr/0024-cloudevents-mandatory-envelope.md)) and all twelve messages are specified in
[`apis/asyncapi.yaml`](https://github.com/IQVO/facility-layout/blob/main/apis/asyncapi.yaml)
(AsyncAPI 2.6.0).

With `DATABASE_URL` **and** `EVENT_PUBLISHER=kafka` set, publishing goes
through the transactional outbox (`outbox_events`,
[ADR 0018](../adr/0018-transactional-outbox.md)): the use case writes the
aggregate row and one outbox row per topic in one transaction, and the
in-process relay drains them onto Kafka. Without `EVENT_PUBLISHER=kafka`,
events go to the service log only — whether or not `DATABASE_URL` is set
(the old `events` table was dropped by migration `0005_outbox`). See
[Publishers](#publishers) and the [Context map](../ecosystem/context-map.md)
for who consumes what.
:::

## The type convention

Identical to the other warehouse-systems services: reverse-DNS,
lowercase except the final PascalCase event name, and the entity segment
carries no hyphen even for multi-word aggregate names.

```
com.warehouse.<subdomain>.<bounded-context>.<entity>.<EventName>
```

This service's **subdomain segment is `wms`**. "Bin-accurate location" is
classified WMS-tier in the e-commerce-fulfillment reference — the "Inventory &
Slotting" Core subdomain references it as WMS's Open Host Service — and this
service is the generalized, multi-consumer version of that same concern.

```
com.warehouse.wms.facility-layout.site.SiteRegistered
com.warehouse.wms.facility-layout.zone.ZoneRegistered
com.warehouse.wms.facility-layout.aisle.AisleRegistered
com.warehouse.wms.facility-layout.locationtype.LocationTypeRegistered
com.warehouse.wms.facility-layout.placementrule.PlacementRuleDefined
com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered
com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned
com.warehouse.wms.facility-layout.locationslot.FacilityLayoutImported
com.warehouse.wms.facility-layout.locationslot.LocationGeometryUpdated
com.warehouse.wms.facility-layout.aisle.AisleGeometryUpdated
com.warehouse.wms.facility-layout.structure.FixedStructureRegistered
com.warehouse.wms.facility-layout.crossaisle.CrossAisleRegistered
```

Entity segments in use: `site`, `zone`, `aisle`, `locationtype`,
`placementrule`, `locationslot`, `structure`, `crossaisle`.

## The DomainEvent interface

```go
// DomainEvent is a past-tense fact published by an aggregate. Adapters
// (outbound/events) serialize and publish these; the domain never depends
// on the publishing mechanism. EventType is this context's Published
// Language: downstream Conformists key off it.
type DomainEvent interface {
	EventName() string
	EventType() string
	OccurredAt() time.Time
}
```

Every event embeds the same base, serialized as:

```json
{
  "eventName": "LocationSlotRegistered",
  "eventType": "com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered",
  "occurredAt": "2026-08-22T09:00:00Z"
}
```

`OccurredAt` comes from the injected `Clock` port, never from
`time.Now()` inside the domain — which is what makes event timestamps
deterministic in tests.

## The twelve events

### SiteRegistered

A physical facility was added to the warehouse map.

| Field | Type |
|---|---|
| `siteCode` | string |
| `siteName` | string |

### ZoneRegistered

A behavioral zone was added inside a Site's area.

| Field | Type |
|---|---|
| `zoneId` | string (`WH1-STOR-AMB`) |
| `siteCode` | string |
| `areaCode` | string |
| `zoneCode` | string |
| `temperatureClass` | `Ambient` \| `Chilled` \| `Frozen` |
| `hazmat` | boolean |

### AisleRegistered

A physical corridor was added inside a Zone.

| Field | Type |
|---|---|
| `aisleId` | string (`WH1-STOR-AMB-A07`) |
| `zoneId` | string |
| `aisleCode` | string |
| `sequenceHint` | integer — walk-order position |
| `direction` | `OneWay` \| `TwoWay` |

This is the event a travel-path consumer cares about most: `sequenceHint`
and `direction` are the two structural inputs to walk-order reasoning.

### LocationTypeRegistered

A reusable slot shape/kind was defined.

| Field | Type |
|---|---|
| `locationType` | string (`PalletRack`) |
| `role` | `LocationRole` — `Storage` (default), `Dock`, `Yard`, `WorkCenter`, `Drop`, `Staging`, `QC`, `Consolidation`, `Shipping` |
| `maxWeightKg` | number — omitted when zero |
| `maxVolumeM3` | number — omitted when zero |

Capacity is optional for the roles that do not hold stock at rest (`Dock`,
`Yard`, `WorkCenter`, `QC`, `Shipping`), so the two capacity fields may be
absent ([ADR 0016](../adr/0016-functional-location-roles.md)).

### PlacementRuleDefined

A rule constraining which LocationTypes are legal in which Zones was
declared.

| Field | Type |
|---|---|
| `ruleId` | string |
| `locationType` | string |
| `effect` | `Allow` \| `Deny` |
| `predicate` | string — the human-readable predicate, e.g. `temperatureClass=Frozen` |

### LocationSlotRegistered

A coded leaf slot now exists on the warehouse map. This is the event
`inventory-storage` consumes to learn that a new `Bin` location is legal to
stow into.

| Field | Type |
|---|---|
| `locationCode` | string (`WH1-STOR-AMB-A07-03-02-B`) |
| `aisleId` | string |
| `zoneId` | string |
| `locationType` | string |
| `role` | `LocationRole`, inherited from the LocationType |
| `dockFlow` | `Inbound` \| `Outbound` \| `Both` — only on a `Dock` slot |
| `activities` | array of `Pack` \| `Sort` \| `QC` \| `VAS` \| `Deconsolidate` \| `Receive` \| `Kit` — only on a `WorkCenter` slot |
| `maxWeightKg` | number — omitted when zero |
| `maxVolumeM3` | number — omitted when zero |

`aisleId` and `zoneId` are denormalised into the payload deliberately: a
consumer should not have to know how to parse this context's code format to
route on zone. That is what makes it a *Published Language* rather than a
leaked internal representation.

### LocationSlotDecommissioned

A coded slot was permanently retired. The signal for a downstream context to
stop offering that location for new work.

| Field | Type |
|---|---|
| `locationCode` | string |

### FacilityLayoutImported

A bulk layout import completed. Emitted **once per import call**; the
individual `LocationSlotRegistered` events still fire per-slot within the
same import.

| Field | Type |
|---|---|
| `rowsSubmitted` | integer |
| `slotsImported` | integer |
| `rowsRejected` | integer |

The summary event exists so that a consumer can distinguish "the building was
loaded" from a burst of unrelated single registrations, without having to
infer it from event volume.

### LocationGeometryUpdated

A slot's physical position and size were set
([ADR 0017](../adr/0017-geometry-and-travel-graph.md)).

| Field | Type |
|---|---|
| `locationCode` | string |
| `xM`, `yM`, `zM` | number — position in metres |
| `widthM`, `depthM`, `heightM` | number — dimensions in metres |
| `pickSequence` | integer — optional |

### AisleGeometryUpdated

An aisle's travel centreline was set.

| Field | Type |
|---|---|
| `aisleId` | string |
| `startXM`, `startYM`, `startZM` | number |
| `endXM`, `endYM`, `endZM` | number |
| `lengthM` | number |

### FixedStructureRegistered

A non-slot physical obstacle was added to a site.

| Field | Type |
|---|---|
| `structureId` | string |
| `siteCode` | string |
| `kind` | `Wall` \| `Column` \| `Office` \| `Conveyor` \| `Other` |
| `xM`, `yM`, `zM`, `widthM`, `depthM`, `heightM` | number — footprint in metres |
| `label` | string |

### CrossAisleRegistered

A walkable connection between two aisles of the same zone was declared.

| Field | Type |
|---|---|
| `zoneId` | string |
| `fromAisle` | string |
| `toAisle` | string |
| `atBay` | string |

## Catalogue at a glance

Every event is published to **both** topics when `EVENT_PUBLISHER=kafka`:
`warehouse.facility.events` (integration, `dataschema`
`urn:warehouse:facility-layout:events:<EventName>:v1`) and
`warehouse.facility.analytics` (analytics, `dataschema`
`urn:warehouse:facility-layout:analytics:<EventName>:v1`), under the **same**
CloudEvents `id`. The Kafka key is the partition key; the CloudEvents
`subject` names the aggregate instance. Every event is keyed by its
aggregate's identity (so all events about one aggregate share a partition and
stay ordered); the only exception is `FacilityLayoutImported`, a batch outcome
with no aggregate, which is keyed by its CloudEvents `id`
([ADR 0032](../adr/0032-aggregate-partition-keys-for-geometry-events.md)).

| CloudEvents `type` | Kafka key (partition) | `subject` | Producer use case(s) | Known consumers |
|---|---|---|---|---|
| `com.warehouse.wms.facility-layout.site.SiteRegistered` | `siteCode` | `siteCode` | `RegisterSite`, `ImportFacilityLayout` | own projector |
| `com.warehouse.wms.facility-layout.zone.ZoneRegistered` | `zoneId` | `zoneId` | `RegisterZone`, `ImportFacilityLayout` | `inventory-storage`, own projector |
| `com.warehouse.wms.facility-layout.aisle.AisleRegistered` | `aisleId` | `aisleId` | `RegisterAisle`, `ImportFacilityLayout` | own projector |
| `com.warehouse.wms.facility-layout.locationtype.LocationTypeRegistered` | `locationType` | `locationType` | `RegisterLocationType` | own projector |
| `com.warehouse.wms.facility-layout.placementrule.PlacementRuleDefined` | `ruleId` | `ruleId` | `DefinePlacementRule` | own projector |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` | `locationCode` | `locationCode` | `RegisterLocationSlot`, `ImportFacilityLayout` | `inventory-storage`, `warehouse-planning`, own projector |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned` | `locationCode` | `locationCode` | `DecommissionLocationSlot` | `inventory-storage`, `warehouse-planning`, own projector |
| `com.warehouse.wms.facility-layout.locationslot.FacilityLayoutImported` | event `id` (batch outcome, no aggregate) | `layout-import` | `ImportFacilityLayout` | own projector |
| `com.warehouse.wms.facility-layout.locationslot.LocationGeometryUpdated` | `locationCode` | `locationCode` | `SetLocationGeometry`, `ImportFacilityLayout` | none |
| `com.warehouse.wms.facility-layout.aisle.AisleGeometryUpdated` | `aisleId` | `aisleId` | `SetAisleGeometry` | none |
| `com.warehouse.wms.facility-layout.structure.FixedStructureRegistered` | `structureId` | `structureId` | `RegisterFixedStructure` | none |
| `com.warehouse.wms.facility-layout.crossaisle.CrossAisleRegistered` | `zoneId/fromAisle-toAisle@atBay` | `zoneId/fromAisle-toAisle@atBay` | `RegisterCrossAisle` | none |

Source: `internal/domain/shared/events.go`,
`internal/adapters/outbound/kafka/publisher.go` (`aggregateKey`,
`partitionKey`, `SubjectOf`), `internal/adapters/inbound/kafka/analytics_consumer.go`.

## Which use case emits what

| Use case | Events emitted |
|---|---|
| `RegisterSite` | `SiteRegistered` |
| `RegisterZone` | `ZoneRegistered` |
| `RegisterAisle` | `AisleRegistered` |
| `RegisterLocationType` | `LocationTypeRegistered` |
| `DefinePlacementRule` | `PlacementRuleDefined` |
| `RegisterLocationSlot` | `LocationSlotRegistered` |
| `DecommissionLocationSlot` | `LocationSlotDecommissioned` |
| `ImportFacilityLayout` | per row: `SiteRegistered` / `ZoneRegistered` / `AisleRegistered` for each parent it creates on first sight, `LocationSlotRegistered` for each successful slot, `LocationGeometryUpdated` when the row carries geometry; then exactly one `FacilityLayoutImported` for the whole call |
| `SetLocationGeometry` | `LocationGeometryUpdated` |
| `SetAisleGeometry` | `AisleGeometryUpdated` |
| `RegisterFixedStructure` | `FixedStructureRegistered` |
| `RegisterCrossAisle` | `CrossAisleRegistered` |
| `GetSiteLayout`, `GetZoneGrid`, `GetZoneTravelGraph`, `EstimateTravelDistance`, `ListLocationsByRole` and the other reads | **none** — read models never write and never publish |

## Publishers

Which publisher the composition root (`cmd/facility/main.go`,
`buildAdapters`) wires depends on two switches:

| `DATABASE_URL` | `EVENT_PUBLISHER` | Publisher wired | Outbox relay |
|---|---|---|---|
| unset | unset | `outbound/events` log publisher | no |
| unset | `kafka` | `outbound/kafka` `FanOut` — direct publish to both topics, no outbox | no |
| set | unset | `outbound/events` log publisher | no |
| set | `kafka` | `outbound/postgres` `OutboxPublisher` — one `outbox_events` row per topic, in the use case's transaction | yes |

| Adapter | Use |
|---|---|
| `outbound/kafka` — `Publisher` | Encodes each event as a CloudEvents 1.0 event (structured mode, `source=/warehouse/facility-layout`, `type` = the event type, `subject` = aggregate id, `dataschema=urn:warehouse:facility-layout:events:<EventName>:v1`, `data` = the event's JSON wire DTO (`cloudevents.WireData`; domain events carry no struct tags); [ADR-0024](../adr/0024-cloudevents-mandatory-envelope.md)) for `warehouse.facility.events`. Writer: `kafkago.Hash` balancer ([ADR 0021](../adr/0021-kafka-writer-balancer-hash.md)), `RequireAll` acks with a 10 ms batch timeout ([ADR 0031](../adr/0031-kafka-writer-durability.md)); the W3C trace context of the publishing request is injected into the message headers. |
| `outbound/kafka` — `AnalyticsPublisher` | The same occurrence (same `type` and `id`) with `dataschema=urn:warehouse:facility-layout:analytics:<EventName>:v1`, for `warehouse.facility.analytics`. |
| `outbound/kafka` — `FanOut` | No-database + `kafka` mode: mints one id and sends the event through both publishers above. |
| `outbound/postgres` — `OutboxPublisher` + `OutboxRelay` | Database + `kafka` mode: enqueues both encodings into `outbox_events` inside the use case's `UnitOfWork`; the relay (`OUTBOX_RELAY_INTERVAL`, default 1s, batches of 100, `FOR UPDATE SKIP LOCKED`) sends them through `RelaySink` ([ADR-0018](../adr/0018-transactional-outbox.md)). Published rows are deleted after `OUTBOX_RETENTION` by the housekeeping sweeper ([ADR 0026](../adr/0026-housekeeping-sweeper.md)). |
| `outbound/events` — log publisher | Default whenever `EVENT_PUBLISHER` is not `kafka`. Writes each event to the service log. |
| `outbound/events` — buffered publisher | Tests. Collects events in memory for assertion. |

The `EventPublisher` port is one method —
`Publish(ctx context.Context, event shared.DomainEvent) error` — which is why
the Kafka adapters were a purely additive change.

## Who consumes them

| Consumer | Topic | Group | Events used |
|---|---|---|---|
| `inventory-storage` (`internal/adapters/outbound/facilitycache`, with `LOCATION_LOOKUP_MODE=kafka`) | `warehouse.facility.events` | process-unique group, replays from the first offset | `ZoneRegistered`, `LocationSlotRegistered`, `LocationSlotDecommissioned` — the rest are ignored ([ADR 0013](../adr/0013-first-published-language-consumer.md)) |
| `warehouse-planning` (`internal/adapters/inbound/kafka/storage_capacity_consumer.go`, started when `KAFKA_BROKERS` is set) | `warehouse.facility.events` | `STORAGE_CAPACITY_CONSUMER_GROUP` (default `warehouse-planning-storage-capacity`) | `LocationSlotRegistered` (Storage and WorkCenter roles), `LocationSlotDecommissioned` — folded into a storage-position / station tally |
| this repository's `cmd/facility-projector` | `warehouse.facility.analytics` | `facility-analytics` (DLQ `warehouse.facility.analytics.dlq`) | The original eight events (`SiteRegistered` … `FacilityLayoutImported`); the four geometry events are ignored |

No other service consumes either topic today. The geometry, fixed-structure
and cross-aisle events are Published Language with no consumer yet.
