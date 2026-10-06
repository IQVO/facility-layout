---
id: aggregates
title: Aggregates
sidebar_label: Aggregates
description: Site, Zone, Aisle, LocationType, PlacementRule, LocationSlot, CrossAisle and FixedStructure — identities, state and behaviour.
---

# Aggregates

Six core modelling elements make up this context. Four are structural
aggregates in a strict hierarchy; two describe placement legality. Two more —
`CrossAisle` and `FixedStructure` — describe physical geometry for the travel
graph and the floor plan ([ADR 0017](../adr/0017-geometry-and-travel-graph.md)).
Each of the eight has its own repository port. The per-aggregate
[Aggregate Design Canvas](./aggregate-design-canvas.md) adds state
diagrams, handled commands, created events and throughput estimates; the
[class diagrams](./class-diagram.md) show every type.

```mermaid
graph TD
    Site["<b>Site</b><br/>identity: SiteCode<br/>e.g. WH1"]
    Zone["<b>Zone</b><br/>identity: Site-Area-Zone<br/>e.g. WH1-STOR-AMB<br/>TemperatureClass · Hazmat"]
    Aisle["<b>Aisle</b><br/>identity: ZoneID-Aisle<br/>e.g. WH1-STOR-AMB-A07<br/>SequenceHint · Direction"]
    Slot["<b>LocationSlot</b><br/>identity: the LocationCode<br/>e.g. WH1-STOR-AMB-A07-03-02-B<br/>LocationType · Capacity · Status"]
    LType["<b>LocationType</b><br/>identity: Name<br/>e.g. PalletRack<br/>LocationRole · default Capacity"]
    Rule["<b>PlacementRule</b><br/>identity: RuleId<br/>LocationType · Effect · ZonePredicate"]

    Site -->|"scopes"| Zone
    Zone -->|"scopes"| Aisle
    Aisle -->|"scopes"| Slot
    LType -->|"classifies"| Slot
    Rule -->|"references"| LType
    Rule -.->|"matched against a Zone's<br/>code / temperature / hazmat"| Zone
    Rule ==>|"evaluated at registration"| Slot
```

Every aggregate holds only unexported fields, exposes read accessors, and is
constructed through a validating factory. There is no way to obtain an
invalid instance.

## Site

The root of the hierarchy: a physical facility or building.

| | |
|---|---|
| **Identity** | `SiteCode` — non-empty, `[A-Z0-9]` only, unique |
| **State** | `code`, `name`, `status` |
| **Constructor** | `NewSite(code, name string) (*Site, error)` |
| **Rehydration** | `RehydrateSite(code, name string, status shared.Status) *Site` |
| **Behaviour** | `Decommission() error` |
| **Errors** | `ErrEmptySiteCode`, `ErrInvalidSiteCode`, `ErrEmptySiteName`, `ErrAlreadyDecommissioned` |

Uniqueness is enforced at the application/repository layer, not inside the
aggregate: a single aggregate cannot see its siblings, so it cannot police a
global constraint. The aggregate rejects an empty or malformed code; the use
case rejects a duplicate one.

## Zone

A behavioral classification scoped to exactly one Site. It bundles the
LocationCode's **Area** and **Zone** segments into a single aggregate — they
are never independently meaningful, they are always registered together, and
every placement decision needs both, so splitting them would produce an
aggregate that can never be used alone.

| | |
|---|---|
| **Identity** | `ID()` = `SiteCode-AreaCode-ZoneCode`, e.g. `WH1-STOR-AMB` |
| **State** | `siteCode`, `areaCode`, `zoneCode`, `temperatureClass`, `hazmat`, `status`, `bayPitchM`, `levelPitchM` |
| **Constructor** | `NewZone(siteCode, areaCode, zoneCode string, temperatureClass shared.TemperatureClass, hazmat bool) (*Zone, error)` |
| **Behaviour** | `Decommission() error`, `SetPitch(bayPitchM, levelPitchM float64) error` |
| **Errors** | `ErrEmptySiteCode`, `ErrEmptyAreaCode`, `ErrEmptyZoneCode`, `ErrInvalidCode`, `ErrAlreadyDecommissioned`, `ErrInvalidPitch` |

The bay and level pitch are the travel graph's fallback distances when an
aisle has no real centreline geometry (default bay pitch 1.2 m).

`TemperatureClass` and `Hazmat` are not decoration. They are the fields a
`PlacementRule` predicate matches on, and they are what makes a Zone a
*behavioral* classification rather than a label.

