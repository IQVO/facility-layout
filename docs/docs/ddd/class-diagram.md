---
id: class-diagram
title: UML class diagrams
sidebar_label: Class diagrams
sidebar_position: 7
description: UML class diagrams of Facility Layout's domain packages — aggregates, value objects, enumerations, domain events — plus the hexagonal ports-and-adapters view.
---

# UML class diagrams

UML class diagrams of `internal/domain/**`, split into four views so each
stays readable. Names are the real Go type names; fields are the real
(unexported) struct fields, shown with `-`; methods shown are the ones that
**change state** or carry a decision, plus constructors. Getters are omitted.

Stereotypes: `<<AggregateRoot>>`, `<<Entity>>`, `<<ValueObject>>`,
`<<Enumeration>>`, `<<DomainEvent>>`, `<<Repository>>` (an outbound port),
`<<DomainService>>`.

Relationship conventions: `*--` composition (the part has no life outside the
whole, e.g. a value object held by value); `..>` a reference **by identity**
(a string id, never an object pointer — aggregates in this context never hold
each other); `-->` a typed dependency.

## 1. Structural aggregates and their value objects

```mermaid
classDiagram
    class Site {
        <<AggregateRoot>>
        -code string
        -name string
        -status Status
        +NewSite(code, name) Site
        +Decommission() error
    }
    class Zone {
        <<AggregateRoot>>
        -siteCode string
        -areaCode string
        -zoneCode string
        -temperatureClass TemperatureClass
        -hazmat bool
        -status Status
        -bayPitchM float64
        -levelPitchM float64
        +NewZone(siteCode, areaCode, zoneCode, temperatureClass, hazmat) Zone
        +ID() string
        +SetPitch(bayPitchM, levelPitchM) error
        +Decommission() error
    }
    class Aisle {
        <<AggregateRoot>>
        -zoneID string
        -aisleCode string
        -sequenceHint int
        -direction Direction
        -status Status
        -centreline Segment
        +NewAisle(zoneID, aisleCode, sequenceHint, direction) Aisle
        +ID() string
        +SetCentreline(centreline) error
        +Decommission() error
    }
    class CrossAisle {
        <<AggregateRoot>>
        -zoneID string
        -fromAisle string
        -toAisle string
        -atBay string
        -status crossAisleStatus
        +NewCrossAisle(zoneID, fromAisle, toAisle, atBay) CrossAisle
        +Connects(aisleCode) string
        +Decommission() error
    }
    class LocationSlot {
        <<AggregateRoot>>
        -code LocationCode
        -locationType string
        -role LocationRole
        -functional FunctionalAttributes
        -capacity Capacity
        -status Status
        -position Point3D
        -dimensions Dimensions
        -pickSequence int nullable
        -version int
        +NewLocationSlot(code, locationType, capacityOverride, functional, attrs, rules) LocationSlot
        +Decommission() error
        +SetGeometry(position, dimensions) error
        +SetPickSequence(sequence) error
    }
    class LocationCode {
        <<ValueObject>>
        -site string
        -area string
        -zone string
        -aisle string
        -bay string
        -level string
        -position string
        +ParseLocationCode(value) LocationCode
        +ZoneID() string
        +AisleID() string
        +String() string
    }
    class FunctionalAttributes {
        <<ValueObject>>
        -dockFlow DockFlow
        -activities Activity[]
        +NewFunctionalAttributes(role, dockFlow, activities) FunctionalAttributes
    }
    class Capacity {
        <<ValueObject>>
        -maxWeightKg float64
        -maxVolumeM3 float64
        +NewCapacity(maxWeightKg, maxVolumeM3) Capacity
    }
    class Point3D {
        <<ValueObject>>
        -xM float64
        -yM float64
        -zM float64
    }
    class Dimensions {
        <<ValueObject>>
        -widthM float64
        -depthM float64
        -heightM float64
    }
    class Segment {
        <<ValueObject>>
        -start Point3D
        -end Point3D
        +LengthM() float64
        +DistanceToPoint(p) float64
    }
    class Status {
        <<Enumeration>>
        Active
        UnderMaintenance
        Decommissioned
    }
    class TemperatureClass {
        <<Enumeration>>
        Ambient
        Chilled
        Frozen
    }
    class Direction {
        <<Enumeration>>
        OneWay
        TwoWay
    }
    class DockFlow {
        <<Enumeration>>
        Inbound
        Outbound
        Both
    }
    class Activity {
        <<Enumeration>>
        Pack
        Sort
        QC
        VAS
        Deconsolidate
        Receive
        Kit
    }

    Zone ..> Site : siteCode
    Aisle ..> Zone : zoneID
    CrossAisle ..> Zone : zoneID
    CrossAisle ..> Aisle : fromAisle and toAisle codes
    LocationSlot ..> Aisle : code.AisleID
    LocationSlot *-- LocationCode
    LocationSlot *-- FunctionalAttributes
    LocationSlot *-- Capacity
    LocationSlot *-- Point3D
    LocationSlot *-- Dimensions
    Aisle *-- Segment
    Segment *-- Point3D
    Site --> Status
    Zone --> Status
    Zone --> TemperatureClass
    Aisle --> Status
    Aisle --> Direction
    LocationSlot --> Status
    FunctionalAttributes --> DockFlow
    FunctionalAttributes --> Activity
```

