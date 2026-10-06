---
id: eventstorming
title: EventStorming (design level)
sidebar_label: EventStorming
sidebar_position: 6
description: Design-level EventStorming of Facility Layout's main processes in ddd-crew sticky-note notation, with every sticky traced to code and hotspots taken from real known gaps.
---

# EventStorming (design level)

Design-level EventStorming in the
[ddd-crew EventStorming glossary & cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet)
notation. Each flow reads left to right: **Actor → Command → Aggregate →
Domain Event → Policy → next Command / Read Model / External System**.
Hotspots are real gaps recorded in the code, ADRs or this site — none are
invented for the exercise.

## Legend

```mermaid
flowchart LR
    A["Actor"]:::actor
    C["Command"]:::command
    G["Aggregate"]:::aggregate
    E["Domain Event"]:::event
    P["Policy"]:::policy
    R["Read Model"]:::readmodel
    X["External System"]:::external
    H["Hotspot"]:::hotspot

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000,font-size:11px
    classDef command fill:#4aa3df,stroke:#1f6f9f,color:#000
    classDef aggregate fill:#f7d84a,stroke:#a68b00,color:#000
    classDef event fill:#f6a04d,stroke:#b5651d,color:#000
    classDef policy fill:#c39bd3,stroke:#7d3c98,color:#000
    classDef readmodel fill:#7dcea0,stroke:#1e8449,color:#000
    classDef external fill:#f1948a,stroke:#b03a2e,color:#000
    classDef hotspot fill:#e74c3c,stroke:#922b21,color:#fff
```

## Process 1 — Building the map (structure and placement rules)

```mermaid
flowchart LR
    OP["Operator<br/>via facility-mfe"]:::actor
    C1["RegisterSite"]:::command
    G1["Site"]:::aggregate
    E1["SiteRegistered"]:::event
    C2["RegisterZone"]:::command
    G2["Zone"]:::aggregate
    E2["ZoneRegistered"]:::event
    C3["RegisterAisle"]:::command
    G3["Aisle"]:::aggregate
    E3["AisleRegistered"]:::event
    C4["RegisterLocationType"]:::command
    G4["LocationType"]:::aggregate
    E4["LocationTypeRegistered"]:::event
    C5["DefinePlacementRule"]:::command
    G5["PlacementRule"]:::aggregate
    E5["PlacementRuleDefined"]:::event
    P1["Parent must exist<br/>and be Active"]:::policy
    X1["inventory-storage<br/>facilitycache"]:::external
    R1["Site layout / Zone grid"]:::readmodel
    H1["No use case decommissions<br/>a Site, Zone or Aisle<br/>or sets UnderMaintenance"]:::hotspot

    OP --> C1 --> G1 --> E1
    E1 --> P1
    OP --> C2 --> G2 --> E2
    P1 --> C2
    E2 --> P1
    OP --> C3 --> G3 --> E3
    P1 --> C3
    E2 --> X1
    OP --> C4 --> G4 --> E4
    OP --> C5 --> G5 --> E5
    E3 --> R1
    G1 -.- H1

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000,font-size:11px
    classDef command fill:#4aa3df,stroke:#1f6f9f,color:#000
    classDef aggregate fill:#f7d84a,stroke:#a68b00,color:#000
    classDef event fill:#f6a04d,stroke:#b5651d,color:#000
    classDef policy fill:#c39bd3,stroke:#7d3c98,color:#000
    classDef readmodel fill:#7dcea0,stroke:#1e8449,color:#000
    classDef external fill:#f1948a,stroke:#b03a2e,color:#000
    classDef hotspot fill:#e74c3c,stroke:#922b21,color:#fff
```

Source: `internal/application/usecases/register_site.go`, `register_zone.go`,
`register_aisle.go`, `register_location_type.go`, `define_placement_rule.go`,
`internal/domain/shared/enums.go`. Omitted: the analytics topic and the
Idempotency-Key check.

## Process 2 — Registering a slot (single or bulk)

