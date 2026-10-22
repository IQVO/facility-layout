---
id: bounded-context-canvas
title: Bounded Context Canvas
sidebar_label: Bounded Context Canvas
sidebar_position: 3
description: The ddd-crew Bounded Context Canvas v5 for Facility Layout — purpose, classification, roles, every inbound and outbound message mapped to a real route, MCP tool or Kafka topic.
---

# Bounded Context Canvas

Following the [ddd-crew Bounded Context Canvas v5](https://github.com/ddd-crew/bounded-context-canvas).
Every message row below maps to a real REST route
(`internal/adapters/inbound/http/server.go`), MCP tool
(`internal/adapters/inbound/mcp/tools.go`, `report_tool.go`) or Kafka topic +
CloudEvents type (`internal/adapters/outbound/kafka/publisher.go`,
`internal/domain/shared/events.go`). REST and MCP are unauthenticated
([ADR 0015](../adr/0015-remove-rest-mcp-auth.md)).

## Canvas at a glance

Two pictures of the sections below, laid out like the ddd-crew canvas:
first the inbound collaborators feeding this context (purpose, domain roles
and the business decisions it enforces), then the outbound collaborators it
calls or notifies. Arrows follow the message type: **solid blue**
commands (`cmd`), **dotted green** queries (`qry`), **thick orange** events
(`evt`); each arrow names its channel and the messages it carries. Box shapes
mark the collaborator kind: rounded = people, UI or any client; blue =
sibling bounded context; double border = external system or platform;
grey = this context's own projector, reports or dead-letter path; dashed =
published today with no consumer. Channel names on the arrows are
abbreviated (REST, MCP, Kafka, HTTP, port); the Inbound and Outbound
Communication tables carry the exact route, tool, topic and CloudEvents
type for every message.

### Inbound

```mermaid
%%{init: {"flowchart": {"nodeSpacing": 14, "rankSpacing": 40, "padding": 6}}}%%
flowchart LR
    classDef bc fill:#fff3bf,stroke:#e67700,stroke-width:1.5px,color:#000
    classDef card fill:#fff9db,stroke:#f08c00,color:#000,text-align:left
    classDef ctx fill:#e7f5ff,stroke:#1c7ed6,color:#000
    classDef actor fill:#fff4e6,stroke:#e8590c,color:#000
    classDef ext fill:#ffe3e3,stroke:#c92a2a,color:#000
    classDef self fill:#f1f3f5,stroke:#868e96,color:#000
    classDef none fill:#f8f9fa,stroke:#adb5bd,stroke-dasharray:5 5,color:#495057
    subgraph IN["Inbound - who commands, queries or feeds it"]
        direction TB
        in0(["Operator via facility-mfe<br/>(console) or any REST<br/>client"]):::actor
        in1["inventory-storage"]:::ctx
        in2["fulfillment-execution"]:::ctx
        in3["wes-work-planning"]:::ctx
        in4["warehouse-ops-agent"]:::ctx
        in5(["Any MCP client"]):::actor
    end
    subgraph BC["facility-layout - Generic subdomain - WMS tier"]
        direction TB
        purpose["<b>Purpose</b><br/>System of record for where things physically are: the site<br/>hierarchy, every coded location slot, its role, legality<br/>and geometry. Not occupancy, not stock, not travel time."]:::card
        roles["<b>Domain roles</b><br/>- Specification model<br/>- Gateway / Open Host (one Published Language)<br/>- Draft context for its own analytics"]:::card
        rules["<b>Business decisions it enforces</b><br/>- No orphan slots: Site, Zone and Aisle must exist and be<br/>Active<br/>- Placement legality decided once, at registration; Deny<br/>wins<br/>- Decommission is one-way<br/>- Bulk import is partial-success, reported per row<br/>- Distance is topology, never time<br/>- CloudEvents 1.0 through the transactional outbox"]:::card
        purpose ~~~ roles ~~~ rules
    end
    in0 -->|"cmd - REST<br/>13 commands: RegisterSite,<br/>RegisterZone, RegisterAisle,<br/>RegisterLocationType,<br/>DefinePlacementRule,<br/>RegisterLocationSlot,<br/>ImportFacilityLayout,<br/>DecommissionLocationSlot,<br/>SetLocationGeometry,<br/>SetAisleGeometry,<br/>RegisterCrossAisle,<br/>RegisterFixedStructure,<br/>ChangeSiteTransferCapability"| BC
    in0 -.->|"qry - REST<br/>site layout, zone grid, travel<br/>graph, locations by role,<br/>single and list reads"| BC
    in1 -.->|"qry - REST (http mode)<br/>GetLocationClassification"| BC
    in2 -.->|"qry - REST (http mode)<br/>GetLocationSlot (role)"| BC
    in3 -.->|"qry - REST (http mode)<br/>EstimateTravelDistance"| BC
    in4 -.->|"qry - MCP<br/>list_sites, get_site_layout,<br/>get_zone_grid,<br/>estimate_travel_distance"| BC
    in4 -.->|"qry - REST (reports)<br/>catalog growth report,<br/>freshness"| BC
    in5 -.->|"qry - MCP<br/>list_functional_locations,<br/>get_zone_travel_graph,<br/>get_facility_catalog_growth_report"| BC
    linkStyle 0 stroke:#1971c2,stroke-width:1.5px
    linkStyle 1 stroke:#2f9e44,stroke-width:1.5px
    linkStyle 2 stroke:#2f9e44,stroke-width:1.5px
    linkStyle 3 stroke:#2f9e44,stroke-width:1.5px
    linkStyle 4 stroke:#2f9e44,stroke-width:1.5px
    linkStyle 5 stroke:#2f9e44,stroke-width:1.5px
    linkStyle 6 stroke:#2f9e44,stroke-width:1.5px
    linkStyle 7 stroke:#2f9e44,stroke-width:1.5px
    class BC bc
```

### Outbound

```mermaid
%%{init: {"flowchart": {"nodeSpacing": 14, "rankSpacing": 40, "padding": 6}}}%%
flowchart LR
    classDef bc fill:#fff3bf,stroke:#e67700,stroke-width:1.5px,color:#000
    classDef card fill:#fff9db,stroke:#f08c00,color:#000,text-align:left
    classDef ctx fill:#e7f5ff,stroke:#1c7ed6,color:#000
    classDef actor fill:#fff4e6,stroke:#e8590c,color:#000
    classDef ext fill:#ffe3e3,stroke:#c92a2a,color:#000
    classDef self fill:#f1f3f5,stroke:#868e96,color:#000
    classDef none fill:#f8f9fa,stroke:#adb5bd,stroke-dasharray:5 5,color:#495057
    BC["<b>facility-layout - Generic subdomain - WMS tier</b>"]:::bc
    subgraph OUT["Outbound - whom it calls or notifies"]
        direction TB
        out0["inventory-storage"]:::ctx
        out1["warehouse-planning"]:::ctx
        out2["inbound-receiving (dock<br/>doors copy)"]:::ctx
        out3["slotting-optimization,<br/>pick-face-replenishment<br/>(layout copies)"]:::ctx
        out4["network-inventory-planning"]:::ctx
        out5["own cmd/facility-projector"]:::self
        out6["no consumer yet"]:::none
        out7["task-interleaving<br/>(layout copy + geometry)"]:::ctx
    end
    BC ==>|"evt - Kafka<br/>warehouse.facility.events<br/>ZoneRegistered,<br/>LocationSlotRegistered,<br/>LocationSlotDecommissioned"| out0
    BC ==>|"evt - Kafka<br/>warehouse.facility.events<br/>LocationSlotRegistered,<br/>LocationSlotDecommissioned"| out1
    BC ==>|"evt - Kafka<br/>warehouse.facility.events<br/>LocationSlotRegistered,<br/>LocationSlotDecommissioned"| out2
    BC ==>|"evt - Kafka<br/>warehouse.facility.events<br/>ZoneRegistered,<br/>LocationSlotRegistered,<br/>LocationSlotDecommissioned"| out3
    BC ==>|"evt - Kafka<br/>warehouse.facility.events<br/>SiteCapabilityChanged"| out4
    BC ==>|"evt - Kafka<br/>warehouse.facility.analytics<br/>analytics copy of the domain<br/>events"| out5
    BC ==>|"evt - Kafka<br/>warehouse.facility.events<br/>SiteRegistered,<br/>AisleRegistered,<br/>LocationTypeRegistered,<br/>PlacementRuleDefined,<br/>FacilityLayoutImported,<br/>AisleGeometryUpdated,<br/>FixedStructureRegistered,<br/>CrossAisleRegistered"| out6
    BC ==>|"evt - Kafka<br/>warehouse.facility.events<br/>ZoneRegistered,<br/>LocationSlotRegistered,<br/>LocationSlotDecommissioned,<br/>LocationGeometryUpdated"| out7
    linkStyle 0 stroke:#e8590c,stroke-width:2.5px
    linkStyle 1 stroke:#e8590c,stroke-width:2.5px
    linkStyle 2 stroke:#e8590c,stroke-width:2.5px
    linkStyle 3 stroke:#e8590c,stroke-width:2.5px
    linkStyle 4 stroke:#e8590c,stroke-width:2.5px
    linkStyle 5 stroke:#e8590c,stroke-width:2.5px
    linkStyle 6 stroke:#e8590c,stroke-width:2.5px
    linkStyle 7 stroke:#e8590c,stroke-width:2.5px
```

Source: the Inbound Communication and Outbound Communication tables of this
page, which map to `internal/adapters/inbound/{http,kafka,mcp}` and `internal/adapters/outbound/`. The diagram omits assumptions, verification metrics, open
questions and the ubiquitous language (sections below), the relationship
pattern per message (OHS, ACL, Conformist, Customer/Supplier - see the
tables) and anything the tables mark as deprecated or platform-only
(health probes).

## Name

**Facility Layout** (`facility-layout`, CloudEvents subdomain segment `wms`).

## Purpose

The system of record for **where things physically are in the building**:
the site hierarchy (Site → Area → Zone → Aisle) and the coded slots inside it
(`WH1-STOR-AMB-A07-03-02-B`). It owns whether a coded location **exists, is
active, and is legal for a given kind of storage unit**, what a location is
**for** (its `LocationRole`), and the map's **geometry and travel topology**.
It does **not** own occupancy or stock — that is `inventory-storage`'s — and
it does not own travel *time* or congestion — that is `wes-work-planning`'s.

## Strategic Classification

| Dimension | Value |
|---|---|
| **Domain** | **Generic Subdomain** — see [Subdomain classification](./subdomain-classification.md) and the [Core Domain Chart](./core-domain-chart.md) |
| **Business Model** | **Compliance / enabler** — it earns nothing directly; it prevents errors (orphan slots, ambient product in a frozen zone) for the revenue-generating Core contexts that consume it |
| **Evolution** | **Product** heading toward commodity — the model mirrors commercial WMS location masters (Oracle WMS Cloud, SAP EWM) |

## Domain Roles

| Role (ddd-crew archetype) | Why |
|---|---|
| **Specification model** | Its job is to answer "is this location real, active and legal?" — a rule/specification oracle for other contexts. |
| **Gateway / Open Host** | It publishes one general-purpose Published Language (12 CloudEvents types + REST + MCP) for every consumer instead of bilateral contracts. |
| **Draft context for analytics** | Its own events feed its own analytics data product (`cmd/facility-projector`, [ADR 0010](../adr/0010-analytical-data-product.md)). |

## Inbound Communication

Who sends messages **into** this context. Collaborators are the callers
verified in their own repositories' `develop` (see the
[Context map](../ecosystem/context-map.md)).

