---
id: ubiquitous-language
title: Ubiquitous language
sidebar_label: Ubiquitous language
description: The exact vocabulary of the Facility Layout bounded context, and the words it deliberately does not use.
---

# Ubiquitous language

These are the exact names used in the code, the API, the events and this
documentation. Where a word is shared with another bounded context, the
overlap is called out explicitly — same word, different model is allowed in
DDD and expected here.

## Core terms

The **Code identifier** column names the Go type or function the term maps
to. A ⚠ marks a term whose code name differs from the spoken word — see
[Name mismatches](#name-mismatches-between-language-and-code).

| Term | Meaning | Code identifier |
|---|---|---|
| **Site** | A physical facility/building. The root of the hierarchy. Has a `SiteCode` (non-empty, uppercase alphanumeric, unique) and a human name. | `site.Site` |
| **Zone** | A behavioral classification scoped to a Site, bundling the Area and Zone code segments into one aggregate. Carries a `TemperatureClass` (Ambient/Chilled/Frozen) and a `Hazmat` flag. Zones are not cosmetic — every `PlacementRule` is keyed by one. Its identity is the zone id `SITE-AREA-ZONE`. | `zone.Zone`, `Zone.ID()` |
| **Aisle** | A physical corridor scoped to a Zone. Carries a `SequenceHint` (its walk-order position — the concrete travel-distance input the WES tier needs) and a `Direction` (`OneWay`/`TwoWay`). | `aisle.Aisle` |
| **LocationType** | A reusable classification of physical slot shape/kind — `PalletRack`, `Shelf`, `ToteWall`, `BulkFloor`, `Staging`, `Amnesty` — each carrying a default capacity envelope (max weight, max volume) and a `LocationRole`. The names are well-known constants, not an enum: any name may be registered. | `placement.LocationType`; constants `placement.PalletRack` … `placement.Amnesty` |
| **LocationRole** | What a LocationType is *for*, independent of its shape: `Storage` (the default), `Dock`, `Yard`, `WorkCenter`, `Drop`, `Staging`, `QC`, `Consolidation`, `Shipping`. Every slot inherits its type's role. See [ADR 0016](../adr/0016-functional-location-roles.md). | `placement.LocationRole` (⚠ `Staging` is `placement.RoleStaging`) |
| **FixedStructure** | A non-slot physical obstacle on a Site's floor plan — `Wall`, `Column`, `Office`, `Conveyor` or `Other` — with a footprint in metres. | `structure.FixedStructure`, `structure.Kind` |
| **CrossAisle** | A walkable connection between two aisles of the same zone at a given bay. An edge in the travel graph. | `aisle.CrossAisle` |
| **Travel graph** | A zone's walkable topology: aisle/bay waypoints joined by directed, metre-weighted edges, built from aisle centrelines and cross-aisles (or from the zone's bay pitch when geometry is missing). The basis for `GET /distance`. See [ADR 0017](../adr/0017-geometry-and-travel-graph.md). | `travel.Graph`; a waypoint is `travel.Node` |
| **LocationSlot** | The leaf aggregate: one coded physical slot. Its identity **is** its `LocationCode`. Has a LocationType, a capacity envelope (which may override the type's default), and a `Status`. | `slot.LocationSlot` |
| **PlacementRule** | A declaration of which LocationTypes are legal in which Zones. The mechanism that prevents "ambient product in the frozen zone" — enforced once, at registration time, not re-checked by every caller. | `placement.PlacementRule`; evaluated by `placement.RuleSet.Check` |
| **LocationCode** | The coded address of a slot: seven typed, hyphen-joined segments, coarsest to finest. A value object, never free text. | `shared.LocationCode` |
| **Facility layout** | The readable, drawable projection of the whole structure: a Site's Zones, each Zone's Aisles, each Aisle's LocationSlots, assembled into a shape a UI can render as a floor plan or grid. | ⚠ `usecases.GetSiteLayout` → `usecases.SiteLayout`; the grid is `usecases.ZoneGrid` |
| **Functional location** | A slot whose role is not `Storage` — a dock door, yard spot, work center, drop, staging, QC, consolidation or shipping location. | ⚠ no type: `usecases.ListLocationsByRole`, MCP `list_functional_locations` |
| **Bulk import** | Loading a building's layout from rows in one call, partial success reported per row. | `usecases.ImportFacilityLayout`, `ImportRow`, `ImportReport` |

## Value objects and enumerations

| Term | Values / shape | Notes | Code identifier |
|---|---|---|---|
| **LocationCode** | `Site-Area-Zone-Aisle-Bay-Level-Position` | Each segment non-empty and `[A-Z0-9]` only. Always round-trips through `String()` / `ParseLocationCode()`. | `shared.LocationCode` |
| **Capacity** | `maxWeightKg`, `maxVolumeM3` | Both must be strictly positive when set. Required for the `Storage`, `Staging`, `Drop` and `Consolidation` roles; optional (may be absent) for the others. | `shared.Capacity`, `LocationRole.RequiresCapacity` |
| **DockFlow** | `Inbound`, `Outbound`, `Both` | Required on a `Dock` slot; not allowed on any other role. | `slot.DockFlow` |
| **Activity** | `Pack`, `Sort`, `QC`, `VAS`, `Deconsolidate`, `Receive`, `Kit` | At least one required on a `WorkCenter` slot; not allowed on any other role. | `slot.Activity` (⚠ `QC` is `slot.ActivityQC`) |
| **Functional attributes** | a dock flow or an activity set | The role-conditional part of a slot. | `slot.FunctionalAttributes` |
| **Geometry** | position `xM/yM/zM`, dimensions `widthM/depthM/heightM`, optional `pickSequence` | Optional physical placement of a slot, in metres. An aisle's geometry is its centreline (start/end point). | ⚠ no `Geometry` type: `shared.Point3D`, `shared.Dimensions`, `shared.Segment` (centreline), `shared.Rect` (structure footprint) |
| **Pitch** | `bayPitchM`, `levelPitchM` | A zone's fallback spacing when geometry is missing; defaults 1.2 m and 1.5 m. | `zone.Zone.SetPitch`, `travel.Pitch` |
| **TemperatureClass** | `Ambient`, `Chilled`, `Frozen` | A Zone attribute; a PlacementRule predicate can match on it. | `shared.TemperatureClass` |
| **Direction** | `OneWay`, `TwoWay` | An Aisle attribute; an input to travel-path planning. | `shared.Direction` |
| **Status** | `Active`, `UnderMaintenance`, `Decommissioned` | Shared by Site, Zone, Aisle and LocationSlot. CrossAisle has its own two-value status (`Active`, `Decommissioned`). | `shared.Status` |
| **Effect** | `Allow`, `Deny` | What a PlacementRule does when its predicate matches. | `placement.Effect` |
| **ZonePredicate** | any of `zoneCode`, `temperatureClass`, `hazmat` | Every set field must match (AND); unset fields are wildcards; at least one must be set. | `placement.ZonePredicate` |
| **SequenceHint** | non-negative integer | An Aisle's walk-order position. | `Aisle.SequenceHint()` |

## The code segments

| Segment | Meaning | Real examples |
|---|---|---|
| Site | the physical facility/building | `WH1` |
| Area | coarse functional area | `STOR` (storage), `RCV` (receiving), `PACK`, `STAGE` |
| Zone | behavioral class *within* an area — drives rules | `AMB` (ambient), `CHL` (chilled), `FRZ` (frozen), `HAZ` (hazmat), `FWD` (forward-pick), `RSV` (reserve) |
| Aisle | physical corridor | `A07` |
| Bay | a bay/section along the aisle | `03` |
| Level | vertical level/shelf | `02` |
| Position | left-to-right slot on that level | `B` |

## Lifecycle vocabulary

| Term | Meaning |
|---|---|
| **Active** | The structure exists and is legal for storage/traversal. |
| **UnderMaintenance** | The structure exists but is temporarily out of service. A legal persisted state — e.g. loaded from an external facility-management system — that the read models render. v1 exposes no use case that *sets* it, but a slot in it can still be decommissioned. |
| **Decommissioned** | Permanently retired. **One-way** in v1: there is no reactivation use case, and re-registering a decommissioned LocationCode is rejected as a duplicate rather than quietly resurrecting the slot. See [ADR 0005](../adr/0005-one-way-decommission.md). |
| **Chain of custody** | The Site → Zone → Aisle resolution performed before a LocationSlot is allowed to exist. Registering a slot is a chain-of-custody check, not a bare insert. Code: `RegisterLocationSlot.resolveChain`. |
| **Amnesty** | A real term from the domain reference: where a damaged or mismatched item is set aside during stow. Modelled here as a `LocationType`, because an amnesty position is a physical slot with a shape and a capacity like any other. |

## Words this context shares with others

| Word | Here it means | Elsewhere it means |
|---|---|---|
| **Zone** | A behavioral classification of physical space (temperature class + hazmat) scoped to a Site. This service is its **source of truth**. | In `wes-work-planning` and the WES ubiquitous language generally, `Zone` is a unit of congestion and travel-path reasoning. Same physical thing, consumed as a read-only fact. |
| **Location** | A coded slot's *structural identity and legality*. Nothing about contents. | In `inventory-storage`, a `Bin`/`Location` is about capacity **occupancy** and what stock sits there. |
| **Capacity** | The static envelope of a slot's shape: max weight, max volume. | In `inventory-storage`, capacity is dynamic and consumed — how much room is left right now. |
| **Classification** | The resolved `hazmat`/`temperatureClass` pair a LocationSlot's parent Zone carries, exposed at `GET /locations/{locationCode}/classification` for a cross-context caller to check compatibility. Always denormalized from Zone; never separately stored. | In `product-master`, classification is a **SKU-level** handling profile: the tag set (Hazmat/Fragile/TemperatureSensitive/Oversized/HighValue), a `TemperatureClass` and an optional DOT hazard class, published as `ProductClassified` on `warehouse.product-master.events`. Same word, different subject — this context classifies *space*, `product-master` classifies *product*. `inventory-storage` no longer owns product classification (its ADR 0034); it keeps a local copy fed by `product-master`'s events and still runs the stow-time placement check where the two meet: a Hazmat SKU may only be placed in a hazmat-rated Zone, and a TemperatureSensitive SKU only in a Zone whose TemperatureClass matches. The Zone half of that check is exactly what this endpoint (or the Kafka-fed cache built from this context's events) exists to answer cheaply. `facility-layout` itself has no relationship with `product-master`. |

This context's endpoints are its **Open Host Service**, and the JSON shapes
they return (`Zone`, `LocationSlot`, `LocationClassification`, and the rest
of `apis/openapi.yaml`) are its **Published Language** — see the
[Bounded Context Canvas](../ddd/bounded-context-canvas.md). `GET
/locations/{locationCode}/classification` is a concrete instance of that:
rather than `inventory-storage` re-deriving or duplicating Zone's
`Hazmat`/`TemperatureClass` fields, it reads them here, denormalized to
exactly the shape a stow-time placement check needs. See [ADR
0008](../adr/0008-location-classification-read-endpoint.md).

## Words this context deliberately does **not** use

`StockUnit`, `Reservation`, `Usable inventory`, `Task`, `Assignment`, `Wave`,
`Pick`, `Pack`, `SLAM`, `Associate`, `Shift`.

One deliberate overlap: `Pack` (and `Sort`, `QC`, `Receive`, `Kit`) **do**
appear as `slot.Activity` values — they name what a `WorkCenter` *location*
is equipped for, never a pack task or its progress.

None of those are physical structure. If a term from that list ever appears
in this service's domain layer, the boundary has leaked and the service has
started to duplicate a neighbour.

## Name mismatches between language and code

| Spoken term | Code name | Why |
|---|---|---|
| LocationRole `Staging` | `placement.RoleStaging` (value `"Staging"`) | `placement.Staging` is already the well-known LocationType **name** constant. |
| Activity `QC` | `slot.ActivityQC` (value `"QC"`) | `placement.QC` is already the LocationRole constant. |
| Facility layout | `usecases.SiteLayout` from `GetSiteLayout` | The read model is built per site. |
| Geometry | `shared.Point3D`, `Dimensions`, `Segment`, `Rect` | Four value objects, no umbrella type. |
| Functional location | no type — a `LocationSlot` whose `Role()` is not `Storage` | Role is data on the slot, not a subtype. |
| Classification | no type — `usecases.GetLocationClassification` | Always derived from the slot's Zone, never stored. |
| Waypoint | `travel.Node` | Generic graph vocabulary in the pure-domain `travel` package. |

Source: `internal/domain/**`, `internal/application/usecases/**`. Part of
the [DDD artifact pack](../ddd/ddd-artifacts.md).
