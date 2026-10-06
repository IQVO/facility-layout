---
id: 0026-housekeeping-sweeper
title: 26. Housekeeping sweeper for idempotency_keys and published outbox rows
sidebar_label: 26. Housekeeping sweeper
sidebar_position: 26
description: "The two append-only tables this service grows without limit — idempotency_keys (ADR-0019) and outbox_events (ADR-0018) — get a periodic in-process sweeper that deletes expired keys and PUBLISHED outbox rows in bounded batches, mirroring inventory-storage's merged sweeper.go pattern."
---

# 26. Housekeeping sweeper for idempotency_keys and published outbox rows

## Status

**Accepted.** 2026-10-04. Closes the "no retention job" follow-ups
recorded in ADR-0018 and ADR-0019.

## Context

Two tables in this service only ever grow:

- `idempotency_keys` (ADR-0019): one row per Idempotency-Key value ever
  seen on a protected route. The middleware needs the row only for as
  long as a client might legitimately retry the same logical submit —
  beyond that, the row is dead weight (and, given it carries the cached
  response body, unbounded storage of stale payloads).
- `outbox_events` (ADR-0018): one row per already-encoded Kafka message,
  per topic (two rows per domain event here — integration and
  analytics). Once `published_at` is set, the row is forensics: useful
  for "what was published when", useless for correctness. Unpublished
  rows are the relay's work queue and must NEVER be deleted.

Both ADRs recorded a "known follow-up: no retention job" gap. The
fleet's reference implementation already exists — inventory-storage's
`internal/adapters/outbound/postgres/sweeper.go` (merged) — and this
repo's tables are shape-compatible (same `created_at` key semantics,
same `published_at IS NOT NULL` relay discipline).

## Decision

Mirror the inventory-storage sweeper verbatim in shape:

- One `postgres.Sweeper` type with one `Run(ctx)` loop, wired in
  `cmd/facility` whenever Postgres is configured (both tables exist even
  when `EVENT_PUBLISHER` is not `kafka`). It runs in the same process as
  the HTTP server and the outbox relay, sharing the shutdown signal.
- **Idempotency-key TTL, default 24h** (`IDEMPOTENCY_KEY_TTL`): rows
  with `created_at` older than the TTL are deleted. This bounds how late
  a client retry can be replayed from the cached response — beyond it,
  the retry is treated as a new request (which is the safe outcome: the
  handler runs again and re-validates against current state).
- **Published-outbox retention, default 7d** (`OUTBOX_RETENTION`): rows
  with `published_at IS NOT NULL` older than the retention are deleted.
  Unpublished rows are never swept, however old — they are the relay's
  pending work.
- **Interval, default 1h** (`HOUSEKEEPING_INTERVAL`): the sweeper sweeps
  once immediately on start (a pod booting after an outage catches up
  without waiting a full interval) and then every interval. `0` disables
  the loop entirely.
- Each DELETE targets an explicit id/key set chosen by a subquery with
  `LIMIT` (default batch 1000), repeated until a batch comes back short,
  so a large backlog is removed in short transactions rather than one
  long lock. A TTL/retention of `0` disables that half only.
- Safe to run in several replicas at once (each replica of the api
  Deployment runs one): concurrent sweeps can only delete rows that are
  anyway eligible; at worst one deletes zero rows.

## Consequences

### Easier

- The two append-only tables stop growing without bound; capacity
  planning reverts from "forever" to a function of traffic and TTL.
- ADR-0018's and ADR-0019's recorded retention gaps are closed by one
  small, fleet-proven type — the SQL and its batching behaviour are the
  same code inventory-storage already runs in production.
- Failures are non-fatal by construction: a failed pass is logged and
  retried on the next tick; the service never refuses to boot or serve
  because housekeeping failed.

### Harder / accepted

- One more background goroutine to reason about in the shutdown
  sequence; it shares the process context and stops with it, but it is
  another moving part.
- A deleted idempotency key changes retry semantics for very late
  retries: a retry after the TTL re-executes the handler. That is the
  documented, intended behaviour — the TTL IS the idempotency window —
  but clients must know the window is 24h by default, not infinite.
- The sweeper runs in every api replica; with HPA the same rows are
  considered by N sweepers. The subquery-bounded DELETE makes this
  merely wasteful, not wrong.

## Related

- ADR-0018 — transactional outbox (the published-rows retention half).
- ADR-0019 — Idempotency-Key middleware (the key-TTL half; its
  "known follow-up: no retention job" note is closed by this ADR).
- inventory-storage `internal/adapters/outbound/postgres/sweeper.go` —
  the fleet's reference implementation this mirrors.
