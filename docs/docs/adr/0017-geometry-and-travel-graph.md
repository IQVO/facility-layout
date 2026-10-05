---
id: 0017-geometry-and-travel-graph
title: 17. Physical geometry and a travel-distance read model
sidebar_label: 17. Geometry & travel graph
sidebar_position: 17
description: "The warehouse map has no coordinates, no fixed structures, and no notion of distance between two coded locations, even though this service's own AGENTS.md names travel-path input to wes-work-planning as a reason it exists. This adds optional geometry to slots and aisles, a site-scoped FixedStructure aggregate, cross-aisles, and a pure-domain travel graph that estimates distance when geometry is missing rather than fabricating a false-precision number."
---

# 17. Physical geometry and a travel-distance read model

## Status

**Accepted.** Cross-zone estimation semantics, the zone-pitch API surface,
and the cross-aisle read model were amended in place by the 2026-10
ADR-conformance pass; the to-scale SVG redraw is recorded below as an
accepted deferral.

## Context

`Site`, `Zone`, `Aisle`, and `LocationSlot` today carry no physical
coordinates. `GetSiteLayout` and `GetZoneGrid` are ordinal projections —
they nest and order aggregates by their identifiers and `Aisle.SequenceHint`
(itself only "walk-order position", not a distance) — not a spatial model.
The `/sites/{siteCode}/layout?format=svg` endpoint therefore draws a grid
derived from code ordering, not a floor plan. There is nothing in the
domain that can answer "how far is it from slot A to slot B", and no
notion of a wall, column, or office that a real building has and a
warehouse map should be able to render.

This is a real gap for a stated reason to exist: this service's own
`AGENTS.md` says `wes-work-planning` and `fulfillment-execution` need
"zone/aisle adjacency for travel-path and congestion reasoning" from this
context. Today there is no adjacency and no distance for them to consume —
that input simply does not exist yet, three services and one ADR-0009
Published Language later.

What real WMS products store and why, checked directly against vendor
documentation:

- **SAP EWM** attaches `Geo-coordinates of storage bin` to every storage
  bin, described as being "used by SAP EWM to compute distances between the
  bins in goods movements", and its Graphical Warehouse Layout "requires
  that information on geographical coordinates (X, Y and Z) and the storage
  bin type are properly maintained... Fix structures, such as walls,
  offices, etc. are objects defined in EWM customizing." SAP also defines
  an `Activity Area` that groups bins for activity-specific sorting
  ("putaway, picking, physical inventory... the sequence of warehouse tasks
  ... optimized according to the assignment of storage bins to an activity
  area").
- **Oracle WMS Cloud** carries `Length/Width/Height` on the location record
  (consulted during putaway's "Most/Least Empty by Volume" search and as a
  replenishment trigger) and an alphanumeric `Pick Sequence` used purely to
  order picks — again a fact the location carries, not a computed
  optimization.
- **Blue Yonder**'s Advanced Slotting product literature explicitly frames
  its optimization as consuming "demand signals and travel paths" — i.e.
  travel distance is an *input* to a decision made elsewhere, not something
  the slotting product itself derives from raw bin coordinates.

All three treat geometry/distance as a fact the location-master service
publishes, and travel optimization/congestion/routing as a separate
concern downstream. That split matches this service's own Generic-
subdomain boundary: it should keep publishing facts, never make a routing
or slotting decision.

## Decision

Add optional physical geometry, entirely backward compatible with every
existing row (all new columns nullable, all new domain fields zero-value
by default and treated as "not supplied"):

- `LocationSlot` gains an optional `Point3D` (`xM, yM, zM`, metres),
  `Dimensions` (`widthM, depthM, heightM`), and an optional explicit
  `PickSequence` override (falls back to derivation from
  `Aisle.SequenceHint` + bay/level/position ordering when unset).
- `Aisle` gains an optional centreline `Segment` (start/end `Point3D`) —
  the walkable path a resource travels along that aisle.
- `Zone` gains optional `bayPitchM`/`levelPitchM` — the estimated spacing
  between bays/levels, used only as a fallback when a zone has no real
  geometry recorded yet. Both are settable at zone registration
  (`bayPitchM`/`levelPitchM` on `POST /sites/{siteCode}/zones`; supplying
  exactly one of the two is a 422 — a half-specified pitch would silently
  mix an override with a default) and always readable as the *effective*
  value (explicit override, else the service default) on every zone
  response, so a consumer never has to know the defaults to reason about
  an estimated distance.
- A new site-scoped aggregate, `FixedStructure` (`kind`: `Wall | Column |
  Office | Conveyor | Other`, a rectangular footprint, a label), mirrors
  SAP's "fix structures... walls, offices" as first-class objects on the
  map rather than gaps in the storage grid.
- A new `CrossAisle` (zone-scoped: `fromAisle`, `toAisle`, `atBay`)
  records a physical connection between two aisles, the missing input for
  any route that isn't a straight walk down one aisle.

On top of these facts, a **pure domain service**
(`internal/domain/travel`, no repository or adapter imports) computes
shortest-path distance between two `LocationCode`s over a graph of aisle
centrelines and cross-aisles, honouring `Aisle.Direction` (a `OneWay`
aisle is a directed edge). When real geometry exists for the aisles
involved, distance is computed from actual centreline length and slot
position. When it does not, the graph falls back to the zone's
`bayPitchM`/`levelPitchM` and the response is explicitly flagged
`estimated: true` — the service returns a labelled estimate rather than
silently reporting a false-precision number computed from data that was
never actually measured. A pair of locations in two different zones is
answered with the straight-line (beeline) distance between the two
recorded positions — flagged `estimated: true` — when BOTH carry real
position geometry, honouring the ADR's original "refuse where neither
zone has geometry" rule at the level it was written: geometry, not zone
identity, is what makes an estimate honest. When either endpoint has no
recorded geometry the request still returns an explicit
`ErrNoRouteBetweenZones` (422) rather than an invented cross-zone
estimate; this is a conscious decision the user should revisit if
day-one estimated whole-site distance turns out to matter more than the
honesty of refusing to guess across a zone boundary.

New reads: `PUT /locations/{locationCode}/geometry`, `PUT
/zones/{zoneId}/aisles/{aisleCode}/geometry`, `POST`/`GET
/sites/{siteCode}/structures`, `POST`/`GET
/zones/{zoneId}/cross-aisles`, `GET /zones/{zoneId}/travel-graph`, and
`GET /distance?from=&to=` returning `{metresM, estimated, route}`. MCP
gains `estimate_travel_distance` and `get_zone_travel_graph` as curated,
read-only tools (ADR-0007's governance charter). The SVG renderer draws
zones with real geometry to scale, including fixed structures and aisle
direction arrows; zones without geometry keep today's ordinal grid.

New events: `LocationGeometryUpdated`, `AisleGeometryUpdated`,
`FixedStructureRegistered`, `CrossAisleRegistered` — all new event types
(not new fields on an existing one), so ADR-0013's compatibility contract
is untouched by this ADR; a consumer that doesn't care about geometry
does not need to change anything.

## Consequences

### Easier

- `wes-work-planning` and `fulfillment-execution` finally have a concrete
  travel-distance fact to consume, closing the gap this service's own
  `AGENTS.md` names as a reason for its existence — as a future,
  separately-decided integration (`TravelDistanceLookup` outbound port,
  mirroring the existing `LOCATION_LOOKUP_MODE` HTTP/permissive pattern).
- The SVG floor plan becomes an actual floor plan once a zone has real
  geometry, instead of a code-ordered grid standing in for one.
- `labor-performance`'s engineered standards gain a path to a real travel
  component instead of a constant, again as a future consumer decision,
  not something this ADR does itself.

### Harder / accepted

- **Two geometry regimes coexist.** A zone with real coordinates gets
  exact distances; a zone without them gets a pitch-based estimate. Every
  consumer of `GET /distance` must read and respect the `estimated` flag
  rather than treating every response as equally precise — this is a
  deliberate honesty tradeoff, not an oversight, but it is a real caller
  obligation.
- **Cross-zone distance without geometry is refused, not estimated**,
  which means a freshly bulk-imported site with no geometry recorded
  anywhere cannot answer any cross-zone distance query on day one. This
  was chosen over inventing a whole-site pitch fallback because a wrong
  number silently used in a WES travel-time calculation is worse than a
  clear 422; it is flagged here as the one design point in this ADR most
  likely to be revisited if it blocks real usage. (Where both endpoints
  do carry position geometry, the beeline amendment above already
  answers the query.)
- Bulk import (ADR-0006) grows six more optional columns (`x`, `y`, `z`,
  `widthM`, `depthM`, `heightM`) plus `pickSequence`; existing import
  files are unaffected.
- The domain travel graph must be re-verified against the analytics
  projector (`cmd/facility-projector`) and any other strict event-type
  consumer before four brand-new event types reach `warehouse.facility.
  events` in production, the same way ADR-0013 had to record a real chart
  bug that had silently prevented event publication entirely — "the code
  compiles and the projector is idempotent" is not evidence a new event
  type doesn't break something that assumes a closed set of types.
- Explicitly out of scope, to keep this Generic subdomain generic: travel
  *time* (as opposed to distance), congestion, and route choice under load
  remain `wes-work-planning`'s concern; slotting optimization that would
  use this distance data remains out of this service entirely.
- **Accepted deferral — to-scale SVG redraw.** The SVG floor plan renders
  zones with real geometry to scale today, but redrawing the no-geometry
  ordinal grid (and the per-role glyph layer ADR-0016 added) as a
  true-to-scale mixed floor plan is deferred: it needs a layout engine
  (collision, padding, label placement) that does not exist here, and no
  consumer has asked for it yet. The current renderer remains the
  contract: to-scale where geometry exists, ordinal grid otherwise,
  per-role monogram distinction on top. Revisit when a consumer needs
  printed floor plans.

## Related

- ADR-0002 — Industry-standard hierarchical location code (`LocationCode`
  is unchanged by this ADR; geometry is attached to it, not a replacement
  for it).
- ADR-0006 — Bulk import reports partial success per row (the import shape
  extended with optional geometry columns).
- ADR-0009 — Kafka integration publisher (the Published Language the four
  new event types are added to).
- ADR-0010 — Per-service analytical data product (the projector that must
  tolerate new event types).
- ADR-0013 — inventory-storage is the first real consumer of the Published
  Language (the compatibility bar this ADR's new event *types* — as
  opposed to ADR-0016's new event *fields* — must not break).
- ADR-0016 — Functional location roles beyond storage (the companion ADR;
  geometry and travel distance are what make a `Dock`, `WorkCenter`, or
  `Drop` location's position on the map actually useful to a consumer).