Source: `internal/domain/site/site.go`, `zone/zone.go`, `aisle/aisle.go`,
`aisle/cross_aisle.go`, `slot/location_slot.go`, `slot/functional.go`,
`shared/location_code.go`, `shared/capacity.go`, `shared/geometry.go`,
`shared/enums.go`. Omitted: getters, `Rehydrate*` constructors, the
`set` marker fields on the geometry value objects, and `CrossAisle`'s private
`crossAisleStatus` enum (`Active`, `Decommissioned`). `LocationSlot.role` and
`locationType` are copied from the `LocationType` at registration (shown in
view 2). `pickSequence` is a Go `*int` (nil means "derive the order").

## 2. Placement, fixed structures and the travel graph

```mermaid
classDiagram
    class LocationType {
        <<AggregateRoot>>
        -name string
        -role LocationRole
        -defaultCapacity Capacity
        +NewLocationType(name, role, defaultCapacity) LocationType
    }
    class LocationRole {
        <<Enumeration>>
        Storage
        Dock
        Yard
        WorkCenter
        Drop
        Staging
        QC
        Consolidation
        Shipping
        +RequiresCapacity() bool
    }
    class PlacementRule {
        <<AggregateRoot>>
        -id string
        -locationType string
        -effect Effect
        -predicate ZonePredicate
        +NewPlacementRule(id, locationType, effect, predicate) PlacementRule
        +Describe() string
    }
    class Effect {
        <<Enumeration>>
        Allow
        Deny
    }
    class ZonePredicate {
        <<ValueObject>>
        -zoneCode string
        -temperatureClass TemperatureClass
        -hazmat bool nullable
        +Matches(attrs) bool
    }
    class ZoneAttributes {
        <<ValueObject>>
        +ZoneID string
        +ZoneCode string
        +TemperatureClass TemperatureClass
        +Hazmat bool
    }
    class RuleSet {
        <<DomainService>>
        +Check(locationType, attrs) error
    }
    class FixedStructure {
        <<AggregateRoot>>
        -id string
        -siteCode string
        -kind Kind
        -footprint Rect
        -label string
        +NewFixedStructure(id, siteCode, kind, footprint, label) FixedStructure
    }
    class Kind {
        <<Enumeration>>
        Wall
        Column
        Office
        Conveyor
        Other
    }
    class Rect {
        <<ValueObject>>
        -origin Point3D
        -size Dimensions
    }
    class Graph {
        <<DomainService>>
        -adjacency map
        -nodes map
        -pitch Pitch
        +Build(aisles, crossAisles, pitch) Graph
        +Distance(from, to) Route
    }
    class Node {
        <<ValueObject>>
        +AisleID string
        +Bay string
    }
    class Edge {
        <<ValueObject>>
        +From Node
        +To Node
        +MetresM float64
        +Estimated bool
    }
    class Route {
        <<ValueObject>>
        +MetresM float64
        +Estimated bool
        +Nodes Node[]
    }
    class AisleGeom {
        <<ValueObject>>
        +AisleID string
        +Bays string[]
        +OneWay bool
        +CentrelineM float64
    }
    class CrossAisleRef {
        <<ValueObject>>
        +FromAisleID string
        +ToAisleID string
        +AtBay string
    }
    class Pitch {
        <<ValueObject>>
        +BayPitchM float64
        +LevelPitchM float64
    }

    LocationType --> LocationRole
    LocationType *-- Capacity
    PlacementRule ..> LocationType : locationType name
    PlacementRule --> Effect
    PlacementRule *-- ZonePredicate
    RuleSet o-- PlacementRule
    RuleSet ..> ZoneAttributes : checks against
    FixedStructure --> Kind
    FixedStructure *-- Rect
    Graph *-- Node
    Graph *-- Edge
    Graph *-- Pitch
    Graph ..> AisleGeom : built from
    Graph ..> CrossAisleRef : built from
    Graph ..> Route : returns
```

