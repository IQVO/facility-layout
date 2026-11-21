---
id: 0046-idempotency-replay-classes
slug: /adr/0046-idempotency-replay-classes
title: "46. Every problem type has an idempotency replay class; a retryable refusal is not stored"
sidebar_label: "46. Idempotency replay classes"
sidebar_position: 46
description: "ADR 0046 — fleet rule (owner, 2026-11-18). A retry under the same Idempotency-Key is answered from the store only when the first answer is final for the request. Every problem type declares FINAL, OUTCOME or RETRYABLE on its catalogue entry; the middleware reads the class and holds no list of slugs. A RETRYABLE refusal (concurrent-modification, a parent not registered yet, a rule defined later) is rolled back with its key row exactly like a 5xx. Narrows ADR 0019 and ADR 0044 (the replay sentences)."
---

# 46. Every problem type has an idempotency replay class; a retryable refusal is not stored

## Status

Accepted (2026-11-20). **Narrows [ADR 0019](./0019-idempotency-key-middleware.md)** ("every normal
response is cached, 4xx included") **and the replay sentence of
[ADR 0044](./0044-adopt-fleet-rest-api-guideline.md)**; their Decision text is not rewritten, both carry a
dated pointer here. Implements the fleet rule `.claude/rules/fleet/idempotency-and-outbox.md` (owner decision
2026-11-18; standard and adoption steps in warehouse-harness-template `docs/idempotency-replay-classes.md`).

## Context

The middleware (`internal/adapters/inbound/http/idempotency.go`) inserts the key row in the SAME Postgres
transaction as the business unit of work (`pool.Begin`, `INSERT ... ON CONFLICT (key) DO NOTHING`), binds the
transaction into the context (`internal/pgtx`) so the use cases' `UnitOfWork.Execute` joins it, and runs the
handler behind `SAVEPOINT handler_run`. Before this ADR it stored every answer below 500 and rolled the
transaction back (key row included) only for a 5xx or a panic. So a `409 concurrent-modification` or a
`404 site-not-found` (the site is registered a moment later by another request) was replayed for 24 hours to a
client that did the right thing and retried.

What a 4xx did to the handler's writes: nothing by itself. The use cases' `atomically` scope JOINS the request
transaction and never opens a savepoint of its own (`postgres/unit_of_work.go` `Execute`: a joined call just runs
`fn`), so a refusal returned after a write inside the scope keeps that write until the request transaction ends;
`RELEASE SAVEPOINT` keeps it and it commits WITH the key row. Only a failed statement (an aborted transaction)
makes the release fail, and then `ROLLBACK TO SAVEPOINT` discards the handler's writes. The ten keyed routes are
every `POST`: `/sites`, `/sites/{siteCode}/zones`, `/sites/{siteCode}/structures`, `/zones/{zoneId}/aisles`,
`/zones/{zoneId}/cross-aisles`, `/location-types`, `/placement-rules`, `/locations`,
`/locations/{locationCode}/decommissionings` and `/location-imports` (`server.go` router wiring); the `PUT`s take
no key. The key lives 24 hours (`IDEMPOTENCY_KEY_TTL`, hourly sweeper, ADR 0026).

## Decision

### 1. One class per problem type, declared on the catalogue entry

| Class | Meaning | Stored and replayed |
|---|---|---|
| `FINAL` | the answer is a function of the request alone, or a stored answer can never become false | yes |
| `OUTCOME` | the command COMMITTED state, then answered an error that must be remembered | yes, its writes commit with it |
| `RETRYABLE` | nothing committed and the answer depends on state that can change (version conflict, a referenced fact not known yet, a rule defined later, a dependency not ready) | no: the key is released |

A 2xx is stored; a 5xx and a panic are never stored; a replay carries `Idempotency-Replayed: true`. The class is
a third field of `problemInfo{slug, title, class}` (`errors.go`): the `errorCategories` rows carry it, and every
other problem type is declared once with `register(...)` in `problems.go` (the middleware's own problems, the
NUL 404, the paging, layout and report problems, `internal-error`, `dependency-unavailable`). Reshape: the
`errorCategories` slice kept its `{err, status, problem}` shape (the status stays the row's, so `statusFor`,
`ErrorSlugs` and the MCP and A2A slug tests are untouched) and `problemInfo` gained `class`; the ad hoc literals at
the call sites became `register` variables (slug, title and status on the wire unchanged). There is no default
class.

### 2. Mechanism

`writeProblem` (so `writeError`) marks the middleware's capture (`replay_class.go`: `capture`,
`markReplayClass`) with the class of the problem it answers. `runFreshRequest` asks `capture.storable()`: a 2xx
yes, a 5xx no, any other answer only when the class is FINAL or OUTCOME. Everything else, a RETRYABLE answer
**and a refusal that declares no class** (a handler that answers a 4xx without the problem writer; the safe
fallback), takes the branch of the 5xx: `tx.Rollback`, key row included, then the answer is written. The
middleware holds no slug and no status range other than 2xx and 5xx. 2xx, 5xx and FINAL answers are
byte-identical to before. The lost-writes rule: a 2xx, and now an OUTCOME refusal, whose savepoint cannot be
released has lost its writes, so the request fails with a 500 and the key retries.

