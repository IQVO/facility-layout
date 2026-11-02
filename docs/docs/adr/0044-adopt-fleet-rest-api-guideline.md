---
id: 0044-adopt-fleet-rest-api-guideline
title: "44. Adopt the fleet REST API guideline"
sidebar_label: 44. Fleet REST API guideline
sidebar_position: 44
description: "ADR 0044 — noun routes, every POST keyed with a global Idempotency-Key (409 on reuse, Idempotency-Replayed), and keyset-paginated {items,nextCursor} collections; hard cutover, no aliases."
---

# 44. Adopt the fleet REST API guideline

## Status

**Accepted** (2026-11-01). Amends [ADR 0019](./0019-idempotency-key-middleware.md)
(key reuse is `409`, not `422`; the key is global; the two excluded commands are
now keyed) and [ADR 0006](./0006-partial-success-bulk-import.md) (the route).
Does not touch auth ([ADR 0015](./0015-remove-rest-mcp-auth.md) still stands).

## Context

The fleet REST API guideline (warehouse-docs) fixes three divergences between
services: command URLs (`:verb`, verb segments), the idempotency contract
(header spelling, reuse status, which POSTs are keyed) and unpaginated lists.
It is enforced by a Spectral ruleset (`.spectral.yaml`) in the `api-lint` job.
The cutover is a hard one: no aliases, no dual routes, no toggles; all services
deploy together.

## Decision

1. **Noun routes.** `POST /locations/import` becomes `POST /location-imports`
   (a batch job noun, STANDARD 1.3e) and
   `POST /locations/{locationCode}/decommission` becomes
   `POST /locations/{locationCode}/decommissionings` (a transition resource,
   1.4). Status codes and bodies are unchanged (`200` import report, `204`
   decommissioning). `operationId`s are unchanged, so MCP and A2A mappings do
   not move. The old routes answer 404/405 (`TestRetiredRoutesAreGone`).
2. **Every POST is idempotent.** All ten POST routes sit behind
   `RequireIdempotencyKey`, including the import and the decommissioning that
   ADR 0019 excluded. The key is 1-128 printable ASCII characters (otherwise
   `400 idempotency-key-invalid`; missing is `400 idempotency-key-required`),
   it is looked up by key alone (global, not per route), and the fingerprint is
   method + path + SHA-256(body). A replay returns the stored answer verbatim
   with `Idempotency-Replayed: true` (never `Idempotent-Replayed`); the same key
   with a different fingerprint is `409 idempotency-key-reused`. The key row
   commits in the same transaction as the aggregate change and its outbox rows;
   a 5xx or a panic rolls all of it back and is never stored. `PUT` routes take
   no key (idempotent by method, none of them appends).
3. **Keyset pagination.** Every collection `GET` takes `limit` (1-500, default
   100) and `cursor` and answers `{items, nextCursor}`; rows are ordered by
   (sort key, id) and the cursor is base64url JSON from
   `internal/adapters/inbound/http/page`. `invalid-limit`, `invalid-cursor` and
   `unknown-query-parameter` are `400` problems, so a client still sending a
   retired paging parameter fails loudly.
4. **Contract job.** `schemathesis.toml` widens only `positive_data_acceptance`
   with `400` for each paged `operationId` (`cursor` is opaque to OpenAPI).

### Paginated collections

`GET /sites`, `/site-roles/transfer-defaults`, `/sites/{siteCode}/zones`,
`/sites/{siteCode}/locations?role=`, `/sites/{siteCode}/structures`,
`/zones/{zoneId}/aisles`, `/zones/{zoneId}/cross-aisles`, `/location-types`,
`/placement-rules`.

### Exemptions (`x-pagination-exempt: view`)

| Operation | Why |
|---|---|
| `GET /sites/{siteCode}/layout` | One composite resource (the nested zone/aisle/slot tree) that merely contains arrays |
| `GET /zones/{zoneId}/grid` | One composite resource: the zone's rendering grid |
| `GET /zones/{zoneId}/travel-graph` | One composite resource: the zone's nodes and edges |
| `GET /distance` | One computed answer (a route between two slots), not a list of resources |

## Consequences

- Breaking for every caller: the two routes, the list envelope, a key on
  import and decommissioning, `409` for key reuse. Known callers: `e2e-tests`
  (`cmd/warehouse-day`), the `facility-mfe` remote (changed in this PR),
  `warehouse-infra` seed, and consumers of the lists listed above.
- The in-repo web remote follows `nextCursor` (`useFetchAll`) and no longer
  sends the `_r` cache-busting query parameter, which collections now reject.
- Paging is applied in the HTTP adapter over the repositories' complete
  result (ordered by (sort key, id)); pushing the keyset predicate into SQL is a
  later optimisation that changes no contract.