Source: `internal/domain/placement/placement.go`, `placement/role.go`,
`placement/rules.go`, `structure/fixed_structure.go`, `shared/geometry.go`,
`travel/graph.go`. Omitted: the `Capacity` and `Point3D`/`Dimensions` boxes
(view 1), the well-known LocationType name constants (`PalletRack`, `Shelf`,
`ToteWall`, `BulkFloor`, `Staging`, `Amnesty` — strings, not an enum), and
the Dijkstra priority queue. `RuleSet` is a Go slice type `[]PlacementRule`
with behaviour. `ZonePredicate.hazmat` is a `*bool` (nil = wildcard). The
LocationRole constant for staging is named `RoleStaging` in Go; its value is
`Staging`.

## 3. Domain events (the Published Language)

```mermaid
classDiagram
    class DomainEvent {
        <<interface>>
        +EventName() string
        +EventType() string
        +OccurredAt() time
    }
    class base {
        +Name string
        +Type string
        +At time
    }
    class SiteRegistered {
        <<DomainEvent>>
        siteCode, siteName
    }
    class ZoneRegistered {
        <<DomainEvent>>
        zoneId, siteCode, areaCode, zoneCode, temperatureClass, hazmat
    }
    class AisleRegistered {
        <<DomainEvent>>
        aisleId, zoneId, aisleCode, sequenceHint, direction
    }
    class LocationTypeRegistered {
        <<DomainEvent>>
        locationType, role, maxWeightKg, maxVolumeM3
    }
    class PlacementRuleDefined {
        <<DomainEvent>>
        ruleId, locationType, effect, predicate
    }
    class LocationSlotRegistered {
        <<DomainEvent>>
        locationCode, aisleId, zoneId, locationType, role, dockFlow, activities, maxWeightKg, maxVolumeM3
    }
    class LocationSlotDecommissioned {
        <<DomainEvent>>
        locationCode
    }
    class FacilityLayoutImported {
        <<DomainEvent>>
        rowsSubmitted, slotsImported, rowsRejected
    }
    class LocationGeometryUpdated {
        <<DomainEvent>>
        locationCode, xM, yM, zM, widthM, depthM, heightM, pickSequence
    }
    class AisleGeometryUpdated {
        <<DomainEvent>>
        aisleId, startXM, startYM, startZM, endXM, endYM, endZM, lengthM
    }
    class FixedStructureRegistered {
        <<DomainEvent>>
        structureId, siteCode, kind, xM, yM, zM, widthM, depthM, heightM, label
    }
    class CrossAisleRegistered {
        <<DomainEvent>>
        zoneId, fromAisle, toAisle, atBay
    }

    DomainEvent <|.. base
    base <|-- SiteRegistered
    base <|-- ZoneRegistered
    base <|-- AisleRegistered
    base <|-- LocationTypeRegistered
    base <|-- PlacementRuleDefined
    base <|-- LocationSlotRegistered
    base <|-- LocationSlotDecommissioned
    base <|-- FacilityLayoutImported
    base <|-- LocationGeometryUpdated
    base <|-- AisleGeometryUpdated
    base <|-- FixedStructureRegistered
    base <|-- CrossAisleRegistered
```