| Collaborator | Message | Type | Channel | Relationship |
|---|---|---|---|---|
| Operator via `facility-mfe` (console) | RegisterSite | Command | REST `POST /sites` | Customer of own OHS |
| Operator via `facility-mfe` | RegisterZone | Command | REST `POST /sites/{siteCode}/zones` | Customer of own OHS |
| Operator via `facility-mfe` | RegisterAisle | Command | REST `POST /zones/{zoneId}/aisles` | Customer of own OHS |
| Operator via `facility-mfe` | RegisterLocationType | Command | REST `POST /location-types` | Customer of own OHS |
| Operator via `facility-mfe` | DefinePlacementRule | Command | REST `POST /placement-rules` | Customer of own OHS |
| Operator via `facility-mfe` | RegisterLocationSlot | Command | REST `POST /locations` | Customer of own OHS |
| Operator via `facility-mfe` | ImportFacilityLayout | Command | REST `POST /locations/import` | Customer of own OHS |
| Operator / REST client | DecommissionLocationSlot | Command | REST `POST /locations/{locationCode}/decommission` | Customer of own OHS |
| Operator / REST client | SetLocationGeometry | Command | REST `PUT /locations/{locationCode}/geometry` | Customer of own OHS |
| Operator / REST client | SetAisleGeometry | Command | REST `PUT /zones/{zoneId}/aisles/{aisleCode}/geometry` | Customer of own OHS |
| Operator / REST client | RegisterCrossAisle | Command | REST `POST /zones/{zoneId}/cross-aisles` | Customer of own OHS |
| Operator / REST client | RegisterFixedStructure | Command | REST `POST /sites/{siteCode}/structures` | Customer of own OHS |
| Operator / REST client | ChangeSiteTransferCapability | Command | REST `PUT /sites/{siteCode}/transfer-capability` ([ADR 0035](../adr/0035-site-transfer-capability-command.md)) | Customer of own OHS |
| `inventory-storage` | GetLocationClassification | Query | REST `GET /locations/{locationCode}/classification` (`LOCATION_LOOKUP_MODE=http`) | Conformist (D) of OHS (U) |
| `fulfillment-execution` | GetLocationSlot (reads `role`) | Query | REST `GET /locations/{locationCode}` (`LOCATION_ROLE_MODE=http`) | Conformist (D) of OHS (U) |
| `wes-work-planning` | EstimateTravelDistance | Query | REST `GET /distance?from=&to=` (`TRAVEL_DISTANCE_MODE=http`) | Conformist (D) of OHS (U) |
| `warehouse-ops-agent` | list sites / site layout / zone grid / travel distance | Query | MCP `list_sites`, `get_site_layout`, `get_zone_grid`, `estimate_travel_distance` | Conformist (D) of OHS (U) |
| `warehouse-ops-agent` | Catalog growth report | Query | REST `GET /reports/catalog-growth`, `GET /reports/catalog-growth/freshness` on `cmd/facility-reports` | Conformist (D) of OHS (U) |
| `facility-mfe` / any client | Read models: site layout, zone grid, travel graph, locations by role, single-resource and list reads | Query | REST `GET /sites/{siteCode}/layout`, `GET /zones/{zoneId}/grid`, `GET /zones/{zoneId}/travel-graph`, `GET /sites/{siteCode}/locations?role=`, and the other `GET` routes | Customer of own OHS |
| Any MCP client | Functional locations / travel graph / catalog report | Query | MCP `list_functional_locations`, `get_zone_travel_graph`, `get_facility_catalog_growth_report` (the last only when `REPORTS_BASE_URL` is set) | OHS |

