---
id: consuming-this-service
title: Consuming this service
sidebar_label: Consuming this service
description: How a downstream context integrates — synchronous REST, Kafka events, or MCP — and which of those are in use today.
---

# Consuming this service

:::info[Current status]
Five services consume `facility-layout` today: `inventory-storage` over
Kafka (with a REST fallback), `warehouse-planning` over Kafka,
`wes-work-planning` and `fulfillment-execution` over REST, and
`warehouse-ops-agent` over MCP (plus the catalog-growth report REST). See the
[Context map](./context-map.md) for the exact endpoints, events and switches
each one uses.
:::

## Integration styles, by need

| Need | Style | In use by |
|---|---|---|
| "Is this exact location valid, right now, before I accept this stow?" | **Synchronous REST** — `GET /locations/{locationCode}` or `/classification` | `inventory-storage` (`LOCATION_LOOKUP_MODE=http`), `fulfillment-execution` (role lookup) |
| "Keep a local read model of the building's structure in sync" | **Event subscription** — `warehouse.facility.events` | `inventory-storage` (`LOCATION_LOOKUP_MODE=kafka`), `warehouse-planning` (storage-capacity tally) |
| "How far apart are these two locations?" | **Synchronous REST** — `GET /distance?from=&to=` | `wes-work-planning` |
| "Let an agent explore the map" | **MCP** — `cmd/mcp`, Streamable HTTP | `warehouse-ops-agent` |

Every current consumer defaults to a `permissive` mode, so none of them needs
this service to be up in order to start.

## Synchronous validation

The narrowest useful integration, and the one that needs nothing new:

```bash
curl -i localhost:8080/locations/WH1-STOR-AMB-A07-03-02-B
```

- `200` with `"status": "Active"` → the location is real and legal to use.
- `200` with `"status": "Decommissioned"` or `"UnderMaintenance"` → it exists
  but must not be used for new work.
- `404` → it is not on the map. Whatever produced that code is wrong.
- `400` → the code is not seven valid `[A-Z0-9]` segments, so it was never a
  location at all.

The important property: because
[PlacementRules are enforced at registration time](../adr/0003-placement-rules-at-registration-time.md),
a consumer that reads back an `Active` slot can treat it as
**already-validated**. It does not need to re-check temperature class,
hazmat, or capacity legality — that check already happened, once, before the
slot was allowed to exist. The consumer's job is a single existence-and-status
read, not a rules evaluation.

For structure rather than a single slot:

| Consumer question | Endpoint |
|---|---|
| What zones does this site have? | `GET /sites/{siteCode}/zones` |
| What aisles, in walk order? | `GET /zones/{zoneId}/aisles` — ordered by `sequenceHint` |
| The whole building at once | `GET /sites/{siteCode}/layout` |
| Where are the dock doors / work centers / yard spots? | `GET /sites/{siteCode}/locations?role={LocationRole}` |
| What is this slot for? | `GET /locations/{locationCode}` — the `role` field (plus `dockFlow` / `activities`) |
| Walkable topology of a zone | `GET /zones/{zoneId}/travel-graph` |
| Declared cross-aisle connections of a zone | `GET /zones/{zoneId}/cross-aisles` |
| Distance between two slots | `GET /distance?from=&to=` → `{metresM, estimated, route}` — routed over the zone's travel graph when both slots share a zone; across zones a straight-line estimate (`estimated: true`) only when both slots have recorded position geometry, otherwise `422 no-route-between-zones` |

## Event subscription