Source: `internal/domain/shared/events.go`. Field lists are the JSON tags
(the wire `data` of the CloudEvent). "Inheritance" here is Go struct
embedding of the unexported `base`. Full types, topics and keys:
[Domain events](./domain-events.md).

## 4. Hexagonal view — ports and adapters

```mermaid
flowchart LR
    subgraph Inbound["Inbound adapters"]
        HTTP["inbound/http<br/>chi router, RFC 7807,<br/>RequireIdempotencyKey"]
        MCPA["inbound/mcp<br/>7 tools, 1 resource template,<br/>1 prompt"]
        KIN["inbound/kafka<br/>AnalyticsConsumer<br/>projector only"]
    end

    subgraph App["Application"]
        UC["usecases<br/>31 use-case structs<br/>the inbound ports"]
        subgraph Ports["ports - Repository and driven ports"]
            REPO["SiteRepo, ZoneRepo, AisleRepo,<br/>CrossAisleRepo, SlotRepo,<br/>LocationTypeRepo, PlacementRuleRepo,<br/>FixedStructureRepo"]
            PUB["EventPublisher"]
            UOW["UnitOfWork"]
            CLK["Clock"]
            MET["LocationMetrics"]
        end
    end

    DOM["domain<br/>site, zone, aisle, slot,<br/>placement, structure, travel, shared"]

    subgraph Outbound["Outbound adapters"]
        PG["outbound/postgres<br/>repos, UnitOfWork,<br/>OutboxPublisher, OutboxRelay, Sweeper"]
        MEM["outbound/memory<br/>repos, SystemClock"]
        KF["outbound/kafka<br/>Publisher, AnalyticsPublisher,<br/>FanOut, RelaySink"]
        EV["outbound/events<br/>log and buffered publishers"]
        TEL["outbound/telemetry<br/>LocationMetrics"]
        AS["outbound/analyticsstore<br/>projection and report repos"]
    end

    HTTP --> UC
    MCPA --> UC
    UC --> DOM
    UC --> REPO
    UC --> PUB
    UC --> UOW
    UC --> CLK
    UC --> MET
    REPO -.implemented by.-> PG
    REPO -.implemented by.-> MEM
    UOW -.implemented by.-> PG
    PUB -.implemented by.-> PG
    PUB -.implemented by.-> KF
    PUB -.implemented by.-> EV
    CLK -.implemented by.-> MEM
    MET -.implemented by.-> TEL
    KIN --> AS
```

Source: `internal/application/ports/ports.go`, `cmd/facility/main.go`
(`buildAdapters`, `memoryAdapters`, `outboxAdapters`), `cmd/mcp/main.go`,
`cmd/facility-projector/main.go`, `internal/adapters/**`. The analytics
consumer writes through `internal/analytics/report` ports implemented by
`outbound/analyticsstore` and never touches the OLTP application layer
(ADR 0010, enforced by `internal/architecture`). `cmd/facility-reports` reads
the same store through `inbound/http/reports_handler.go`. Omitted:
`internal/pgtx` (transaction-in-context helper), `outbound/bootretry`, and
`kafka/cloudevents` (the CloudEvents helper used by every Kafka adapter).
