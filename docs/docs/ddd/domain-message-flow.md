---
id: domain-message-flow
title: Domain Message Flow
sidebar_label: Domain Message Flow
sidebar_position: 5
description: ddd-crew Domain Message Flow diagrams for four Facility Layout business scenarios — commissioning a zone, the stow-time location check, decommissioning a slot, and travel distance for planning.
---

# Domain Message Flow

Following [ddd-crew Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling).
Each scenario shows **contexts and actors as participants** and only real
messages. Every arrow is numbered and prefixed:

- `cmd:` — a command (REST write)
- `qry:` — a query (REST `GET` or MCP tool)
- `evt:` — a domain event (Kafka, CloudEvents 1.0; type prefix
  `com.warehouse.wms.facility-layout.` omitted inside the diagrams, written
  in full in the tables)

Downstream behaviour is taken from each consumer's own `develop` branch (see
the [Context map](../ecosystem/context-map.md) for file paths).

## 1. Commissioning a zone of the building

An operator loads a new storage zone through the console micro-frontend,
which calls this service's own REST API.

```mermaid
sequenceDiagram
    autonumber
    actor Op as Operator
    participant MFE as facility-mfe in warehouse-console
    participant FL as facility-layout
    participant K as Kafka warehouse.facility.events
    participant IS as inventory-storage
    participant WPL as warehouse-planning

    Op->>MFE: register site, zone, aisles, types, rules
    MFE->>FL: cmd: POST /sites RegisterSite
    FL-->>K: evt: site.SiteRegistered
    MFE->>FL: cmd: POST /sites/WH1/zones RegisterZone
    FL-->>K: evt: zone.ZoneRegistered
    K-->>IS: evt: zone.ZoneRegistered - cache zone temperature and hazmat
    MFE->>FL: cmd: POST /zones/WH1-STOR-AMB/aisles RegisterAisle
    FL-->>K: evt: aisle.AisleRegistered
    MFE->>FL: cmd: POST /location-types RegisterLocationType
    FL-->>K: evt: locationtype.LocationTypeRegistered
    MFE->>FL: cmd: POST /placement-rules DefinePlacementRule
    FL-->>K: evt: placementrule.PlacementRuleDefined
    Op->>MFE: upload slot rows
    MFE->>FL: cmd: POST /locations/import ImportFacilityLayout
    FL-->>K: evt: locationslot.LocationSlotRegistered per accepted row
    K-->>IS: evt: locationslot.LocationSlotRegistered - add active slot to cache
    K-->>WPL: evt: locationslot.LocationSlotRegistered - add to capacity tally
    FL-->>K: evt: locationslot.FacilityLayoutImported once per call
```

Source: `internal/adapters/inbound/http/server.go`,
`internal/application/usecases/register_*.go`, `define_placement_rule.go`,
`import_facility_layout.go`; consumers `inventory-storage`
`internal/adapters/outbound/facilitycache/consumer.go`, `warehouse-planning`
`internal/adapters/inbound/kafka/storage_capacity_consumer.go`.
Omitted: the analytics topic copy of every event, the Idempotency-Key header
on each creation `POST`, and per-row rejections (reported in the import
response, no event).

## 2. Stow-time location check (inventory-storage)

`inventory-storage` must not stow into a slot that does not exist, is retired,
or sits in the wrong kind of zone. It has two modes, selected by its own
`LOCATION_LOOKUP_MODE`.

```mermaid
sequenceDiagram
    autonumber
    actor Assoc as Stow associate
    participant IS as inventory-storage
    participant K as Kafka warehouse.facility.events
    participant FL as facility-layout

    Note over IS,K: LOCATION_LOOKUP_MODE=kafka - local cache
    K-->>IS: evt: zone.ZoneRegistered replayed from first offset
    K-->>IS: evt: locationslot.LocationSlotRegistered replayed
    K-->>IS: evt: locationslot.LocationSlotDecommissioned replayed
    Assoc->>IS: cmd: stow unit into bin WH1-STOR-AMB-A07-03-02-B
    IS->>IS: check cached classification - no call to facility-layout

    Note over IS,FL: LOCATION_LOOKUP_MODE=http - rollback path
    Assoc->>IS: cmd: stow unit into bin WH1-STOR-AMB-A07-03-02-B
    IS->>FL: qry: GET /locations/WH1-STOR-AMB-A07-03-02-B/classification
    FL-->>IS: zone hazmat and temperatureClass
```

Source: `inventory-storage` `cmd/inventory/main.go` (`buildLocationLookup`),
`internal/adapters/outbound/facilitycache/consumer.go`,
`internal/adapters/outbound/facilitylayout/client.go`; this repo
`GetLocationClassification`, [ADR 0008](../adr/0008-location-classification-read-endpoint.md),
[ADR 0013](../adr/0013-first-published-language-consumer.md).
Omitted: `permissive` mode (the default — no check at all) and the
inventory-storage stow command's own events.

