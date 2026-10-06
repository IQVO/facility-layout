---
paths:
  - "internal/domain/**"
  - "internal/application/**"
  - "internal/adapters/inbound/mcp/**"
  - "migrations/*.sql"
---

# Domain model — location-code hierarchy, ubiquitous language, invariants, events, use cases

## The location-code hierarchy (INDUSTRY STANDARD — use this exact shape)

Not a made-up scheme — the widely-used WMS industry pattern:
**Site → Area → Zone → Aisle → Bay → Level → Position**, human-parsable and
hyphen-joined, e.g. `WH1-STOR-AMB-A07-03-02-B`
(Site=`WH1`, Area=`STOR`, Zone=`AMB`, Aisle=`A07`, Bay=`03`, Level=`02`,
Position=`B`). Segments read left→right, coarsest to finest:

| Segment  | Meaning                                                | Example |
|----------|---------------------------------------------------------|---------|
| Site     | the physical facility/building                          | `WH1`   |
| Area     | coarse functional area                                   | `STOR` (storage), `RCV` (receiving), `PACK`, `STAGE` |
| Zone     | behavioral class *within* an area — drives rules         | `AMB` (ambient), `CHL` (chilled), `FRZ` (frozen), `HAZ` (hazmat), `FWD` (forward-pick), `RSV` (reserve) |
| Aisle    | physical corridor                                        | `A07`   |
| Bay      | a bay/section along the aisle                            | `03`    |
| Level    | vertical level/shelf                                     | `02`    |
| Position | left-to-right slot on that level                         | `B`     |

`LocationCode` is a **value object**, not a free-text string: built from
these seven typed segments and always round-trips through
`String()`/`ParseLocationCode()`. Reject construction if any segment is
empty or contains characters other than `[A-Z0-9]`.

## Ubiquitous Language (use these exact names)

- **Site** — a physical facility/building. Root of the hierarchy. Has a
  `SiteCode` and a human name.
- **Zone** — a behavioral classification scoped to a Site (bundles the
  Area+Zone segments above into one aggregate). Carries `TemperatureClass`
  (Ambient/Chilled/Frozen) and a `Hazmat` flag; every `PlacementRule` is
  keyed by Zone. This is the SAME `Zone` word `warehouse-systems-ddd.md`
  already uses for WES's congestion/travel-path reasoning — this service is
  its source of truth.
