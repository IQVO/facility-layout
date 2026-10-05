---
id: 0019-idempotency-key-middleware
slug: /adr/0019-idempotency-key-middleware
title: 19. Transactional Idempotency-Key middleware for resource-creation POSTs
sidebar_label: 19. Idempotency-Key middleware
sidebar_position: 19
description: "ADR 0019 — every true resource-creation POST (sites, zones, structures, aisles, cross-aisles, location types, placement rules, location slots) requires a caller-supplied Idempotency-Key header. A route-scoped middleware begins the outer Postgres transaction, joins it with each use case's own atomically()/UnitOfWork call via the existing tx-in-context mechanism (internal/pgtx), and lets the database's own unique-index lock do request de-duplication with no polling, no timeout, and no in-progress state."
---

# 19. Transactional Idempotency-Key middleware for resource-creation POSTs

## Status

**Accepted.** Ports order-management's reference implementation
(PR #105, ADR 0023) to this service, applied to all eight true
resource-creation POST endpoints this router exposes.

## Context

This service has the most resource-creation POST endpoints in the
warehouse-systems fleet: registering a site, a zone, a fixed structure,
an aisle, a cross-aisle, a location type, a placement rule, and a
location slot. Every one of them creates a genuinely NEW resource, and
a client that never receives the response to a successful call — a
dropped connection, a load-balancer timeout, a client-side retry policy
— has no safe way to tell "my request never arrived" apart from "my
request arrived and succeeded but I never saw the response." A naive
retry against any of these endpoints double-creates (or, for the
natural-key-id endpoints, hits a confusing 409/422 from the natural-key
uniqueness check instead of a clean, deterministic replay).

Two endpoints are deliberately excluded, matching the rollout's stated
"which endpoints need this" rule:

- **`POST /locations/import`** (`handleImportFacilityLayout`) is a
  BULK/batch import over many rows with partial-success semantics
  (ADR-0006) — a retry's semantics for "some rows already landed, some
  didn't" do not fit this middleware's whole-request replay model, and
  bolting it on would need a very different design (per-row
  idempotency, not per-request). Left for a dedicated follow-up if ever
  needed.
- **`POST /locations/{locationCode}/decommission`** acts on an
  EXISTING, caller-supplied resource, not a creation. It is not
  naturally susceptible to the double-create problem this middleware
  exists to solve (ADR-0005 already makes decommission one-way and
  idempotent-by-repeated-effect in the domain sense — the design goal
  for a *different* set of concerns, addressed instead by optimistic
  concurrency; see [ADR-0025](./0025-optimistic-concurrency-version-column.md),
  which added the version guard this rationale had been pointing at
  before it existed).

Every one of the eight protected use cases (`RegisterSite`,
`RegisterZone`, `RegisterFixedStructure`, `RegisterAisle`,
`RegisterCrossAisle`, `RegisterLocationType`, `DefinePlacementRule`,
`RegisterLocationSlot`) already wraps its aggregate `Save` and its
`Events.Publish` in `atomically(ctx, uc.UnitOfWork, ...)` from the
transactional-outbox rollout (ADR-0018, PR #106) — verified by reading
every one of the eight use case files before writing this change. No
use case needed new wiring to join the outer transaction; ADR-0018's
existing `atomically` helper already treats "already inside a
transaction" and "no transaction at all" identically, and its "already
in a tx? just run fn(ctx) directly" branch resolves the SAME
transaction the idempotency middleware begins, via the mechanism in
§3 below.

## Decision

### 1. `Idempotency-Key` header, required on every protected route

A request to any of the eight endpoints without an `Idempotency-Key`
header gets `400 application/problem+json`
(`idempotency-key-required`). Deliberate v1 choice, mirroring
order-management: require the header rather than make it
optional-but-recommended, since an optional header is trivial to forget
on exactly the retry path where it matters.

### 2. `idempotency_keys` table (migration `0006_idempotency_keys`)

```sql
CREATE TABLE idempotency_keys (
    key              TEXT PRIMARY KEY,
    method           TEXT NOT NULL,
    path             TEXT NOT NULL,
    request_hash     TEXT NOT NULL,
    status_code      INTEGER,
    response_body    BYTEA,
    response_headers JSONB,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at     TIMESTAMPTZ
);
CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys (created_at);
```

Identical shape to order-management's table. `request_hash` is
`hex(sha256(request body))`. `status_code`, `response_body`, and
`response_headers` start `NULL` and are populated by exactly one
`UPDATE`, in the same transaction that inserted the row, immediately
before that transaction commits (§4).

### 3. `internal/pgtx` extraction

This service's `internal/adapters/outbound/postgres/unit_of_work.go`
(ADR-0018) already held an unexported `txKey`/`withTx`/`txFrom` triple.
The idempotency middleware lives in the INBOUND http adapter and must
bind its own transaction into the same context slot
`UnitOfWork.Execute`/`querierFrom` reads — but this repo's
`internal/architecture` fitness tests forbid an inbound adapter from
importing the outbound postgres package (and vice versa). The
key/`WithTx`/`TxFrom` trio was extracted, unchanged in behavior, into a
new dependency-free package `internal/pgtx` that both sides import;
`unit_of_work.go`'s own `withTx`/`txFrom` are now one-line aliases over
it. Verified via `make arch-test` (all ten hexagonal fitness checks
still pass) and required zero changes to any use case.

### 4. `RequireIdempotencyKey` middleware (`internal/adapters/inbound/http/idempotency.go`)

Route-scoped only, via a small `idempotent(r chi.Router) chi.Router`
helper in `server.go` that wraps `r.With(RequireIdempotencyKey(pool))`
when `Server.IdempotencyPool` is non-nil, applied individually to each
of the eight `POST` route registrations — never `r.Use(...)` on the
whole router. `IdempotencyPool` nil (the in-memory dev/test
configuration, no Postgres backing) is a no-op passthrough, mirroring
every other optional Postgres-backed capability in this codebase's
convention (`UnitOfWork`, the outbox relay).

The request body is read fully into memory once, hashed, and restored
via `io.NopCloser(bytes.NewReader(...))` so downstream JSON decoding in
the real handler is unaffected. The middleware then begins a `pgxpool`
transaction directly (no import of the outbound `postgres` package
needed) and binds it into the request's context via `pgtx.WithTx`.

`INSERT INTO idempotency_keys (key, method, path, request_hash) VALUES
($1,$2,$3,$4) ON CONFLICT (key) DO NOTHING` runs inside that
transaction:

- **1 row inserted** (genuinely new key): call the real handler with
  the tx-carrying context, using an `httptest.ResponseRecorder` to
  CAPTURE its status/headers/body rather than writing to the real
  `http.ResponseWriter` yet (§5).
- **0 rows** (a row for this key already exists): roll back this new,
  empty transaction (it never wrote anything) and fall through to a
  plain, non-transactional `SELECT` (§6 explains why this is safe
  without additional locking):
  - `request_hash` mismatch → `422` (`idempotency-key-reused`).
  - `request_hash` match → a genuine retry: write the stored
    headers/status/body VERBATIM to the real `http.ResponseWriter`,
    without calling the real handler at all.

### 5. The no-null-status-code-ever-committed invariant

On the fresh-key path, after the wrapped handler runs against the
recorder:

- **Normal completion (no panic, including a business-validation
  error):** within the SAME transaction that inserted the bare row,
  `UPDATE idempotency_keys SET status_code=$1, response_body=$2,
  response_headers=$3, completed_at=now() WHERE key=$4`, then
  `COMMIT`. Only after a successful commit does the middleware copy the
  recorder's headers/status/body onto the real `http.ResponseWriter`.
- **Panic:** recovered, the transaction is `ROLLBACK`ed (neither the
  idempotency row nor any domain write the handler made ever becomes
  visible), and re-panics so the outer chi `Recoverer` still produces
  the normal `500`. A panic's outcome is never cached.

This buys the same invariant order-management's ADR 0023 documents: a
transaction that reads a COMMITTED `idempotency_keys` row can never
observe a `NULL` `status_code`. No "in-progress" marker, no
client-facing retry-after, no polling loop or timeout anywhere in this
design.

### 6. Concurrency: Postgres' own unique-index lock does the serialization

Two concurrent requests carrying the SAME key race on the
`INSERT ... ON CONFLICT (key) DO NOTHING`. Postgres serializes them at
the primary-key unique index: the second (and every later) inserter's
statement BLOCKS until the first inserter's transaction resolves
(commit or rollback). So by the time ANY transaction observes
`rowsAffected()==0`, the original inserting transaction has
unconditionally finished — combined with §5's invariant, the "0 rows"
branch's plain read is always safe, with no polling or lock-retry
budget needed. Proven with a real 5-goroutine concurrency test hitting
the real router through `RegisterSite`, not a sequential simulation.

## Test coverage strategy

The reference implementation's six required scenarios (a–f: fresh key,
replay, mismatched body, missing header, concurrency, cached business
error) are run in full, through the real chi router
(`inboundhttp.NewRouter`) against real testcontainers Postgres, for
**`POST /sites`** (`RegisterSite`) — the simplest endpoint end to end,
and **`POST /locations`** (`RegisterLocationSlot`) — the most complex
use case in this service (chain-of-custody resolution, PlacementRule
evaluation, metrics recording), proving the middleware's transaction
join works correctly even through the deepest call graph this service
has. This is the same "two representative endpoints, full matrix" scope
called for in the rollout brief.

The remaining six endpoints (zones, fixed structures, aisles,
cross-aisles, location types, placement rules) each get a single
smoke-level integration test: fresh key creates the resource, and a
byte-identical replay with the same key does not create a duplicate.
This proves the `idempotent(r)` wiring is live and correctly threaded
on every route — the middleware's own internal correctness (the
invariant, the concurrency proof, the panic/rollback path) is
architecture-invariant across routes and is exhaustively covered once,
not per-route, in the two full-matrix suites. Splitting this way keeps
the PR reviewable while still exercising every protected endpoint at
least once against a real database and a real HTTP request.