This is the same `Zone` word the WES tier already uses for congestion and
travel-path reasoning. This service becomes its source of truth.

## Aisle

A physical corridor scoped to exactly one Zone.

| | |
|---|---|
| **Identity** | `ID()` = `ZoneID-AisleCode`, e.g. `WH1-STOR-AMB-A07` |
| **State** | `zoneID`, `aisleCode`, `sequenceHint`, `direction`, `status`, `centreline` |
| **Constructor** | `NewAisle(zoneID, aisleCode string, sequenceHint int, direction shared.Direction) (*Aisle, error)` |
| **Behaviour** | `Decommission() error`, `SetCentreline(shared.Segment) error` |
| **Errors** | `ErrEmptyZoneID`, `ErrEmptyAisleCode`, `ErrInvalidAisleCode`, `ErrNegativeSequenceHint`, `ErrAlreadyDecommissioned`, `ErrAisleDecommissioned` |

`SequenceHint` is the walk-order position of the aisle — the concrete
travel-distance input the WES tier needs and previously had nowhere to get.
The layout read model orders aisles by it, not by registration order.
`Direction` (`OneWay`/`TwoWay`) is the second travel input.

## LocationType

A reusable classification of physical slot shape/kind, carrying the default
capacity envelope its slots inherit and the functional role they play.

| | |
|---|---|
| **Identity** | `Name` |
| **State** | `name`, `role`, `defaultCapacity` |
| **Constructor** | `NewLocationType(name string, role LocationRole, defaultCapacity shared.Capacity) (LocationType, error)` |
| **Errors** | `ErrEmptyLocationTypeName`, `ErrUnknownLocationRole`, `shared.ErrInvalidMaxWeight` |

`LocationRole` says what a location is *for*: `Storage` (the default when
none is given), `Dock`, `Yard`, `WorkCenter`, `Drop`, `Staging`, `QC`,
`Consolidation` or `Shipping`
([ADR 0016](../adr/0016-functional-location-roles.md)). Only the roles that
hold stock — `Storage`, `Staging`, `Drop`, `Consolidation` — require a
capacity envelope; for the others it is optional.

The well-established names from the domain reference are declared as
constants — `PalletRack`, `Shelf`, `ToteWall`, `BulkFloor`, `Staging`,
`Amnesty` — but the type is deliberately **not** an enum:
`RegisterLocationType` accepts any name. Real buildings invent slot kinds,
and a closed enum would force a code change and a redeploy for a rack shape
that the domain does not otherwise care about.

`Amnesty` is a real term from the domain reference: where a damaged or
mismatched item is set aside during stow. It is a physical slot with a shape
and a capacity like any other, so it is a LocationType.

## PlacementRule

A declaration of which LocationTypes are legal in which Zones. Value-typed,
not a pointer aggregate — a rule has no lifecycle beyond existing.

| | |
|---|---|
| **Identity** | `RuleId` |
| **State** | `ruleID`, `locationType`, `effect` (`Allow`/`Deny`), `predicate` |
| **Predicate** | any of `zoneCode`, `temperatureClass`, `hazmat`; every set field must match (AND), unset fields are wildcards, at least one must be set |
| **Errors** | `ErrEmptyRuleID`, `ErrEmptyRuleLocationType`, `ErrUnknownEffect`, `ErrEmptyPredicate` |

`ErrEmptyPredicate` is worth calling out: a rule whose predicate constrains
nothing would match every zone in the building. That is almost always a typo
in a rule definition rather than an intent, so it is rejected at
construction.

A `RuleSet` is the collection of rules applicable to one placement decision.
It is loaded by the use case from the repository and handed to the
`LocationSlot` constructor — an aggregate never reaches outside itself to
query a repository.

```go
// Semantics, in evaluation order:
//
//  1. Any matching Deny rule naming this LocationType rejects it. Deny wins.
//  2. If any matching Allow rule exists for the zone at all, the zone is an
//     allow-list: the LocationType must be named by one of them.
//  3. Otherwise the zone is unconstrained and the placement is permitted.
func (rs RuleSet) Check(locationType string, attrs ZoneAttributes) error
```

## LocationSlot

The leaf aggregate: one coded physical slot. **Its identity is its
LocationCode.**