This context consumes **no** event from any other context — the only Kafka
consumer in the repository (`internal/adapters/inbound/kafka/analytics_consumer.go`)
reads this context's own `warehouse.facility.analytics` topic.

## Outbound Communication

Every domain event goes to **both** topics
(integration `warehouse.facility.events`, analytics
`warehouse.facility.analytics`). CloudEvents type prefix:
`com.warehouse.wms.facility-layout.`

| Collaborator | Message | Type | Channel | Relationship |
|---|---|---|---|---|
| `inventory-storage` (`facilitycache`), `slotting-optimization`, `pick-face-replenishment`, `task-interleaving` (layout copies) | ZoneRegistered | Event | Kafka `warehouse.facility.events` — `com.warehouse.wms.facility-layout.zone.ZoneRegistered` | OHS + PL (U) → Conformist + ACL (D) |
| `inventory-storage`, `warehouse-planning`, `inbound-receiving` (dock-door copy), `slotting-optimization`, `pick-face-replenishment`, `task-interleaving` (layout copies) | LocationSlotRegistered | Event | Kafka `warehouse.facility.events` — `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` | OHS + PL (U) → Conformist + ACL (D) |
| `inventory-storage`, `warehouse-planning`, `inbound-receiving` (dock-door copy), `slotting-optimization`, `pick-face-replenishment`, `task-interleaving` (layout copies) | LocationSlotDecommissioned | Event | Kafka `warehouse.facility.events` — `com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned` | OHS + PL (U) → Conformist + ACL (D) |
| `task-interleaving` (layout copy) | LocationGeometryUpdated | Event | Kafka `warehouse.facility.events` — `com.warehouse.wms.facility-layout.locationslot.LocationGeometryUpdated` | OHS + PL (U) → Conformist + ACL (D) |
| `network-inventory-planning` (`SiteCapabilityConsumer`) | SiteCapabilityChanged | Event | Kafka `warehouse.facility.events` — `com.warehouse.wms.facility-layout.site.SiteCapabilityChanged` (snake_case payload, ADR 0035) | OHS + PL (U) → Conformist + ACL (D) |
| own `cmd/facility-projector` | SiteRegistered, ZoneRegistered, AisleRegistered, LocationTypeRegistered, PlacementRuleDefined, LocationSlotRegistered, LocationSlotDecommissioned, FacilityLayoutImported | Event | Kafka `warehouse.facility.analytics` — same `type` strings, `dataschema` `urn:warehouse:facility-layout:analytics:<EventName>:v1` | same context |
| no consumer yet | SiteRegistered, AisleRegistered, LocationTypeRegistered, PlacementRuleDefined, FacilityLayoutImported | Event | Kafka `warehouse.facility.events` — `...site.SiteRegistered`, `...aisle.AisleRegistered`, `...locationtype.LocationTypeRegistered`, `...placementrule.PlacementRuleDefined`, `...locationslot.FacilityLayoutImported` | OHS + PL |
| no consumer yet | AisleGeometryUpdated, FixedStructureRegistered, CrossAisleRegistered | Event | Kafka `warehouse.facility.events` — `...aisle.AisleGeometryUpdated`, `...structure.FixedStructureRegistered`, `...crossaisle.CrossAisleRegistered` | OHS + PL |

