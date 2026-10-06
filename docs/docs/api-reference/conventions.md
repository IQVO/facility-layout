---
id: conventions
title: Conventions
sidebar_label: Conventions
description: REST maturity level 2, RFC 7807 problem details, status-code semantics and Location headers.
---

# API conventions

This service was built to **Richardson maturity level 2** and **RFC 7807**
from the first commit, rather than migrating to them later as the four
sibling services did historically.

## Resource nouns, correct verbs

Every path is a resource collection or a member of one. Two verb-suffixed
action endpoints exist and both are deliberate:

- **`POST /locations/{locationCode}/decommission`** — a genuine domain
  command on a single slot, not a CRUD update. Forcing it into
  `PATCH {"status":"Decommissioned"}` would misrepresent a one-way state
  transition as a field assignment.
- **`POST /locations/import`** — a collection-level action scoped to the
  `/locations` resource, rather than dangling off an unscoped `/admin/...`
  path.

No unscoped RPC-style endpoint exists in this service.

## Every `201` carries a resolvable `Location`

```http
HTTP/1.1 201 Created
Content-Type: application/json
Location: /zones/WH1-STOR-AMB
```

Four single-resource `GET`s exist specifically so that every `Location`
header points at something that actually has a representation:

| Endpoint | Exists so that this `Location` resolves |
|---|---|
| `GET /zones/{zoneId}` | `POST /sites/{siteCode}/zones` |
| `GET /zones/{zoneId}/aisles/{aisleCode}` | `POST /zones/{zoneId}/aisles` |
| `GET /location-types/{name}` | `POST /location-types` |
| `GET /placement-rules/{ruleId}` | `POST /placement-rules` |

A `Location` with no `GET` behind it is not maturity level 2.

:::caution[Two exceptions in the current code]
The geometry-era create endpoints do not yet meet this rule.
`POST /sites/{siteCode}/structures` sets
`Location: /sites/{siteCode}/structures/{id}`, but no route serves a single
fixed structure (use `GET /sites/{siteCode}/structures`).
`POST /zones/{zoneId}/cross-aisles` returns `201` with no `Location` header.
:::

## Status codes

| Code | When |
|---|---|
| `200` | Successful read, or a bulk-import report |
| `201` | Resource created — always with `Location` |
| `204` | Successful action with no body (decommission) |
| `400` | Malformed input: bad JSON, a location code that is not seven `[A-Z0-9]` segments, a missing required identifier, a resource-creation POST missing its required `Idempotency-Key` header |
| `404` | The named site/zone/aisle/slot/type/rule does not exist |
| `409` | State conflict: a code already taken, a parent that exists but is not Active, a slot already decommissioned |
| `422` | Semantically invalid: non-positive capacity, unknown enum value, **PlacementRule violation**, an `Idempotency-Key` reused with a different request body |

The 409/422 split is the one worth internalising:

- **409** means *the world is in a state that forbids this* — the code is
  taken, or the parent zone has been decommissioned. Retrying the identical
  request will keep failing until something else changes.
- **422** means *this request is internally wrong* — a negative capacity, an
  unknown temperature class, a location type that is not legal in that zone.
  The caller has to change the request.

`POST /locations/import` answers **`200`**, not `201`: a bulk import is a
partial-success report over many rows, not the creation of one addressable
resource, so there is no single `Location` to hand back.

## Idempotency-Key on resource-creation POSTs

Every true resource-creation `POST` — `/sites`, `/sites/{siteCode}/zones`,
`/sites/{siteCode}/structures`, `/zones/{zoneId}/aisles`,
`/zones/{zoneId}/cross-aisles`, `/location-types`, `/placement-rules`,
`/locations` — requires a caller-supplied `Idempotency-Key` header
whenever the service is running with `DATABASE_URL` set (ADR-0019). The
middleware is Postgres-backed: an in-memory/no-database run skips the
check entirely. `POST /locations/import` (bulk, partial-success
semantics) and `POST /locations/{locationCode}/decommission` (acts on an
existing resource, not a creation) are deliberately excluded.