| | |
|---|---|
| **Identity** | `shared.LocationCode` — globally unique |
| **State** | `code`, `locationType`, `role`, `functional` (`dockFlow` / `activities`), `capacity`, `status`, optional `position`, `dimensions`, `pickSequence`, and `version` (optimistic-concurrency token, [ADR 0025](../adr/0025-optimistic-concurrency-version-column.md)) |
| **Constructor** | `NewLocationSlot(code, locationType, capacityOverride, functional, attrs, rules) (*LocationSlot, error)` |
| **Behaviour** | `Decommission() error`, `SetGeometry(position, dimensions) error`, `SetPickSequence(int) error` |
| **Errors** | `ErrMissingLocationCode`, `ErrMissingLocationType`, `ErrZoneMismatch`, `ErrAlreadyDecommissioned`, `ErrSlotDecommissioned`, `ErrNegativePickSequence`, `placement.ErrPlacementRuleViolated` |

The constructor is the most interesting signature in the domain:

```go
func NewLocationSlot(
	code shared.LocationCode,
	locationType placement.LocationType,
	capacityOverride shared.Capacity,
	functional FunctionalAttributes,
	attrs placement.ZoneAttributes,
	rules placement.RuleSet,
) (*LocationSlot, error)
```

`functional` is built beforehand by `NewFunctionalAttributes(role, dockFlow,
activities)`, which requires a `dockFlow` on a `Dock` slot, at least one
activity on a `WorkCenter` slot, and neither on any other role
(`ErrDockFlowRequired`, `ErrWorkCenterActivitiesRequired`,
`ErrFunctionalAttributesNotAllowed`).

`attrs` and `rules` are *passed in* rather than looked up. That is Vernon's
"aggregates don't reach outside themselves" discipline applied literally:
the use case resolves the Zone, reads its attributes, loads the applicable
rule set, and hands both to the constructor. The aggregate then enforces:

1. The code is non-zero.
2. The LocationType has a name.
3. `attrs.ZoneID` matches the code's own `ZoneID()` — the caller cannot hand
   in one zone's attributes while registering a slot in another
   (`ErrZoneMismatch`).
4. The capacity envelope resolves: the override if given, otherwise the
   LocationType's default. It must be positive when the type's role requires
   capacity.
5. `rules.Check(...)` passes.

Only then does a `LocationSlot` exist. There is no path that produces an
illegal one.

## CrossAisle

A walkable connection between two aisles of the same zone, at one bay.

| | |
|---|---|
| **Identity** | `zoneID` + `fromAisle` + `toAisle` + `atBay` |
| **Constructor** | `NewCrossAisle(zoneID, fromAisle, toAisle, atBay string) (*CrossAisle, error)` |
| **Behaviour** | `Decommission() error`, `Connects(aisleCode) (other string, ok bool)` |
| **Errors** | `ErrCrossAisleEmptyZoneID`, `ErrCrossAisleEmptyFromAisle`, `ErrCrossAisleEmptyToAisle`, `ErrCrossAisleSameAisle`, `ErrCrossAisleEmptyBay` |

## FixedStructure

A non-slot physical obstacle on a site's floor plan.

| | |
|---|---|
| **Identity** | `ID` — unique; a duplicate is rejected by the use case |
| **State** | `siteCode`, `kind` (`Wall`/`Column`/`Office`/`Conveyor`/`Other`), `footprint` (a `shared.Rect` in metres), `label` |
| **Constructor** | `NewFixedStructure(id, siteCode string, kind Kind, footprint shared.Rect, label string) (*FixedStructure, error)` |
| **Errors** | `ErrEmptyID`, `ErrEmptySiteCode`, `ErrUnknownKind`, `ErrEmptyFootprint`, `ErrEmptyLabel` |

## The travel graph

`internal/domain/travel` is not an aggregate but a pure-domain value: a
zone's aisle/bay waypoints joined by directed, metre-weighted edges, built
from aisle centrelines, one-way directions and cross-aisles (falling back to
the zone's bay pitch, flagged `estimated`, when geometry is missing). Its
shortest-path search backs `GET /distance` and returns `ErrNoRoute` or
`ErrUnknownNode` rather than guessing.

## Read models are not aggregates

`GetSiteLayout`, `GetZoneGrid` and `GetZoneTravelGraph` return **projections** assembled by
querying across these aggregates through the repositories. They are not
separately stored state, they emit no events, and they perform no writes.
Because they are derived on read, they cannot go stale relative to the
aggregates they are built from.