- **Aisle** — a physical corridor scoped to a Zone. Carries a
  `SequenceHint` (walk-order position for travel-path optimization — the
  concrete answer to WES's travel-distance input) and a `Direction`
  (one-way or two-way).
- **LocationType** — a reusable classification of physical slot shape/kind
  (`PalletRack`, `Shelf`, `ToteWall`, `BulkFloor`, `Staging`, `Amnesty` —
  `amnesty` is a real domain-reference term: where a damaged/mismatched
  item is set aside during stow). Carries a `LocationRole` and a default
  capacity envelope (max weight, max volume).
- **LocationRole** (ADR-0016) — what a location is *for*: `Storage`
  (default), `Dock`, `Yard`, `WorkCenter`, `Drop`, `Staging`, `QC`,
  `Consolidation`, `Shipping`. Only `Storage`/`Staging`/`Drop`/
  `Consolidation` require a capacity envelope. A `Dock` slot must carry a
  `dockFlow` (`Inbound`/`Outbound`/`Both`); a `WorkCenter` slot must carry
  at least one activity (`Pack`, `Sort`, `QC`, `VAS`, `Deconsolidate`,
  `Receive`, `Kit`); no other role may carry either.
  `GET /sites/{siteCode}/locations?role=` lists a site's slots by role.
- **LocationSlot** — the leaf aggregate: one coded physical slot. Its
  identity IS its `LocationCode`. Has a `LocationType`, a capacity envelope
  (can override the type's default), and a `Status`
  (`Active`/`Decommissioned`/`UnderMaintenance`), plus optional geometry
  (position, dimensions, pick sequence — ADR-0017). `inventory-storage`
  validates stows against a Kafka-fed local cache of these slots.
- **CrossAisle / FixedStructure / travel graph** (ADR-0017) — a walkable
  connection between two aisles of a zone at one bay; a wall/column/office/
  conveyor footprint on a site; and the pure-domain graph of a zone's
  aisle/bay waypoints behind `GET /zones/{zoneId}/travel-graph` and
  `GET /distance`.
- **PlacementRule** — declares which `LocationType`s are legal in which
  `Zone` (e.g. only a `PalletRack` LocationType may be placed in a `HAZ`
  zone if the rule set says so; a `Frozen`-temperature-class zone rejects a
  LocationType not rated for cold). This is the mechanism that prevents
  "ambient product in the frozen zone" — enforced once, at registration
  time, not re-checked by every caller.
- **Facility layout** — the readable, "drawable" projection of the whole
  structure: a Site's Zones, each Zone's Aisles, each Aisle's
  LocationSlots, assembled into a shape a UI/operator can render as a
  floor plan or grid.
- **Location classification (read)** —
  `GET /locations/{locationCode}/classification` resolves a slot to its
  Zone and returns the Zone's `Hazmat`/`TemperatureClass` attributes as a
  cheap, denormalized read. This is the concrete Published Language
  realization of this context's Open Host Service role: `inventory-storage`
  consumes it synchronously at stow time to enforce hazmat/temperature
  placement rules on classified SKUs, without duplicating Zone data
  (ADR-0008).

## Aggregates & invariants (enforce in domain, unit-tested)

- **Site**: `SiteCode` must be non-empty, uppercase alphanumeric, unique.
  Cannot register a `Zone` against an unknown or `Decommissioned` Site.
- **Zone**: scoped to exactly one Site; `AreaCode` + `ZoneCode` together
  form the LocationCode's second and third segments. Cannot register an
  `Aisle` against an unknown or `Decommissioned` Zone.
- **Aisle**: scoped to exactly one Zone. Cannot register a `LocationSlot`
  whose LocationCode's Site/Area/Zone/Aisle segments don't resolve to an
  existing, `Active` Aisle (and transitively Zone and Site) — **this is the
  core invariant of the whole service**: no orphan slots, ever. Registering
  a slot is a chain-of-custody check, not a bare insert.
- **LocationSlot**: `LocationCode` is globally unique (it IS the identity).
  A slot's `LocationType` must satisfy every `PlacementRule` that applies
  to its Zone, checked at registration time — reject with a clear domain
  error naming the violated rule if not. Capacity envelope (weight/volume)
  must be positive when the type's role requires capacity. A `Decommissioned` slot cannot be re-activated by
  re-registering the same code — one-way decommission (ADR-0005).
- **PlacementRule**: references an existing `LocationType` and either a
  specific `Zone` or a `TemperatureClass`/`Hazmat` predicate; cannot
  reference a `LocationType` that does not exist.
- Read models (a Site's full layout, a Zone's grid) are PROJECTIONS built
  by querying across these aggregates via the repositories — not
  separately stored state.

## Domain events (past tense)

SiteRegistered, ZoneRegistered, AisleRegistered, LocationTypeRegistered,
PlacementRuleDefined, LocationSlotRegistered, LocationSlotDecommissioned,
FacilityLayoutImported (emitted once per bulk import call, carrying a
count of slots imported — individual `LocationSlotRegistered` events also
fire per-slot within that same import), and the ADR-0017 geometry events
LocationGeometryUpdated, AisleGeometryUpdated, FixedStructureRegistered,
CrossAisleRegistered — twelve in all, published to
`warehouse.facility.events` (`apis/asyncapi.yaml`) when
`EVENT_PUBLISHER=kafka`.

CloudEvents `type` convention (IDENTICAL to the other fleet
services — reverse-DNS, lowercase except the final PascalCase event name,
entity segment has NO hyphen even for multi-word aggregate names, matching
e.g. workforce-management's `shiftplan`):

```
com.warehouse.<subdomain>.facility-layout.<entity>.<EventName>
```

This service's **subdomain segment is `wms`** — "bin-accurate location" is
already classified WMS-tier in `amazon-fulfillment-ddd.md` ("Inventory &
Slotting" Core subdomain references bin-accurate location as WMS's Open
Host Service), and this service is the generalized, multi-consumer version
of that concern. Entity segments: `site`, `zone`, `aisle`, `locationtype`,
`placementrule`, `locationslot` (also used by `FacilityLayoutImported` and
`LocationGeometryUpdated`), `structure`, `crossaisle`. Examples:

```
com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered
com.warehouse.wms.facility-layout.zone.ZoneRegistered
com.warehouse.wms.facility-layout.placementrule.PlacementRuleDefined
```

On the wire this `type` is the `type` attribute of a mandatory CloudEvents
1.0 event (structured mode, `source=/warehouse/facility-layout`,
`subject`=aggregate id, `dataschema=urn:warehouse:facility-layout:<events|analytics>:<EventName>:v1`,
`data`=the event's own JSON) — ADR-0024. No flat envelope ever.

## Use cases (application layer)

1. RegisterSite(siteCode, name) -> Site
2. RegisterZone(siteCode, areaCode, zoneCode, temperatureClass, hazmat) -> Zone
3. RegisterAisle(zoneRef, aisleCode, sequenceHint, direction) -> Aisle
4. RegisterLocationType(name, role?, defaultCapacity) -> LocationType
5. DefinePlacementRule(locationType, zonePredicate) -> PlacementRule
6. RegisterLocationSlot(locationCode, locationType, capacityOverride?,
   dockFlow?, activities?) -> LocationSlot (validates the full
   chain-of-custody + placement rules + functional attributes)
7. DecommissionLocationSlot(locationCode) -> marks Decommissioned
8. ImportFacilityLayout(rows[]) -> bulk-registers sites/zones/aisles/slots
   from a structured list in one call, atomic per-row validation, partial
   success reported per row (bootstrap mechanism for loading a real
   building's layout from a CSV/JSON export)
9. GetSiteLayout(siteCode) -> the full nested, drawable structure (read model)
10. GetZoneGrid(zoneRef) -> a 2D grid (aisle x bay x level) of that zone's
    slots, shaped for direct UI rendering (read model)
11. ListLocationsByRole(siteCode, role) -> a site's slots with one role
    (read model, ADR-0016)
12. SetLocationGeometry / SetAisleGeometry / RegisterCrossAisle /
    RegisterFixedStructure -> the ADR-0017 geometry writes
13. GetZoneTravelGraph(zoneRef) / EstimateTravelDistance(from, to) -> the
    travel graph and shortest same-zone distance; across zones a
    straight-line estimate only when both slots have position geometry
    (read models, ADR-0017)

Plus single-resource/list reads (`GetSite`, `ListSites`, `GetZone`, …,
`GetLocationClassification`, `ListFixedStructures`) — 31 use-case structs
in `internal/application/usecases/`.