| Outcome | Status |
|---|---|
| Header missing on a protected route | `400` (`idempotency-key-required`) |
| Fresh key | the real response, cached verbatim |
| Same key, byte-identical request | the original cached response, replayed, handler not re-run |
| Same key, different request body | `422` (`idempotency-key-reused`) |

## RFC 7807 problem details

Every error response — from the first commit, with no bespoke
`{"error": "..."}` predecessor — is
`Content-Type: application/problem+json`:

```json
{
  "type": "https://errors.facility-layout.warehouse-systems.dev/site-not-found",
  "title": "Site not found",
  "status": 404,
  "detail": "site not found",
  "instance": "/sites/NOPE"
}
```

| Member | Meaning here |
|---|---|
| `type` | A stable URI identifying the error **category**. One per distinct category; it is an identifier, not a page that has to resolve. |
| `title` | A fixed human string for the category. Never varies for a given `type`. |
| `status` | Mirrors the HTTP status code. |
| `detail` | The dynamic, request-specific message — this is where the violated rule gets named. |
| `instance` | The request path. |

The mapping is a single table in the HTTP adapter
(`internal/adapters/inbound/http/errors.go`) keyed off typed domain errors
with `errors.Is`. The domain never knows an HTTP status code exists.

### The problem type catalogue