This context sends **no** command or query to any other context: there is
no outbound REST/MCP client in `internal/adapters/outbound/`.

## Ubiquitous Language

Full glossary: [Ubiquitous language](../business-context/ubiquitous-language.md).
Top terms:

| Term | Code identifier |
|---|---|
| Site | `site.Site` |
| Zone (Area + Zone segments) | `zone.Zone` |
| Aisle, SequenceHint, Direction | `aisle.Aisle`, `shared.Direction` |
| LocationSlot | `slot.LocationSlot` |
| LocationCode | `shared.LocationCode` |
| LocationType, LocationRole | `placement.LocationType`, `placement.LocationRole` |
| PlacementRule, ZonePredicate, Allow/Deny | `placement.PlacementRule`, `placement.ZonePredicate`, `placement.Effect` |
| Chain of custody | `RegisterLocationSlot.resolveChain` |
| Decommission (one-way) | `Decommission()` on each structural aggregate |
| Travel graph, waypoint | `travel.Graph`, `travel.Node` |

## Business Decisions

- **No orphan slots, ever**: a slot is registered only if its Site, Zone and
  Aisle exist and are `Active` (`resolveChain`).
- **Placement legality is decided once, at registration**: Deny wins; any
  matching Allow turns a zone into an allow-list
  ([ADR 0003](../adr/0003-placement-rules-at-registration-time.md)). Rule
  changes never retroactively invalidate existing slots
  (`RehydrateLocationSlot` skips the check).