```mermaid
flowchart LR
    OP["Operator"]:::actor
    C6["RegisterLocationSlot"]:::command
    C7["ImportFacilityLayout"]:::command
    P2["Chain of custody:<br/>Site, Zone, Aisle Active"]:::policy
    P3["Placement rules:<br/>Deny wins, Allow = allow-list"]:::policy
    P4["Role needs dockFlow /<br/>activities / capacity"]:::policy
    P5["Ensure parents exist<br/>per row"]:::policy
    G6["LocationSlot"]:::aggregate
    E6["LocationSlotRegistered"]:::event
    E7["FacilityLayoutImported"]:::event
    X2["inventory-storage"]:::external
    X3["warehouse-planning"]:::external
    R2["Locations by role"]:::readmodel
    R3["Catalog growth report"]:::readmodel

    OP --> C6 --> P2 --> P4 --> P3 --> G6 --> E6
    OP --> C7 --> P5 --> C6
    C7 --> E7
    E6 --> X2
    E6 --> X3
    E6 --> R2
    E6 --> R3
    E7 --> R3

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000,font-size:11px
    classDef command fill:#4aa3df,stroke:#1f6f9f,color:#000
    classDef aggregate fill:#f7d84a,stroke:#a68b00,color:#000
    classDef event fill:#f6a04d,stroke:#b5651d,color:#000
    classDef policy fill:#c39bd3,stroke:#7d3c98,color:#000
    classDef readmodel fill:#7dcea0,stroke:#1e8449,color:#000
    classDef external fill:#f1948a,stroke:#b03a2e,color:#000
```

Source: `internal/application/usecases/register_location_slot.go`,
`import_facility_layout.go`, `internal/domain/slot/location_slot.go`,
`functional.go`, `internal/domain/placement/rules.go`,
`internal/adapters/inbound/kafka/analytics_consumer.go`. Omitted:
`SetLocationGeometry` invoked by import rows that carry geometry (Process 3),
and the rejection branches (see [Invariants](./invariants.md)).

## Process 3 — Geometry and travel distance

```mermaid
flowchart LR
    OP["Operator"]:::actor
    C8["SetLocationGeometry"]:::command
    C9["SetAisleGeometry"]:::command
    C10["RegisterCrossAisle"]:::command
    C11["RegisterFixedStructure"]:::command
    G6["LocationSlot"]:::aggregate
    G3["Aisle"]:::aggregate
    G7["CrossAisle"]:::aggregate
    G8["FixedStructure"]:::aggregate
    E8["LocationGeometryUpdated"]:::event
    E9["AisleGeometryUpdated"]:::event
    E10["CrossAisleRegistered"]:::event
    E11["FixedStructureRegistered"]:::event
    R4["Zone travel graph"]:::readmodel
    R5["Travel distance"]:::readmodel
    X4["wes-work-planning"]:::external
    X5["warehouse-ops-agent"]:::external
    H2["No routed cross-zone path:<br/>straight line or 422"]:::hotspot
    H3["Cross-aisle edges always<br/>estimated from bay pitch"]:::hotspot
    H4["FixedStructure not yet<br/>an obstacle in the graph"]:::hotspot
    H5["Geometry events keyed by<br/>event type, not aggregate"]:::hotspot

    OP --> C8 --> G6 --> E8
    OP --> C9 --> G3 --> E9
    OP --> C10 --> G7 --> E10
    OP --> C11 --> G8 --> E11
    E8 --> R4
    E9 --> R4
    E10 --> R4
    R4 --> R5
    R5 --> X4
    R5 --> X5
    R5 -.- H2
    G7 -.- H3
    G8 -.- H4
    E8 -.- H5

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000,font-size:11px
    classDef command fill:#4aa3df,stroke:#1f6f9f,color:#000
    classDef aggregate fill:#f7d84a,stroke:#a68b00,color:#000
    classDef event fill:#f6a04d,stroke:#b5651d,color:#000
    classDef readmodel fill:#7dcea0,stroke:#1e8449,color:#000
    classDef external fill:#f1948a,stroke:#b03a2e,color:#000
    classDef hotspot fill:#e74c3c,stroke:#922b21,color:#fff
```

Source: `internal/application/usecases/set_location_geometry.go`,
`set_aisle_geometry.go`, `register_cross_aisle.go`,
`register_fixed_structure.go`, `travel_graph_builder.go`,
`estimate_travel_distance.go`, `internal/domain/travel/graph.go`,
`internal/adapters/outbound/kafka/publisher.go`. The read models are built
per request from repository state, not from the events — the event arrows
show what makes the read model change. Omitted: zone pitch (set only at
`RegisterZone`).

## Process 4 — Retiring a slot