## Consequences

- All eight resource-creation POSTs are now safe against a
  lost-response client retry: a retry either replays the original
  outcome verbatim or is rejected as a different request under an
  already-used key, and never silently double-creates.
- `idempotency_keys` rows are retained past their usefulness unless a
  cleanup runs — closed by [ADR-0026](./0026-housekeeping-sweeper.md)'s
  sweeper (default 24h TTL, `IDEMPOTENCY_KEY_TTL`; the `created_at`
  index below exists precisely for it). A retry after the TTL is
  treated as a new request, which re-validates against current state.
- `POST /locations/import` and `POST /locations/{locationCode}/decommission`
  remain unprotected by design (see Context); a future change adding
  per-row idempotency to bulk import, or revisiting decommission's
  semantics, is a separate decision.
- `internal/pgtx` is now the one place both the inbound HTTP adapter
  and the outbound Postgres adapter bind/read a transaction from
  context — any FUTURE inbound adapter needing to join a Postgres
  transaction (e.g. a future MCP write tool) reuses this package rather
  than inventing a third mechanism.
- Every protected route pays one extra `INSERT ... ON CONFLICT` and,
  on the fresh-key path, one extra `UPDATE` inside the request's
  existing transaction — no additional round trip to a different
  system, no added latency budget beyond ordinary Postgres write
  latency.