- **Decommission is one-way**; re-registering a retired code is a
  `409 duplicate-location-code` ([ADR 0005](../adr/0005-one-way-decommission.md)).
- **Bulk import is partial-success**, reported per row
  ([ADR 0006](../adr/0006-partial-success-bulk-import.md)).
- **Capacity is required only for roles that hold stock** (`Storage`,
  `Staging`, `Drop`, `Consolidation`); `Dock` needs a `dockFlow`,
  `WorkCenter` at least one activity
  ([ADR 0016](../adr/0016-functional-location-roles.md)).
- **Distance is topology, never time**; cross-zone distance is a flagged
  straight-line estimate only when both slots have geometry, else refused
  ([ADR 0017](../adr/0017-geometry-and-travel-graph.md)).
- **Every Kafka message is CloudEvents 1.0, structured mode**
  ([ADR 0024](../adr/0024-cloudevents-mandatory-envelope.md)); with a
  database, events leave through the transactional outbox
  ([ADR 0018](../adr/0018-transactional-outbox.md)).

## Assumptions

- Consumers tolerate this service being down: the Kafka consumers keep local
  copies, and the two REST callers (`wes-work-planning`, `fulfillment-execution`)
  wrap the call in a circuit breaker and fall back to their `permissive`
  behaviour. `inventory-storage` can run from its Kafka-fed cache.
