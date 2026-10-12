---
id: use-cases
title: Application use cases
sidebar_label: Use cases
---

# Application use cases

Every use case lives in `internal/application/usecases` and is wired once in
the composition roots (`cmd/facility/main.go` `newServer`, and
`cmd/mcp/main.go` for the read subset). There are no Kafka-triggered or
scheduled use cases: facility-layout consumes no topic in its OLTP path. The
only background work (outbox relay, housekeeping sweeper, analytics
projection) lives in adapters, not in use cases.

Writes run inside `atomically(ctx, UnitOfWork, …)`. With Postgres plus
Kafka, each write saves the aggregate and enqueues its event(s) into
`outbox_events` in one transaction (ADR-0018). Without a unit of work
(in-memory mode) the two steps run back to back. Every event below is
published to **both** `warehouse.facility.events` and
`warehouse.facility.analytics` with the CloudEvents type
`com.warehouse.wms.facility-layout.<entity>.<EventName>` (ADR-0024).

## Commands (write use cases)

| Use case | Trigger | Inputs | Invariants checked (→ problem slug) | Event raised |
|---|---|---|---|---|
| `RegisterSite` | `POST /sites` (Idempotency-Key) | `siteCode`, `name` | valid site code (`invalid-site-code`); unique code (`duplicate-site-code`) | `SiteRegistered` (`…site.SiteRegistered`) |
| `RegisterZone` | `POST /sites/{siteCode}/zones` (Idempotency-Key) | site code, area code, zone code, temperature class, hazmat, optional `bayPitchM`/`levelPitchM` | site exists (`site-not-found`) and is active (`site-not-active`); valid codes; known temperature class; pitches both-or-neither and > 0 (`invalid-pitch`); unique zone (`duplicate-zone`) | `ZoneRegistered` |
| `RegisterAisle` | `POST /zones/{zoneId}/aisles` (Idempotency-Key) | zone id, aisle code, sequence hint, direction | zone exists and is active; sequence hint ≥ 0; known direction; unique aisle in the zone (`duplicate-aisle`) | `AisleRegistered` |
| `RegisterLocationType` | `POST /location-types` (Idempotency-Key) | name, role (default `Storage`), default capacity | known role (`unknown-location-role`); capacity required (> 0) for roles that require it, zero for Dock/Yard/WorkCenter/QC/Shipping; unique name (`duplicate-location-type`) | `LocationTypeRegistered` |
| `DefinePlacementRule` | `POST /placement-rules` (Idempotency-Key) | rule id, location type, effect, zone predicate | location type exists (`location-type-not-found`); known effect (`unknown-placement-effect`); predicate constrains something (`empty-zone-predicate`); unique id (`duplicate-placement-rule`) | `PlacementRuleDefined` |
| `RegisterLocationSlot` | `POST /locations` (Idempotency-Key); also called once per row by `ImportFacilityLayout` | location code, location type, optional capacity override, `dockFlow`, `activities` | well-formed code (`malformed-location-code`); site, zone and aisle exist and are active; location type exists; code unique (`duplicate-location-code`); **placement rules evaluated at registration** (`placement-rule-violated`, ADR-0003); Dock needs `dockFlow`, WorkCenter needs ≥ 1 activity, others none (ADR-0016) | `LocationSlotRegistered`. Records `facility.location_slot.registrations{outcome}`. |
| `DecommissionLocationSlot` | `POST /locations/{locationCode}/decommission` (no Idempotency-Key) | location code | slot exists (`location-slot-not-found`); not already decommissioned (`already-decommissioned`, one-way per ADR-0005); version check (`concurrent-modification`, ADR-0025) | `LocationSlotDecommissioned` |
| `ImportFacilityLayout` | `POST /locations/import` (no Idempotency-Key) | rows of site/area/zone/aisle/bay/level/position plus location type, optional functional attributes, capacity and geometry | non-empty (`empty-import`). Each row is atomic and creates missing site, zone and aisle parents (it never mutates existing ones; inactive parents reject the row). Each row then runs `RegisterLocationSlot` and, if X/Y/Z are given, `SetLocationGeometry`. **Partial success**: one bad row does not abort the rest (ADR-0006). | Per row: `SiteRegistered`/`ZoneRegistered`/`AisleRegistered` for new parents, `LocationSlotRegistered`, optionally `LocationGeometryUpdated`. Then exactly one `FacilityLayoutImported` with `rowsSubmitted`, `slotsImported` and `rowsRejected`. |
| `SetLocationGeometry` | `PUT /locations/{locationCode}/geometry` | position (x, y, z), dimensions (w, d, h), optional pick sequence | slot exists; z ≥ 0 (`invalid-z`); dimensions > 0 (`invalid-dimensions`); pick sequence ≥ 0; slot version check (`concurrent-modification`) | `LocationGeometryUpdated` |
| `SetAisleGeometry` | `PUT /zones/{zoneId}/aisles/{aisleCode}/geometry` | centreline segment | aisle exists; both endpoints real (`segment-endpoints-not-real`); start ≠ end (`segment-start-end-equal`) | `AisleGeometryUpdated` |
| `RegisterFixedStructure` | `POST /sites/{siteCode}/structures` (Idempotency-Key) | id, site code, kind, footprint, label | site exists; known kind (`unknown-fixed-structure-kind`); real footprint (`empty-fixed-structure-footprint`); unique id | `FixedStructureRegistered` |
| `RegisterCrossAisle` | `POST /zones/{zoneId}/cross-aisles` (Idempotency-Key) | zone id, from aisle, to aisle, at bay | zone exists; both aisles belong to the zone (`cross-aisle-aisle-mismatch`); distinct aisles (`cross-aisle-same-aisle`); unique (`duplicate-cross-aisle`) | `CrossAisleRegistered` |