| `type` slug | Status | Title |
|---|---:|---|
| `site-not-found` | 404 | Site not found |
| `zone-not-found` | 404 | Zone not found |
| `aisle-not-found` | 404 | Aisle not found |
| `location-slot-not-found` | 404 | Location slot not found |
| `location-type-not-found` | 404 | Location type not found |
| `placement-rule-not-found` | 404 | Placement rule not found |
| `duplicate-site-code` | 409 | A site with this code already exists |
| `duplicate-zone` | 409 | A zone with this area and zone code already exists in this site |
| `duplicate-aisle` | 409 | An aisle with this code already exists in this zone |
| `duplicate-location-type` | 409 | A location type with this name already exists |
| `duplicate-placement-rule` | 409 | A placement rule with this id already exists |
| `duplicate-location-code` | 409 | A location slot with this code already exists |
| `duplicate-fixed-structure` | 409 | A fixed structure with this id already exists |
| `duplicate-cross-aisle` | 409 | A cross-aisle between these aisles at this bay already exists |
| `site-not-active` | 409 | Site is not active |
| `zone-not-active` | 409 | Zone is not active |
| `aisle-not-active` | 409 | Aisle is not active |
| `already-decommissioned` | 409 | This structure is already decommissioned |
| `placement-rule-violated` | 422 | Location type is not legal in this zone |
| `invalid-max-weight` | 422 | Capacity max weight must be greater than zero |
| `invalid-max-volume` | 422 | Capacity max volume must be greater than zero |
| `unknown-temperature-class` | 422 | Unknown temperature class |
| `unknown-direction` | 422 | Unknown aisle direction |
| `unknown-status` | 422 | Unknown lifecycle status |
| `unknown-placement-effect` | 422 | Unknown placement rule effect |
| `empty-zone-predicate` | 422 | Placement rule predicate constrains nothing |
| `unknown-location-role` | 422 | Unknown location role |
| `unknown-dock-flow` | 422 | Unknown dock flow |
| `unknown-activity` | 422 | Unknown work center activity |
| `dock-flow-required` | 422 | A Dock location requires a dockFlow |
| `work-center-activities-required` | 422 | A WorkCenter location requires at least one activity |
| `functional-attributes-not-allowed` | 422 | dockFlow and activities may only be set on a Dock or WorkCenter location |
| `negative-sequence-hint` | 422 | Aisle sequence hint must not be negative |
| `zone-mismatch` | 422 | Zone attributes do not match the location code's zone |
| `invalid-z` | 422 | Z coordinate must not be negative |
| `invalid-dimensions` | 422 | Width, depth, and height must all be greater than zero |
| `negative-pick-sequence` | 422 | Pick sequence must not be negative |
| `unknown-fixed-structure-kind` | 422 | Unknown fixed structure kind |
| `empty-fixed-structure-footprint` | 422 | Fixed structure requires a real footprint |
| `cross-aisle-same-aisle` | 422 | Cross-aisle must connect two distinct aisles |
| `cross-aisle-aisle-mismatch` | 422 | Cross-aisle aisles must both belong to the named zone |
| `no-route-between-zones` | 422 | No route: the two locations are in different zones |
| `no-route` | 422 | No route exists between these two waypoints |
| `unknown-travel-waypoint` | 422 | The travel graph has no waypoint for this aisle/bay |
| `invalid-pitch` | 422 | Bay pitch and level pitch must both be greater than zero |
| `malformed-request-body` | 400 | The request body is not valid JSON |
| `malformed-location-code` | 400 | Malformed location code |
| `invalid-site-code` | 400 | Invalid site code |
| `empty-site-name` | 400 | Site name must not be empty |
| `invalid-zone-code` | 400 | Invalid area or zone code |
| `invalid-aisle-code` | 400 | Invalid aisle code |
| `invalid-location-type` | 400 | Invalid location type |
| `empty-placement-rule-id` | 400 | Placement rule id must not be empty |
| `missing-location-code` | 400 | Location code is required |
| `empty-fixed-structure-id` | 400 | Fixed structure requires an id |
| `empty-fixed-structure-site-code` | 400 | Fixed structure must be scoped to a site code |
| `empty-fixed-structure-label` | 400 | Fixed structure requires a label |
| `empty-cross-aisle-zone-id` | 400 | Cross-aisle must be scoped to a zone id |
| `empty-cross-aisle-from-aisle` | 400 | Cross-aisle requires a from-aisle code |
| `empty-cross-aisle-to-aisle` | 400 | Cross-aisle requires a to-aisle code |
| `empty-cross-aisle-bay` | 400 | Cross-aisle requires a bay |
| `empty-import` | 400 | Facility layout import must contain at least one row |
| `idempotency-key-required` | 400 | A resource-creation POST was missing its required `Idempotency-Key` header |
| `idempotency-key-reused` | 422 | The same `Idempotency-Key` was reused with a different request body |
| `concurrent-modification` | 409 | The resource was modified by another writer; re-fetch and retry ([ADR 0025](../adr/0025-optimistic-concurrency-version-column.md)) |
| `segment-endpoints-not-real` | 422 | Centreline endpoints must both be real points |
| `segment-start-end-equal` | 422 | Centreline start and end must differ |
| `missing-geometry-field` | 400 | Geometry coordinates and dimensions must be numbers, not null |
| `missing-sequence-hint` | 400 | Aisle sequenceHint is required |
| `invalid-layout-format` | 400 | Layout format must be json or svg (`GET /sites/{siteCode}/layout?format=`) |
| `invalid-report-query` | 400 | The report query is malformed or missing a required parameter (`cmd/facility-reports` only) |
| `report-store-error` | 500 | The report could not be served (`cmd/facility-reports` only) |
| `internal-error` | 500 | An unexpected internal error occurred |

All `type` values are prefixed with
`https://errors.facility-layout.warehouse-systems.dev/`.

## Two worked errors

Unknown site:

```bash
curl -i localhost:8080/sites/NOPE
```

```http
HTTP/1.1 404 Not Found
Content-Type: application/problem+json

{"type":"https://errors.facility-layout.warehouse-systems.dev/site-not-found","title":"Site not found","status":404,"detail":"site not found","instance":"/sites/NOPE"}
```

Duplicate location code:

```bash
curl -i -X POST localhost:8080/locations -H 'Content-Type: application/json' \
  -d '{"locationCode":"WH1-STOR-AMB-A07-03-02-B","locationType":"PalletRack"}'
```

```http
HTTP/1.1 409 Conflict
Content-Type: application/problem+json

{"type":"https://errors.facility-layout.warehouse-systems.dev/duplicate-location-code","title":"A location slot with this code already exists","status":409,"detail":"a location slot with this code already exists","instance":"/locations"}
```

## DTOs never leak domain structs

Request and response bodies are defined in the HTTP adapter
(`internal/adapters/inbound/http/dto.go`) and mapped explicitly. No domain
aggregate is serialized directly — the aggregates have unexported fields, so
it is not even possible by accident.