**Releasing a key means a different body under the same key is a NEW request**, not `409 idempotency-key-reused`:
nothing is left to compare against. The same body runs again. A client that reused a key after a refusal on
purpose now gets its retry executed instead of the refusal back.

**What a concurrent duplicate sees when the first ends RETRYABLE:** it is blocked by Postgres on the unique index
at the key INSERT until the first transaction resolves; the first rolled back, so the duplicate's own INSERT
succeeds (the "fresh key" branch), it executes the command itself and answers its own result, possibly a
success. It never replays a released refusal and never runs alongside the first. When the first ends FINAL,
OUTCOME or 2xx the duplicate finds the row and replays it.

### 3. No OUTCOME entry (re-checked per keyed route)

This store keeps a 4xx's writes (`RELEASE SAVEPOINT`), so a refusal returned after a write inside `atomically`
WOULD commit with its key row: OUTCOME needs no new mechanism, only the lost-writes rule above. Read for each keyed
use case; none writes and then refuses with a stored answer:

- `register_site.go`, `register_zone.go`, `register_fixed_structure.go`, `register_aisle.go`,
  `register_cross_aisle.go`, `register_location_type.go`, `define_placement_rule.go`,
  `register_location_slot.go`: every refusal (validation, parent chain, duplicate, placement rule) is returned
  before `atomically`. Inside the scope the only refusals are `gln-already-assigned` (the FIRST step, a refused
  claim writes nothing, `gln.go:39`) and `concurrent-modification` (RETRYABLE: the whole transaction, the GLN claim
  included, rolls back).
- `decommission_location_slot.go`: the 404 and `already-decommissioned` (`slot.Decommission`) come before the
  scope.
- `import_facility_layout.go`: a bad row is reported in the 200 body (`ImportReport`, `RowsRejected`), never as a
  4xx; the only 4xx is `empty-import`, before any write. Rows that did write are answered 200 and stored as a
  success.

The class stays in the vocabulary so a future refusal that commits is declared OUTCOME, not forced into FINAL; the
mechanism is proven with a synthetic OUTCOME refusal over the real middleware and Postgres
(`TestReplayClass_AnOutcomeRefusalCommitsItsWritesWithTheKeyRowAndReplays`,
`TestIdempotency_AnOutcomeRefusalWhoseWritesWereLostIsNeverCached`). No handler writes a refusal by hand (grep of
`problem+json` and `WriteHeader` in the handlers: `writeProblem` and `writeJSON`/`WriteHeader(204)` only).

### 4. The class table

