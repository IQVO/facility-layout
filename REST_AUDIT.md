# REST audit — facility-layout

This service was built to REST maturity level 2 and RFC 7807 from the first
commit, rather than migrating to them later like the four sibling services
did historically. This document records the audit against the same six
checks those repos used, and the decisions taken.

## 1 — Resource nouns, not verbs, in URLs

**Clean.** Every path is a resource collection or a member of one:
`/sites`, `/sites/{siteCode}`, `/sites/{siteCode}/zones`, `/zones/{zoneId}`,
`/zones/{zoneId}/aisles`, `/location-types`, `/placement-rules`,
`/locations`, `/locations/{locationCode}`.

Two verb-suffixed action endpoints exist, and both are deliberate:

- `POST /locations/{locationCode}/decommissionings` — a genuine domain command
  on a single slot, not a CRUD update. This is the same
  `/tasks/{id}/complete`, `/reservations/{id}/confirm-pick` pattern the rest
  of the fleet uses, and correct DDD/REST practice for a non-CRUD command.
  Forcing it into `PATCH {"status":"Decommissioned"}` would misrepresent a
  one-way state transition as a field assignment.
- `POST /location-imports` — a collection-level action on the `/locations`
  resource, scoped to it rather than dangling off an unscoped `/admin/...`
  path.

No unscoped RPC-style endpoint exists in this service.

## 2 — Correct HTTP methods

**Clean.** Every `GET` handler is side-effect-free: they call only
`GetSite`, `ListSites`, `GetZone`, `ListZones`, `GetAisle`, `ListAisles`,
`GetLocationType`, `ListLocationTypes`, `GetPlacementRule`,
`ListPlacementRules`, `GetLocationSlot`, `GetSiteLayout` and `GetZoneGrid` —
all of which are read-only over the repositories and publish no events.
`GetSiteLayout` and `GetZoneGrid` have explicit "publishes nothing: it is a
pure read model" unit tests that assert the event count is unchanged after
the call.

`POST` is used for creation and for the two domain commands. `PUT`/`PATCH`
are not required: this domain is command-oriented and has no
field-assignment update anywhere. There is no `DELETE`, because nothing in
this context is deleted — structure is decommissioned, which is a state
transition with a published event, not a removal.

## 3 — Correct status codes

**Clean, by construction.** The mapping is centralised in
`internal/adapters/inbound/http/errors.go` (`statusFor`), tested endpoint by
endpoint:

| Code | Meaning here |
|---|---|
| `200` | Successful read, or the bulk-import report |
| `201` | Resource created — **always** with a `Location` header |
| `204` | `decommission` succeeded; no body |
| `400` | Malformed input: bad JSON, a location code that is not seven `[A-Z0-9]` segments, an empty required identifier, an empty import array |
| `404` | The named site/zone/aisle/slot/type/rule does not exist |
| `409` | State conflict: duplicate code, parent exists but is not Active, slot already decommissioned |
| `422` | Semantically invalid: non-positive capacity, unknown enum value, PlacementRule violation |

Two distinctions worth stating, because they are easy to get wrong:

- **404 vs 409 on the chain of custody.** Registering a slot whose Site,
  Zone or Aisle does not exist is `404` (the parent is not there). If the
  parent exists but is Decommissioned, it is `409` (it is there, and its
  state conflicts with the request). Both are covered by tests.
- **400 vs 422 on a bad location code.** A code that cannot be parsed into
  seven `[A-Z0-9]` segments is `400` — it could never identify a resource.
  A well-formed code whose LocationType violates a PlacementRule is `422` —
  the request is understood and refused on semantics.

### Location headers

Every `201` sets one. This forced a decision: a `Location` pointing at a URL
with no `GET` behind it is not level 2. Four single-resource reads were
therefore added beyond CLAUDE.md's endpoint list, purely so each `Location`
resolves:

- `GET /zones/{zoneId}` — target of `POST /sites/{siteCode}/zones`
- `GET /zones/{zoneId}/aisles/{aisleCode}` — target of `POST /zones/{zoneId}/aisles`
- `GET /location-types/{name}` — target of `POST /location-types`
- `GET /placement-rules/{ruleId}` — target of `POST /placement-rules`

Each is covered by a test that follows the returned `Location` header and
asserts a `200`.

`POST /location-imports` deliberately returns `200`, not `201`: a bulk
import is a partial-success report over many rows, not the creation of one
addressable resource, so there is no single `Location` to hand back.

## 4 — Idempotency semantics

Documented, not changed:

- **Naturally idempotent:** every `GET`.
  `POST /locations/{locationCode}/decommissionings` is idempotent in *effect*
  but not in *status*: the second call returns `409`, because v1 treats
  double-decommission as a conflict a caller should know about rather than
  silently absorb. This is a deliberate design choice, recorded here and in
  the OpenAPI description.
- **Not idempotent:** every `POST` that creates. They are all
  client-supplied-identity creates (site code, zone id, aisle code, type
  name, rule id, location code), so a repeat is detected and rejected as a
  duplicate `409` rather than creating a second copy. That is the correct
  behaviour for this domain — there is no scenario where re-registering an
  existing coded location should silently succeed.
- `POST /location-imports` is idempotent per-row in the sense that a row
  naming existing structure reuses it; a row whose *slot* already exists is
  reported as a rejected row, not a silent overwrite.

No bug found under this check.

## 5 — Consistent JSON casing

**Clean.** Every request and response field is camelCase, verified across
`internal/adapters/inbound/http/dto.go`: `siteCode`, `zoneId`, `aisleId`,
`areaCode`, `zoneCode`, `temperatureClass`, `sequenceHint`, `locationCode`,
`locationType`, `capacityOverride`, `maxWeightKg`, `maxVolumeM3`,
`rowsSubmitted`, `slotsImported`, `rowsRejected`. Domain structs never cross
the boundary; every response is a DTO defined in that file.

## 6 — Content negotiation

**Clean.** Success responses set `Content-Type: application/json`; the SVG
variant of the layout endpoint sets `image/svg+xml`; and **every** error
response sets `Content-Type: application/problem+json` with an RFC 7807
body (`type`, `title`, `status`, `detail`, `instance`). This is asserted by
a shared `assertProblem` helper used by every failing-path HTTP test, which
also checks that the body's `status` field agrees with the actual HTTP
status.

---

## OpenAPI coverage

`apis/openapi.yaml` (OpenAPI 3.0.3) documents **22/22** router operations
across 17 paths, with full request/response schemas, a shared RFC 7807
`Problem` component referenced by every error response, typed path/query
parameters, and domain-grounded examples throughout (`WH1`, `STOR`, `AMB`,
`A07`, `WH1-STOR-AMB-A07-03-02-B`, `PalletRack`, `RULE-HAZ-ONLY-RACK`) — no
`foo`/`bar` anywhere.

```
$ spectral lint apis/openapi.yaml --ruleset .spectral.yaml --fail-severity=warn
No results with a severity of 'warn' or higher found!
```

One spec-level fix was needed during linting: the zone-grid's nullable cell
was originally expressed as `nullable: true` alongside an `allOf` on the
`items` schema, which Spectral rejects (`oas3-valid-media-example`:
`"nullable" cannot be used without "type"`). It is now declared on the
`GridCell` schema itself, which is both valid and more accurate — a grid
cell *is* a nullable object.