### `SiteCapabilityChanged`: defined, but no use case raises it

`com.warehouse.wms.facility-layout.site.SiteCapabilityChanged` is defined in
`internal/domain/shared/events.go`. It is a full last-write-wins snapshot
with `siteCode`, `transferOriginEnabled`, `transferDestinationEnabled` and
`capabilityRevision`, and consumers keep the highest revision. It has a wire
DTO (`internal/adapters/kafka/cloudevents/wire.go`), a partition key (the
site code), an AsyncAPI message and a contract fitness test. **No use case,
REST route or MCP tool produces it on `develop`**, so nothing publishes it
today. Consumers can code against the contract, but they will not receive
messages until a capability-changing command exists.

## Queries (read use cases)

| Use case | REST trigger | MCP tool | Inputs | Not-found → |
|---|---|---|---|---|
| `ListSites` | `GET /sites` | `list_sites` | none | n/a |
| `GetSite` | `GET /sites/{siteCode}` | n/a | site code | `site-not-found` |
| `GetSiteLayout` | `GET /sites/{siteCode}/layout` (`format=json` or `svg`, otherwise `invalid-layout-format`) | `get_site_layout` | site code | `site-not-found` |
| `ListLocationsByRole` | `GET /sites/{siteCode}/locations?role=` | `list_functional_locations` | site code, role | `site-not-found` |
| `ListZones` | `GET /sites/{siteCode}/zones` | n/a | site code | `site-not-found` |
| `ListFixedStructures` | `GET /sites/{siteCode}/structures` | n/a | site code | `site-not-found` |
| `GetZone` | `GET /zones/{zoneId}` | n/a | zone id | `zone-not-found` |
| `GetZoneGrid` | `GET /zones/{zoneId}/grid` | `get_zone_grid` | zone id | `zone-not-found` |
| `ListAisles` / `GetAisle` | `GET /zones/{zoneId}/aisles`, `GET /zones/{zoneId}/aisles/{aisleCode}` | n/a | zone id (+ aisle code) | `zone-not-found` / `aisle-not-found` |
| `ListCrossAisles` | `GET /zones/{zoneId}/cross-aisles` | n/a | zone id | `zone-not-found` |
| `GetZoneTravelGraph` | `GET /zones/{zoneId}/travel-graph` | `get_zone_travel_graph` | zone id | `zone-not-found` |
| `EstimateTravelDistance` | `GET /distance?from=&to=` | `estimate_travel_distance` | two location codes | `location-slot-not-found`. Two locations in different zones return 422 `no-route-between-zones`, and an unreachable waypoint returns 422 `no-route`. |
| `ListLocationTypes` / `GetLocationType` | `GET /location-types`, `GET /location-types/{name}` | n/a | name | `location-type-not-found` |
| `ListPlacementRules` / `GetPlacementRule` | `GET /placement-rules`, `GET /placement-rules/{ruleId}` | n/a | rule id | `placement-rule-not-found` |
| `GetLocationSlot` | `GET /locations/{locationCode}` | n/a | location code | `location-slot-not-found` |
| `GetLocationClassification` | `GET /locations/{locationCode}/classification` (ADR-0008) | n/a | location code | `location-slot-not-found` |

The MCP binary also registers `get_facility_catalog_growth_report` when
`REPORTS_BASE_URL` is set. That tool is not an application use case: it
calls the `facility-reports` REST API (`GET /reports/catalog-growth`). The
MCP server wires **only** read use cases, so it cannot mutate the map.

## Analytics projection (adapter, not a use case)

`facility-projector` applies eight of the events above
(`SiteRegistered`, `ZoneRegistered`, `AisleRegistered`,
`LocationTypeRegistered`, `PlacementRuleDefined`, `LocationSlotRegistered`,
`LocationSlotDecommissioned`, `FacilityLayoutImported`) to
`catalog_growth_rollup`. It works directly from the inbound Kafka adapter
through the `report.ProjectionStore` port
(`internal/adapters/inbound/kafka/analytics_consumer.go`), idempotently per
CloudEvents id. See the [Runbook](../operations/runbook.md#analytics-consumer-and-dlq-adr-0020-adr-0030).
