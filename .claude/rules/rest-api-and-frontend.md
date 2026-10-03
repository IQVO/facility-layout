# REST API and frontend contract

## REST API (inbound adapter) — this is the "draw the warehouse" capability

Structural / write side (builds up the model). Every route marked **(idem)**
requires a caller-supplied `Idempotency-Key` header when `DATABASE_URL` is
set (ADR-0019; skipped entirely in in-memory/no-database runs):

- `POST   /sites`                                    -> RegisterSite (idem)
- `GET    /sites`                                    -> list sites
- `GET    /sites/{siteCode}`                          -> get one site
- `POST   /sites/{siteCode}/zones`                    -> RegisterZone (idem)
- `GET    /sites/{siteCode}/zones`                    -> list a site's zones
- `POST   /sites/{siteCode}/structures`                -> RegisterFixedStructure (idem)
- `GET    /sites/{siteCode}/structures`                -> list a site's fixed structures
- `POST   /zones/{zoneId}/aisles`                     -> RegisterAisle (idem)
- `GET    /zones/{zoneId}/aisles`                     -> list a zone's aisles
- `GET    /zones/{zoneId}`                             -> get one zone
- `GET    /zones/{zoneId}/aisles/{aisleCode}`          -> get one aisle
- `PUT    /zones/{zoneId}/aisles/{aisleCode}/geometry` -> SetAisleGeometry (ADR-0017)
- `POST   /zones/{zoneId}/cross-aisles`                -> RegisterCrossAisle (idem, ADR-0017)
- `POST   /location-types`                            -> RegisterLocationType (idem)
- `GET    /location-types`                            -> list location types
- `GET    /location-types/{name}`                      -> get one location type
- `POST   /placement-rules`                           -> DefinePlacementRule (idem)
- `GET    /placement-rules`                           -> list placement rules
- `GET    /placement-rules/{ruleId}`                   -> get one placement rule
- `POST   /locations`                                 -> RegisterLocationSlot (idem)
- `GET    /locations/{locationCode}`                  -> get one slot
- `PUT    /locations/{locationCode}/geometry`          -> SetLocationGeometry (ADR-0017)
- `GET    /locations/{locationCode}/classification`   -> Zone Hazmat/TemperatureClass
  (denormalized read; Published Language for placement-rule consumers like
  `inventory-storage` — see ADR-0008)
- `POST   /locations/{locationCode}/decommission`     -> DecommissionLocationSlot
  (NOT idempotency-protected: acts on an existing resource, not a creation)
- `POST   /locations/import`                          -> ImportFacilityLayout
  (bulk; NOT idempotency-protected — partial-success semantics don't fit the
  whole-request replay model, see ADR-0019; request body is a JSON array of
  rows, each row fully specifying a site/area/zone/aisle/bay/level/position +
  locationType — the WHOLE point of this endpoint is that a real warehouse's
  layout gets loaded here once, reproducibly, not typed in by hand
  slot-by-slot)

Read / "draw" side (the deliverable this service exists for — REST APIs
whose PURPOSE is to render the warehouse, not just CRUD):

- `GET  /sites/{siteCode}/layout`
  Returns the FULL nested structure for one site: zones -> aisles ->
  location slots, with every slot's coordinates, type, status. Shaped so a
  frontend can directly render a floor plan (group by zone, then aisle,
  then bay/level/position) without further client-side joining. The
  primary "draw the warehouse" endpoint.
- `GET  /zones/{zoneId}/grid`
  Returns ONE zone's slots as an explicit 2D grid: rows = Level, columns =
  (Aisle, Bay) pairs in aisle `SequenceHint` order, cell = slot
  status/type (or null for a gap). A UI can iterate over this directly and
  paint it as a warehouse map — no client-side layout math required.
- `GET  /sites/{siteCode}/layout?format=svg`
  STRETCH scope: same data as the JSON layout endpoint, rendered
  server-side as a minimal SVG floor plan (one colored rect per zone, one
  row of rects per aisle, color = zone temperature class / hazmat flag).
  Must be valid, viewable SVG (`<svg>...</svg>`,
  `Content-Type: image/svg+xml`). Keep it a thin adapter-only concern
  (render function in the HTTP layer only) — do NOT let SVG rendering leak
  into domain or application layers.
- `GET  /sites/{siteCode}/locations?role=`
  A site's slots filtered to one `LocationRole` (ADR-0016), e.g. every
  `Dock` door.
- `GET  /zones/{zoneId}/travel-graph` / `GET /distance?from=&to=`
  The zone's travel graph (aisle/bay waypoints, metre-weighted edges) and
  the shortest same-zone distance between two slots (ADR-0017). `/distance`
  reports map topology only — never travel time or congestion.
- `GET  /healthz` — liveness
- `GET  /readyz` — readiness; flipped not-ready as the first step of
  graceful shutdown (ADR-0020), not in `apis/openapi.yaml` (orchestration
  concern, not a published API operation)

## CORS

`go-chi/cors` middleware is enabled on every route, allowing
`CORS_ALLOWED_ORIGINS` (env, default
`http://localhost:5173,http://localhost:5186` — the `warehouse-console`
shell and this service's own `facility-mfe` remote). This service is not
part of the fleet's cross-service Order Lifecycle read model (see ADR-0002
in `warehouse-ops-agent`'s docs) — no order-lifecycle stage touches
facility structure — CORS here exists solely for `facility-mfe`.

## Frontend micro-frontend remote (`web/`)

This repo owns `web/`: `facility-mfe`, a Vite + React Module Federation
**remote** consumed by the separate `warehouse-console` shell repo. It is
a plain browser client of this service's own REST API above (site list,
drill into a site's nested zone/aisle/slot layout) — nothing in `web/`
talks to any other bounded context, and nothing in `internal/` knows
`web/` exists. `web/` has its own `package.json`, build, and dev server
(`:5186`); it does not participate in this repo's Go quality gate and is
not part of the Go module.

JSON DTOs live in the http adapter; never leak domain structs. Follow the
SAME REST maturity level (Richardson Level 2) and RFC 7807
(`application/problem+json`) error format the other fleet services already
use — replicate it exactly (resource nouns, correct verbs/status codes,
`Location` header on every 201, RFC 7807 problem details on every error).

## Auth status

There is currently **no auth layer** on REST or MCP endpoints (ADR-0014
introduced static-bearer auth; ADR-0015 fully reverted it, commit
`73d6068`, 2026-09-09). Do not assume `AUTH_MODE`/`API_READ_KEY`/
`API_READWRITE_KEY` env vars exist — they were removed. Re-adding auth
requires re-reading ADR-0015 first.