## 3. Retiring a slot

A slot is permanently decommissioned; downstream caches drop it.

```mermaid
sequenceDiagram
    autonumber
    actor Op as Operator
    participant FL as facility-layout
    participant K as Kafka warehouse.facility.events
    participant IS as inventory-storage
    participant WPL as warehouse-planning
    participant FE as fulfillment-execution

    Op->>FL: cmd: POST /locations/WH1-STOR-AMB-A07-03-02-B/decommission
    FL-->>K: evt: locationslot.LocationSlotDecommissioned
    K-->>IS: evt: locationslot.LocationSlotDecommissioned - delete slot from cache
    K-->>WPL: evt: locationslot.LocationSlotDecommissioned - decrement tally
    Op->>FL: cmd: POST /locations with the same code
    FL-->>Op: 409 duplicate-location-code - one-way decommission
    FE->>FL: qry: GET /locations/WH1-STOR-AMB-A07-03-02-B
    FL-->>FE: slot with status Decommissioned and its role
```

Source: `internal/application/usecases/decommission_location_slot.go`,
`register_location_slot.go` (`ErrDuplicateLocationCode`),
[ADR 0005](../adr/0005-one-way-decommission.md); `fulfillment-execution`
`internal/adapters/outbound/facilitylayout/client.go` (opt-in
`LOCATION_ROLE_MODE=http`). Omitted: the analytics copy and the
`409 concurrent-modification` branch.

## 4. Travel distance for planning and for the ops agent

`wes-work-planning` prices travel between two locations; the ops agent asks
the same question over MCP.

```mermaid
sequenceDiagram
    autonumber
    participant WP as wes-work-planning
    participant OA as warehouse-ops-agent
    participant FL as facility-layout
    participant MCP as facility-layout cmd/mcp

    WP->>FL: qry: GET /distance?from=CODE1&to=CODE2 EstimateTravelDistance
    alt both codes in the same zone
        FL-->>WP: metresM, estimated, route over the zone travel graph
    else different zones and both slots have position geometry
        FL-->>WP: straight-line metresM with estimated true
    else different zones without geometry
        FL-->>WP: 422 no-route-between-zones
    end
    OA->>MCP: qry: estimate_travel_distance from CODE1 to CODE2
    MCP-->>OA: same TravelDistance result
    OA->>MCP: qry: list_sites, get_site_layout, get_zone_grid
    MCP-->>OA: sites, nested layout, zone grid
```

Source: `internal/application/usecases/estimate_travel_distance.go`,
`internal/domain/travel/graph.go`, `internal/adapters/inbound/mcp/tools.go`;
`wes-work-planning` `internal/adapters/outbound/traveldistance/client.go`;
`warehouse-ops-agent` `internal/adapters/outbound/mcpclient/facility_layout.go`.
Omitted: `wes-work-planning`'s `permissive` default and circuit breaker; no
event is produced (read model).

## Message catalogue used above

| # | Message | Kind | Channel |
|---|---|---|---|
| 1 | RegisterSite | cmd | `POST /sites` |
| 2 | RegisterZone | cmd | `POST /sites/{siteCode}/zones` |
| 3 | RegisterAisle | cmd | `POST /zones/{zoneId}/aisles` |
| 4 | RegisterLocationType | cmd | `POST /location-types` |
| 5 | DefinePlacementRule | cmd | `POST /placement-rules` |
| 6 | ImportFacilityLayout | cmd | `POST /locations/import` |
| 7 | RegisterLocationSlot | cmd | `POST /locations` |
| 8 | DecommissionLocationSlot | cmd | `POST /locations/{locationCode}/decommission` |
| 9 | GetLocationClassification | qry | `GET /locations/{locationCode}/classification` |
| 10 | GetLocationSlot | qry | `GET /locations/{locationCode}` |
| 11 | EstimateTravelDistance | qry | `GET /distance`, MCP `estimate_travel_distance` |
| 12 | list_sites / get_site_layout / get_zone_grid | qry | MCP |
| 13 | SiteRegistered | evt | `com.warehouse.wms.facility-layout.site.SiteRegistered` |
| 14 | ZoneRegistered | evt | `com.warehouse.wms.facility-layout.zone.ZoneRegistered` |
| 15 | AisleRegistered | evt | `com.warehouse.wms.facility-layout.aisle.AisleRegistered` |
| 16 | LocationTypeRegistered | evt | `com.warehouse.wms.facility-layout.locationtype.LocationTypeRegistered` |
| 17 | PlacementRuleDefined | evt | `com.warehouse.wms.facility-layout.placementrule.PlacementRuleDefined` |
| 18 | LocationSlotRegistered | evt | `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` |
| 19 | LocationSlotDecommissioned | evt | `com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned` |
| 20 | FacilityLayoutImported | evt | `com.warehouse.wms.facility-layout.locationslot.FacilityLayoutImported` |