87 problem types: 68 FINAL, 0 OUTCOME, 19 RETRYABLE. This table is held equal to the
catalogue (slug, status, class) by `TestTheADRClassTableMatchesTheCatalogue`. Every non-FINAL row says why, with
lines; the FINAL rows that are not plain request validation say why too. "Not keyed" means no keyed route can
answer it today; its class is still declared. A slug that several sentinels share has one class
(`already-decommissioned`, `site-not-active`, `malformed-location-code`, ...).

| Problem type | Status | Class | Why |
|---|---|---|---|
| `aisle-not-active` | 409 | FINAL | Same for aisles (`register_location_slot.go:308`) (J2). |
| `aisle-not-found` | 404 | RETRYABLE | The aisle of the location code is not registered yet (`register_location_slot.go:305`); caller-chosen codes (J3). Before any write. |
| `already-decommissioned` | 409 | FINAL | `slot.Decommission` (`slot/location_slot.go:196`): decommission is one-way (ADR 0005); the sibling aggregates' variants are not reachable from a keyed route (J2). |
| `asrs-port-without-automated-storage` | 422 | RETRYABLE | `automated_storage_rule.go:27` (ADR 0048): the aisle holds no active `AutomatedStorage` slot yet; registering it lets the same request pass (the J3 argument). Before any write. |
| `automated-storage-already-in-aisle` | 409 | RETRYABLE | `automated_storage_rule.go:25` (ADR 0048), or the lost race on migration 0014's partial unique index (`postgres/slot_repo.go`, rolled back with the scope): decommissioning the aisle's AS/RS frees it, so a retry can pass. Nothing committed. |
| `concurrent-modification` | 409 | RETRYABLE | A version-guarded `Save` of an existing row found it at another version (`postgres/site_repo.go:56-71`, `slot_repo.go:78-104`): the answer depends on another writer's commit (two concurrent `POST /locations/{code}/decommissionings` of one slot). The whole request transaction rolls back, key row included. Proven by `TestReplayClass_ConcurrentModificationLoserIsReleasedAndItsRetryExecutes` (a staged genuine race: the loser's key row is absent, its retry executes and is then stored and replayed). |
| `cross-aisle-aisle-mismatch` | 422 | RETRYABLE | `register_cross_aisle.go:49`: the two aisles must already exist in the zone; they can be registered after the first attempt (J6). Before any write. |
| `cross-aisle-same-aisle` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `dependency-unavailable` | 503 | RETRYABLE | 503 of `/readyz`, never behind the middleware; a 5xx is never stored. |
| `dock-flow-required` | 422 | RETRYABLE | `functionalAttributesFor(locationType.Role(), ...)` (`slot/functional.go:108`): a function of the request AND the named type's role (J4). Before any write. |
| `duplicate-aisle` | 409 | FINAL | `register_aisle.go:45`; same argument (J1). |
| `duplicate-cross-aisle` | 409 | FINAL | `register_cross_aisle.go:57` (J1). |
| `duplicate-fixed-structure` | 409 | FINAL | `register_fixed_structure.go:41` (J1). |
| `duplicate-location-code` | 409 | FINAL | `register_location_slot.go:179`; a decommissioned code is still taken (ADR 0005) (J1). |
| `duplicate-location-type` | 409 | FINAL | `register_location_type.go:29`; location types are never edited or deleted (J1). |
| `duplicate-placement-rule` | 409 | FINAL | `define_placement_rule.go:37`; rules are never edited or deleted (J1). |
| `duplicate-site-code` | 409 | FINAL | `register_site.go:48`: the code is taken and no site is ever deleted or reused (ADR 0005, one-way decommission): the refusal never lifts (J1). Before any write. |
| `duplicate-zone` | 409 | FINAL | `register_zone.go:70`; same argument (J1). |
| `empty-cross-aisle-bay` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `empty-cross-aisle-from-aisle` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `empty-cross-aisle-to-aisle` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `empty-cross-aisle-zone-id` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `empty-fixed-structure-footprint` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `empty-fixed-structure-id` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `empty-fixed-structure-kind` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `empty-fixed-structure-label` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `empty-fixed-structure-site-code` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `empty-import` | 400 | FINAL | `import_facility_layout.go:102`: the request has no rows; nothing was written. |
| `empty-placement-rule-id` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `empty-site-name` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `empty-zone-predicate` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `functional-attributes-not-allowed` | 422 | RETRYABLE | Same (`slot/functional.go:114`, `:120`, `:133`) (J4). |
| `gln-already-assigned` | 409 | FINAL | `gln.go:39`: `location_glns` is claim-only, no row is ever deleted (`postgres/gln_registry.go:28-43`); the claim is the first step of the scope and a refused claim writes nothing (J1). |
| `idempotency-key-invalid` | 400 | FINAL | The key header of this request; answered before the store. |
| `idempotency-key-required` | 400 | FINAL | The key header of this request; answered before the store. |
| `idempotency-key-reused` | 409 | FINAL | The fingerprint of this request against the stored row of the first one (`idempotency.go` `replayCachedResponse`); answered before anything executes. |
| `internal-error` | 500 | RETRYABLE | A 5xx is never stored: unmapped errors and every middleware failure. |
| `invalid-aisle-code` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `invalid-cursor` | 400 | FINAL | Collection `GET` query; not keyed. |
| `invalid-dimensions` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `invalid-gln` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `invalid-layout-format` | 400 | FINAL | `GET .../layout?format=` query; not keyed. |
| `invalid-limit` | 400 | FINAL | Collection `GET` query; not keyed. |
| `invalid-location-type` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `invalid-max-volume` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `invalid-max-weight` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `invalid-pitch` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `invalid-report-query` | 400 | FINAL | `GET /reports/*` query; not keyed. |
| `invalid-site-code` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `invalid-site-role` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `invalid-zone-code` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `invalid-z` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `location-slot-not-found` | 404 | RETRYABLE | `POST /locations/{code}/decommissionings` names a code nobody registered yet (`decommission_location_slot.go:28`); location codes are caller-chosen (J3). Before any write. |
| `location-type-not-found` | 404 | RETRYABLE | A slot or a placement rule names a location type that is not registered yet (`register_location_slot.go:192`, `define_placement_rule.go:29`); caller-chosen names (J3). Before any write. |
| `location-type-role-mismatch` | 422 | FINAL | `placement.NewLocationType` (`domain/placement/placement.go:97`, ADR 0048): a well-known type name with another role; a function of the request alone. |
| `malformed-location-code` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `malformed-request-body` | 400 | FINAL | `decodeJSON` (`server.go`), the unreadable body (`idempotency.go`) and a NUL in the body (`nul.go`): a function of the bytes of the request. Three titles, one slug, one class. |
| `missing-geometry-field` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `missing-location-code` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `missing-sequence-hint` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `missing-transfer-capability-field` | 400 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `negative-pick-sequence` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `negative-sequence-hint` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `no-route-between-zones` | 422 | RETRYABLE | `GET /distance` only (`estimate_travel_distance.go:88`): depends on the positions recorded so far; not keyed. |
| `no-route` | 422 | RETRYABLE | `GET /distance` only (`travel/graph.go:233`, `:240`): depends on the travel graph other requests build; not keyed. |
| `placement-rule-not-found` | 404 | RETRYABLE | `GET /placement-rules/{id}` only (`reads.go:83`), not keyed; caller-chosen rule ids (J3). |
| `placement-rule-violated` | 422 | RETRYABLE | `placement.Evaluate` (`domain/placement/rules.go:39-40`, `:51-52`) reads the rules and the zone at that moment; a rule defined later (an Allow rule) lets the same request pass (J5). `RegisterLocationSlot` refuses before `atomically`; only a metric is recorded. |
| `report-store-error` | 500 | RETRYABLE | A 5xx is never stored; `GET /reports/*`, not keyed. |
| `resource-not-found` | 404 | FINAL | A NUL in the path can never name a resource (`nul.go`); answered before the middleware. |
| `segment-endpoints-not-real` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `segment-start-end-equal` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `site-not-active` | 409 | FINAL | A decommissioned site never returns (ADR 0005); no use case decommissions a site, so no keyed route can answer it today (`import_facility_layout.go:206` answers it per row) (J2). |
| `site-not-found` | 404 | RETRYABLE | `FindByCode` found no site (`register_zone.go:51`, `register_fixed_structure.go:33`, `register_location_slot.go:283`). Site codes are chosen by the caller, so another request can register the site a moment later; nothing is deleted, but absence is what can change (J3). Nothing committed (before `atomically`). Proven by `TestReplayClass_SiteNotFoundIsReleasedSoTheRetryFindsItOnceRegistered`. |
| `spot-kind-not-allowed` | 422 | RETRYABLE | `slot/identity.go:49`: the slot's role comes from the named location type (J4). |
| `unknown-activity` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `unknown-direction` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `unknown-dock-flow` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `unknown-fixed-structure-kind` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `unknown-handling-unit` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `unknown-location-role` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `unknown-placement-effect` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `unknown-query-parameter` | 400 | FINAL | Collection `GET` query; not keyed. |
| `unknown-spot-kind` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `unknown-status` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `unknown-temperature-class` | 422 | FINAL | Validation of the request's own fields or path: a function of the request alone. |
| `unknown-travel-waypoint` | 422 | RETRYABLE | `GET /distance` only (`travel/graph.go:190`, `:193`): the graph has no waypoint for the aisle/bay yet; not keyed. |
| `work-center-activities-required` | 422 | RETRYABLE | Same (`slot/functional.go:123`) (J4). |
| `zone-mismatch` | 422 | FINAL | `slot/location_slot.go:110-111`: compares the zone id derived from the location code with the attributes of the zone found by that same id: an invariant of the code, never raised in practice (J7). |
| `zone-not-active` | 409 | FINAL | Same for zones (`register_aisle.go:32`, `register_location_slot.go:297`) (J2). |
| `zone-not-found` | 404 | RETRYABLE | The parent zone of an aisle, cross-aisle or slot is not there yet (`register_aisle.go:29`, `register_cross_aisle.go:33`, `register_location_slot.go:294`); zone ids derive from caller-chosen codes (J3). Before any write. |