- A building's layout changes slowly (hundreds to thousands of writes per
  day, mostly during commissioning), so per-day analytics buckets and
  single-row optimistic concurrency on `location_slots` only
  ([ADR 0025](../adr/0025-optimistic-concurrency-version-column.md)) are
  enough.
- Site coordinates are local metres with an arbitrary origin; no geodesy
  (`shared.Point3D`).

## Verification Metrics

| Metric | Source |
|---|---|
| `facility.location_slot.registrations` counter by `outcome` = `accepted` / `rejected_by_placement_rule` / `rejected` | `internal/adapters/outbound/telemetry/metrics.go`, `usecases.registrationOutcome` |
| `http.server.request.duration`, `http.server.active_requests` | `otelchimetric` middleware in `inbound/http/server.go` |
| Analytics freshness lag | `GET /reports/catalog-growth/freshness` |
| Catalog growth per site/zone per day | `GET /reports/catalog-growth`, table `catalog_growth_rollup` |
| Outbox backlog | rows in `outbox_events` with `published_at IS NULL` |

## Open Questions

- Should the travel graph connect zones with a routed path rather than a
  straight-line estimate? (open, [ADR 0017](../adr/0017-geometry-and-travel-graph.md);
  `estimate_travel_distance.go:38-41`)
- Should cross-aisles carry their own geometry? Today every cross-aisle edge
  is estimated from the zone's bay pitch (`travel.crossAisleDistance`).
- Should `FixedStructure` obstacles constrain the travel graph? The domain
  comment says "in a later phase".
- There is no use case that sets `UnderMaintenance`, decommissions a Site,
  Zone, Aisle or CrossAisle, or deletes a placement rule — the methods exist
  on the aggregates but nothing exposes them.
- Nine of the fourteen event types have no external consumer. Is the
  geometry Published Language worth its contract cost? Only
  `task-interleaving` consumes one geometry event (`LocationGeometryUpdated`);
  `wes-work-planning` still reads distance over REST.