Consumers that need a local read model subscribe to this context's
[domain events](../ddd/domain-events.md) on the `warehouse.facility.events`
Kafka topic, specified in
[`apis/asyncapi.yaml`](https://github.com/IQVO/facility-layout/blob/main/apis/asyncapi.yaml).
The service publishes there when it runs with `EVENT_PUBLISHER=kafka`. Each
message is a CloudEvents 1.0 event in structured content mode
([ADR-0024](../adr/0024-cloudevents-mandatory-envelope.md)) — `specversion`,
`id`, `source=/warehouse/facility-layout`, `type`, `subject` (aggregate id),
`time`, `datacontenttype`, `dataschema` and `data` (the domain event's own
JSON) — with the Kafka header
`content-type: application/cloudevents+json; charset=UTF-8`. The Kafka key is
the identity of the aggregate that raised it for the registration and
decommission events, so per-aggregate order is preserved; the four ADR 0017
geometry events and `FacilityLayoutImported` are keyed by their event type
instead (their `subject` still names the aggregate — see
[Domain events](../ddd/domain-events.md#catalogue-at-a-glance)). Dispatch on the **full** `type` string (e.g.
`com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered`),
dedupe on `id`, and DLQ/skip anything that fails CloudEvents validation.

The events a consumer is most likely to care about:

| Event | Consumer reaction |
|---|---|
| `LocationSlotRegistered` | Add the location to the local map as usable. Payload carries `zoneId`, `aisleId` and `role` already, so no code parsing is needed. |
| `LocationSlotDecommissioned` | Stop offering that location for new work. |
| `ZoneRegistered` | Learn a new zone, its temperature class and hazmat flag. |
| `AisleRegistered` | Learn a corridor, its `sequenceHint` walk-order position and its `direction`. This is the travel-path input. |
| `AisleGeometryUpdated`, `CrossAisleRegistered`, `LocationGeometryUpdated` | Keep a local copy of the travel geometry. |
| `FacilityLayoutImported` | Recognise a bulk load — useful for distinguishing a building bootstrap from ordinary drift. |

`inventory-storage` is the reference consumer: it replays the topic from the
first offset on every start under a per-instance consumer group, uses
`ZoneRegistered`, `LocationSlotRegistered` and `LocationSlotDecommissioned`,
ignores the rest, and gates its readiness on the replay finishing
([ADR 0013](../adr/0013-first-published-language-consumer.md)).
`warehouse-planning` is the second: under a fixed consumer group it folds
`LocationSlotRegistered` (Storage slots by location type, WorkCenter slots
by activity) and `LocationSlotDecommissioned` into a capacity tally, deduping
on the CloudEvents `id`.

## Rules for a consumer

These apply to either style, and follow from this context being an Open Host
Service its consumers are Conformist to.

**Never write.** No other context gets write access to a Site, Zone, Aisle,
LocationType, PlacementRule or LocationSlot. The map has exactly one author.

**Never re-derive the hierarchy by string surgery.** A `LocationCode` looks
easy to `split("-")`, and every consumer that does it owns a copy of this
context's format forever. Use the `zoneId` and `aisleId` the API and the
events already give you.

**Never re-implement PlacementRules.** Duplicating the rule engine in a
consumer recreates the exact problem the rule engine exists to prevent: two
implementations that disagree the first time a rule changes.

**Translate at your edge.** Conformist does not mean "use `LocationSlot` as
your internal type." `inventory-storage` should keep its own `Bin` aggregate
and populate it from this context's facts; the two models have different
lifecycles and different reasons to change.

**Expect one-way decommission.** A retired code never comes back. A consumer
never has to handle a `Decommissioned → Active` transition, and should not
build one. See [ADR 0005](../adr/0005-one-way-decommission.md).

## What this service will never do

So that no consumer waits for it:

- It will not track occupancy, stock, reservations or usable inventory.
- It will not know about Tasks, Assignments, Waves, Shifts or Associates.
- It will not plan work or optimise a pick path. It supplies map topology —
  `sequenceHint`, `direction`, the travel graph and a shortest distance
  between two slots (`GET /distance`) — and the WES tier does the
  optimisation, because that optimisation *is* the WES tier's core domain.
  `/distance` never reports travel time or congestion.
- It will not call another warehouse-systems service. Ever.