## Consequences

- A client retrying through a `concurrent-modification`, a parent that is not registered yet or a rule defined
  later now gets its retry executed under the same key, and only the final answer is stored. The cost is one more
  execution of a refusal that wrote nothing.
- A stored refusal is now only one a retry could not change: the request's own validation, a code that is taken
  for good and a structure that is decommissioned for good. `Idempotency-Replayed: true` still means "the same
  answer".
- **Callers.** The phase-0 scan of this wave found no outbound caller of these `POST`s with a deterministic key
  (the consumers read the layout over REST, MCP, A2A and events). A person who retries a refused
  form under the same key now sees the second attempt execute; nothing retries by itself (release means "the same
  key may be tried again by whoever sent it").
- MCP and A2A are read-only Open Host Service tools and skills (`mcp.ToolAnnotations{ReadOnlyHint: true}`, the
  A2A skills `site_layout` and `travel_distance`): no write tool, no store of their own, nothing to classify.
- The spec descriptions of `Idempotency-Key` and of the stored response say what is replayed and what releases the
  key; the REST reference is regenerated.

### Judgement calls (for the owner)

1. **J1: `duplicate-*` and `gln-already-assigned` are FINAL** although they depend on stored state. Codes, names
   and ids are chosen by the caller and a registered one is never deleted, edited or reused (ADR 0005; no
   `DELETE` outside the sweeper; no partial unique index), so the refusal can never become false; same criterion
   as inbound-receiving `asn-already-exists` and offer-availability `duplicate-listing`. Flip to RETRYABLE costs
   one word and one extra read. A future delete or re-register command must reclassify them.
2. **J2: `already-decommissioned`, `site-not-active`, `zone-not-active`, `aisle-not-active` are FINAL.** Decommission
   is one-way and no reactivation use case exists (ADR 0005); only a slot can be decommissioned through the API
   (`decommission_location_slot.go`), so the three `*-not-active` types are not answerable by any keyed route today.
   A future decommission or reactivation command for sites, zones or aisles must reclassify them.
3. **J3: the six `*-not-found` types are RETRYABLE although this service deletes nothing.** The codes are chosen by
   the caller, not minted here, and a parent registered by another request a moment later makes the same request
   pass. Contrast order-management `order-not-found` (an id minted by the service). Same criterion as the brief's
   404 default.
4. **J4: `dock-flow-required`, `work-center-activities-required`, `functional-attributes-not-allowed` and
   `spot-kind-not-allowed` are RETRYABLE** although a location type's role never changes today (no edit command):
   the refusal is computed from the REFERENCED type's role, not from the request alone, so by the tie-break
   (unsure between FINAL and RETRYABLE) it is released. The line between J1/J2 and J4: a record this service holds
   that nothing can remove (FINAL) against a verdict derived from another aggregate's attributes (RETRYABLE).
5. **J5: `placement-rule-violated` is RETRYABLE.** Rules are defined at any time (`POST /placement-rules`); an
   Allow rule defined later lets the same slot pass.
6. **J6: `cross-aisle-aisle-mismatch` is RETRYABLE**: the aisles it names may be registered after the first
   attempt. It also covers a cross-aisle naming an aisle that does not belong to the zone at all (request-only);
   mixed slug, so RETRYABLE; splitting it is a follow-up.
7. **J7: `zone-mismatch` is FINAL**: an invariant of the location code against the zone found by the same id;
   unreachable in practice.
8. `no-route`, `no-route-between-zones` and `unknown-travel-waypoint` are answered by the unkeyed `GET /distance`
   only; classified RETRYABLE (the graph changes) so a future keyed route cannot answer an unclassified one.

### Follow-ups, not in this change

- A replay does not check the age of the row: the 24-hour window is enforced by the hourly purge alone
  (`sweeper.go`); `replayCachedResponse` selects the row with no `created_at` test.
- Split `cross-aisle-aisle-mismatch` into a request-only and a state-dependent type.
- The `idempotency_keys` table still has no `route` column (the fingerprint holds method and path).
- **A creation race is not guarded.** Found while staging the lost race: a new aggregate carries version 1
  (`site.go:100`, `location_slot.go:131`), the same number the `INSERT` writes, so when two requests register the
  same new code at once (both pass the existence check) the second `INSERT ... ON CONFLICT DO UPDATE ... WHERE
  version = 1` succeeds and overwrites the first row: both answer 201 instead of one answering
  `duplicate-site-code`. Not fixed here (it changes who wins a race, not what is replayed); a follow-up should
  give a new aggregate version 0 or make the first `INSERT` conflict an error.
  **Fixed by [ADR 0047](./0047-concurrent-creation-race.md) (2026-11-20): version-0 new aggregates for the
  versioned repos, a refusing `Create` for the non-versioned ones, and the loser answered with this table's
  FINAL `duplicate-*` types.**

## Tests

`replay_class_test.go` (Docker-free): every problem type has a class (red before the field existed:
`undefined: allProblemTypes`; red with one entry blanked), no `problemInfo` literal outside the catalogue and
`register`, this table equals the catalogue, `statusFor` and `problemFor` equal their row, `writeProblem` and
`writeError` mark the capture, the `storable` and lost-writes truth tables.
`idempotency_replay_classes_integration_test.go` (+ `..._wiring_integration_test.go`): the fleet template, every
entry through the real middleware and Postgres. `idempotency_retryable_integration_test.go`: a genuine lost race
(`concurrent-modification`) and a parent not registered yet (`site-not-found`), through the real router and
Postgres. `idempotency_outcome_integration_test.go` and `idempotency_poison_integration_test.go`: the OUTCOME
mechanism and the aborted-transaction cases.