```mermaid
flowchart LR
    OP["Operator"]:::actor
    C12["DecommissionLocationSlot"]:::command
    G6["LocationSlot"]:::aggregate
    E12["LocationSlotDecommissioned"]:::event
    P6["One-way: re-register<br/>is a duplicate"]:::policy
    P7["Optimistic concurrency<br/>version check"]:::policy
    X2["inventory-storage"]:::external
    X3["warehouse-planning"]:::external
    R3["Catalog growth report"]:::readmodel

    OP --> C12 --> P7 --> G6 --> E12
    G6 --> P6
    E12 --> X2
    E12 --> X3
    E12 --> R3

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000,font-size:11px
    classDef command fill:#4aa3df,stroke:#1f6f9f,color:#000
    classDef aggregate fill:#f7d84a,stroke:#a68b00,color:#000
    classDef event fill:#f6a04d,stroke:#b5651d,color:#000
    classDef policy fill:#c39bd3,stroke:#7d3c98,color:#000
    classDef readmodel fill:#7dcea0,stroke:#1e8449,color:#000
    classDef external fill:#f1948a,stroke:#b03a2e,color:#000
```

Source: `internal/application/usecases/decommission_location_slot.go`,
`internal/adapters/outbound/postgres/slot_repo.go` (version guard),
[ADR 0005](../adr/0005-one-way-decommission.md),
[ADR 0025](../adr/0025-optimistic-concurrency-version-column.md).

## Sticky evidence

| Sticky | Kind | Code evidence |
|---|---|---|
| Operator | Actor | `web/` (`facility-mfe`), or any REST client |
| RegisterSite … DefinePlacementRule | Command | `internal/application/usecases/register_site.go`, `register_zone.go`, `register_aisle.go`, `register_location_type.go`, `define_placement_rule.go` |
| RegisterLocationSlot, ImportFacilityLayout, DecommissionLocationSlot | Command | `register_location_slot.go`, `import_facility_layout.go`, `decommission_location_slot.go` |
| SetLocationGeometry, SetAisleGeometry, RegisterCrossAisle, RegisterFixedStructure | Command | `set_location_geometry.go`, `set_aisle_geometry.go`, `register_cross_aisle.go`, `register_fixed_structure.go` |
| Site, Zone, Aisle, CrossAisle, LocationSlot, LocationType, PlacementRule, FixedStructure | Aggregate | `internal/domain/site`, `zone`, `aisle`, `slot`, `placement`, `structure` |
| 12 events | Domain Event | `internal/domain/shared/events.go` |
| Parent must exist and be Active / Chain of custody | Policy | `RegisterZone`, `RegisterAisle`, `RegisterLocationSlot.resolveChain` |
| Placement rules | Policy | `placement.RuleSet.Check` |
| Role needs dockFlow / activities / capacity | Policy | `slot.NewFunctionalAttributes`, `LocationRole.RequiresCapacity` |
| Ensure parents exist per row | Policy | `ImportFacilityLayout.ensureSite`, `ensureZone`, `ensureAisle` |
| One-way decommission | Policy | `ErrDuplicateLocationCode`, `slot.ErrAlreadyDecommissioned` |
| Optimistic concurrency | Policy | Postgres `SlotRepo.Save`, `ports.ErrConcurrentModification` |
| Site layout, Zone grid, Locations by role, Zone travel graph, Travel distance | Read Model | `get_site_layout.go`, `get_zone_grid.go`, `reads.go`, `get_zone_travel_graph.go`, `estimate_travel_distance.go` |
| Catalog growth report | Read Model | `cmd/facility-projector`, `catalog_growth_rollup` |
| inventory-storage, warehouse-planning, wes-work-planning, warehouse-ops-agent | External System | see [Context map](../ecosystem/context-map.md) |
| H1 nothing decommissions Site/Zone/Aisle or sets UnderMaintenance | Hotspot | `internal/domain/shared/enums.go` (UnderMaintenance comment); only `DecommissionLocationSlot` calls a `Decommission` method |
| H2 no routed cross-zone path | Hotspot | `estimate_travel_distance.go` (`crossZoneBeeline`), [ADR 0017](../adr/0017-geometry-and-travel-graph.md) |
| H3 cross-aisle edges always estimated | Hotspot | `travel.crossAisleDistance` |
| H4 FixedStructure not an obstacle | Hotspot | `internal/domain/structure/fixed_structure.go` package comment ("in a later phase") |
| H5 geometry and import events keyed by event type | Hotspot | `kafka.aggregateKey` default branch — all occurrences of those five event types share one partition |
