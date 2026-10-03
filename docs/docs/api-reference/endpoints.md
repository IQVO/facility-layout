---
id: endpoints
title: Endpoint catalogue
sidebar_label: Endpoint catalogue
description: All 31 operations across 23 OpenAPI paths, grouped by OpenAPI tag and cross-checked against the chi router.
---

# Endpoint catalogue

Every route the chi router mounts, grouped by its OpenAPI tag. Click any
operation to reach the generated, interactive reference for it.

:::tip[Coverage]
All **31** router operations have a corresponding operation in
`apis/openapi.yaml` (23 `paths` entries). Two of them — the geometry `PUT`s —
are documented at a different path than the router mounts; see the
[coverage cross-check](#coverage-cross-check) at the bottom of this page.
:::

## Sites

Physical facilities/buildings. A Site is the root of the location hierarchy
and the first segment of every LocationCode inside it; no Zone, Aisle or
LocationSlot can exist without one.

| Method | Path | Operation | Success |
|---|---|---|---|
| `POST` | `/sites` | [Register a physical facility](./rest/register-site.api.mdx) | `201` + `Location` |
| `GET` | `/sites` | [List every registered site](./rest/list-sites.api.mdx) | `200` |
| `GET` | `/sites/{siteCode}` | [Get one site](./rest/get-site.api.mdx) | `200` |

## Zones

Behavioral classifications scoped to a Site (ambient, chilled, frozen,
hazmat, forward-pick, reserve), bundling a LocationCode's Area and Zone
segments. Their `TemperatureClass` and `Hazmat` flag are exactly what
PlacementRules match on.

| Method | Path | Operation | Success |
|---|---|---|---|
| `POST` | `/sites/{siteCode}/zones` | [Register a zone](./rest/register-zone.api.mdx) | `201` + `Location` |
| `GET` | `/sites/{siteCode}/zones` | [List a site's zones](./rest/list-zones.api.mdx) | `200` |
| `GET` | `/zones/{zoneId}` | [Get one zone](./rest/get-zone.api.mdx) | `200` |

The zone identifier in the path is the composite `Site-Area-Zone`, e.g.
`WH1-STOR-AMB`.

## Aisles

Physical corridors scoped to a Zone. An Aisle carries a `SequenceHint` — its
walk-order position, the concrete travel-distance input WES-tier planning
needs — and a `Direction`.

| Method | Path | Operation | Success |
|---|---|---|---|
| `POST` | `/zones/{zoneId}/aisles` | [Register an aisle](./rest/register-aisle.api.mdx) | `201` + `Location` |
| `GET` | `/zones/{zoneId}/aisles` | [List a zone's aisles, in walk order](./rest/list-aisles.api.mdx) | `200` |
| `GET` | `/zones/{zoneId}/aisles/{aisleCode}` | [Get one aisle](./rest/get-aisle.api.mdx) | `200` |
| `PUT` | `/zones/{zoneId}/aisles/{aisleCode}/geometry` | [Set an aisle's travel centreline](./rest/set-aisle-geometry.api.mdx) | `200` |
| `POST` | `/zones/{zoneId}/cross-aisles` | [Register a connection between two aisles](./rest/register-cross-aisle.api.mdx) | `201` (no `Location`) |

`GET /zones/{zoneId}/aisles` returns aisles ordered by `sequenceHint`, not by
registration order. That ordering *is* the walk order. The centreline and
cross-aisle operations feed the travel graph
([ADR 0017](../adr/0017-geometry-and-travel-graph.md)).

## Location Types

Reusable classifications of physical slot shape/kind — `PalletRack`, `Shelf`,
`ToteWall`, `BulkFloor`, `Staging`, `Amnesty` — each carrying the default
capacity envelope its slots inherit and a functional `role` (`Storage` by
default; also `Dock`, `Yard`, `WorkCenter`, `Drop`, `Staging`, `QC`,
`Consolidation`, `Shipping` — see
[ADR 0016](../adr/0016-functional-location-roles.md)).

| Method | Path | Operation | Success |
|---|---|---|---|
| `POST` | `/location-types` | [Register a location type](./rest/register-location-type.api.mdx) | `201` + `Location` |
| `GET` | `/location-types` | [List location types](./rest/list-location-types.api.mdx) | `200` |
| `GET` | `/location-types/{name}` | [Get one location type](./rest/get-location-type.api.mdx) | `200` |

## Placement Rules

Declarations of which LocationTypes are legal in which Zones. Deny always
wins; declaring any Allow rule for a zone turns that zone into an allow-list.

| Method | Path | Operation | Success |
|---|---|---|---|
| `POST` | `/placement-rules` | [Define a placement rule](./rest/define-placement-rule.api.mdx) | `201` + `Location` |
| `GET` | `/placement-rules` | [List placement rules](./rest/list-placement-rules.api.mdx) | `200` |
| `GET` | `/placement-rules/{ruleId}` | [Get one placement rule](./rest/get-placement-rule.api.mdx) | `200` |

## Locations

The coded leaf slots themselves — registering, reading, decommissioning, and
bulk-importing a whole building's layout from a structured export. Also
exposes a slot's resolved hazmat/temperature-class classification, the
concrete realization of this context's Open Host Service / Published
Language for cross-context placement validation (see [ADR
0008](../adr/0008-location-classification-read-endpoint.md)).

| Method | Path | Operation | Success |
|---|---|---|---|
| `POST` | `/locations` | [Register a location slot](./rest/register-location-slot.api.mdx) | `201` + `Location` |
| `GET` | `/locations/{locationCode}` | [Get one slot](./rest/get-location-slot.api.mdx) | `200` |
| `GET` | `/locations/{locationCode}/classification` | [Get a slot's resolved hazmat/temperature classification](./rest/get-location-classification.api.mdx) | `200` |
| `POST` | `/locations/{locationCode}/decommission` | [Decommission a slot](./rest/decommission-location-slot.api.mdx) | `204` |
| `POST` | `/locations/import` | [Import a facility layout](./rest/import-facility-layout.api.mdx) | `200` (partial-success report) |
| `PUT` | `/locations/{locationCode}/geometry` | [Set a slot's physical geometry](./rest/set-location-geometry.api.mdx) | `200` |

`POST /locations` is the endpoint where the whole
[chain-of-custody invariant](../ddd/invariants.md) and every applicable
[PlacementRule](../business-context/domain-vision.md#placementrules-the-enforcement-point)
are enforced. See [Bulk import](./bulk-import.md) for the import report
shape.

`POST /locations` — along with every other true resource-creation `POST`
in this catalogue except `/locations/import` and the decommission action
below — requires a caller-supplied `Idempotency-Key` header when the
service runs with `DATABASE_URL` set (ADR-0019); see
[Conventions](./conventions.md#idempotency-key-on-resource-creation-posts).

## Layout — "draw the warehouse"

The readable, drawable projections. These are assembled across the aggregates
on read, never separately stored state.

| Method | Path | Operation | Returns |
|---|---|---|---|
| `GET` | `/sites/{siteCode}/layout` | [Get a site's full layout](./rest/get-site-layout.api.mdx) | Nested zones → aisles → slots, pre-ordered |
| `GET` | `/sites/{siteCode}/layout?format=svg` | same operation, `format` query parameter | `image/svg+xml` floor plan |
| `GET` | `/zones/{zoneId}/grid` | [Get a zone's 2D grid](./rest/get-zone-grid.api.mdx) | Explicit matrix: rows = Level, columns = (Aisle, Bay) in walk order |
| `GET` | `/sites/{siteCode}/locations?role=` | [List a site's locations by role](./rest/list-locations-by-role.api.mdx) | Flat list of slots with the requested `LocationRole` (e.g. every `Dock` door) |
| `POST` | `/sites/{siteCode}/structures` | [Register a fixed structure](./rest/register-fixed-structure.api.mdx) | `201` + `Location` — a wall, column, office, conveyor or other obstacle |
| `GET` | `/sites/{siteCode}/structures` | [List a site's fixed structures](./rest/list-fixed-structures.api.mdx) | `200` |
| `GET` | `/zones/{zoneId}/travel-graph` | [Get a zone's travel graph](./rest/get-zone-travel-graph.api.mdx) | Nodes (aisle/bay waypoints) and directed, metre-weighted edges |
| `GET` | `/distance?from=&to=` | [Estimate travel distance](./rest/estimate-travel-distance.api.mdx) | `{metresM, estimated, route}` between two slots in the same zone |

Real output for the layout and grid is on
[Drawing the warehouse](./drawing-the-warehouse.md). `/distance` reports map
topology only — never travel time or congestion — and refuses rather than
guesses when the two locations are in different zones.

## Health

| Method | Path | Operation | Returns |
|---|---|---|---|
| `GET` | `/healthz` | [Liveness probe](./rest/get-healthz.api.mdx) | `200` |
| `GET` | `/readyz` | Readiness probe (not in `apis/openapi.yaml` — see below) | `200`, `503` once draining |

## Coverage cross-check

The router in `internal/adapters/inbound/http/server.go` mounts exactly these
routes:

```go
r.Get("/healthz", s.handleHealthz)
r.Get("/readyz", HandleReadyz(s.Readiness))

r.Route("/sites", func(r chi.Router) {
    r.Post("/", s.handleRegisterSite)
    r.Get("/", s.handleListSites)
    r.Get("/{siteCode}", s.handleGetSite)
    r.Get("/{siteCode}/layout", s.handleGetSiteLayout)
    r.Get("/{siteCode}/locations", s.handleListLocationsByRole)
    r.Post("/{siteCode}/zones", s.handleRegisterZone)
    r.Get("/{siteCode}/zones", s.handleListZones)
    r.Post("/{siteCode}/structures", s.handleRegisterFixedStructure)
    r.Get("/{siteCode}/structures", s.handleListFixedStructures)
})

r.Route("/zones", func(r chi.Router) {
    r.Get("/{zoneId}", s.handleGetZone)
    r.Get("/{zoneId}/grid", s.handleGetZoneGrid)
    r.Post("/{zoneId}/aisles", s.handleRegisterAisle)
    r.Get("/{zoneId}/aisles", s.handleListAisles)
    r.Get("/{zoneId}/aisles/{aisleCode}", s.handleGetAisle)
    r.Put("/{zoneId}/aisles/{aisleCode}/geometry", s.handleSetAisleGeometry)
    r.Post("/{zoneId}/cross-aisles", s.handleRegisterCrossAisle)
    r.Get("/{zoneId}/travel-graph", s.handleGetZoneTravelGraph)
})

r.Get("/distance", s.handleEstimateTravelDistance)

r.Route("/location-types", func(r chi.Router) {
    r.Post("/", s.handleRegisterLocationType)
    r.Get("/", s.handleListLocationTypes)
    r.Get("/{name}", s.handleGetLocationType)
})

r.Route("/placement-rules", func(r chi.Router) {
    r.Post("/", s.handleDefinePlacementRule)
    r.Get("/", s.handleListPlacementRules)
    r.Get("/{ruleId}", s.handleGetPlacementRule)
})

r.Route("/locations", func(r chi.Router) {
    r.Post("/", s.handleRegisterLocationSlot)
    r.Post("/import", s.handleImportFacilityLayout)
    r.Get("/{locationCode}", s.handleGetLocationSlot)
    r.Get("/{locationCode}/classification", s.handleGetLocationClassification)
    r.Post("/{locationCode}/decommission", s.handleDecommissionLocationSlot)
    r.Put("/{locationCode}/geometry", s.handleSetLocationGeometry)
})
```

That is **31 operations**, every one of which has an operation in
`apis/openapi.yaml`, grouped under **23 OpenAPI paths**:

| Tag | Paths | Operations |
|---|---:|---:|
| Sites | 2 | 3 |
| Zones | 2 | 3 |
| Aisles | 3 | 5 |
| Location Types | 2 | 3 |
| Placement Rules | 2 | 3 |
| Locations | 5 | 6 |
| Layout | 6 | 7 |
| Health | 1 | 1 |
| **Total** | **23** | **31** |

:::caution[Known spec-vs-router mismatch]
The two geometry operations (`setAisleGeometry`, `setLocationGeometry`) are
declared in `apis/openapi.yaml` as `PUT /zones/{zoneId}/aisles/{aisleCode}`
and `PUT /locations/{locationCode}`, but the router mounts them with a
trailing `/geometry` segment, as shown above and in the tables on this page.
The router — and its handler tests — are authoritative; the generated
reference pages for those two operations show the specification's path.
:::

:::info[A 32nd route with no OpenAPI operation]
`GET /readyz` (the Kubernetes readiness probe, ADR-0020) is mounted by the
router right next to `/healthz` but has no entry in `apis/openapi.yaml` and
is not part of the counts above — it is an orchestration concern, not a
published API operation, so it is intentionally outside the OpenAPI
contract and the generated reference.
:::
