---
id: 0022-horizontal-autoscaling-and-pgxpool-tuning
slug: /adr/0022-horizontal-autoscaling-and-pgxpool-tuning
title: 22. Per-workload HorizontalPodAutoscaler and pgxpool MaxConns/statement_timeout tuning
sidebar_label: 22. HPA + pgxpool tuning
sidebar_position: 22
description: "ADR 0022 — Phase 3 (scalability) for facility-layout: an autoscaling/v2 HorizontalPodAutoscaler per independently-assessed workload (api max 4, analytics-projector max 2, analytics-reports max 3, frontend max 3; mcp explicitly excluded for a real in-memory-session reason), all default-disabled via values.yaml so this PR changes nothing on merge; plus explicit pgxpool.Config MaxConns caps and per-pool statement_timeout values, sized against the shared Postgres instance's real max_connections=100 ceiling and against warehouse-infra PR #43's PgBouncer front for the OLTP path. Ported from order-management PR #110 / ADR 0026, the fleet's reference implementation for this phase."
---

# 22. Per-workload HorizontalPodAutoscaler and pgxpool MaxConns/statement_timeout tuning

## Status

Accepted — implemented in the same change that introduces this record.
This is facility-layout's slice of Phase 3 (scalability) of the fleet
production-readiness plan, built on top of Phase 0 (boot-retry), Phase 1
(idempotency, ADR-0019), and Phase 2 (resilience, ADR-0020). The
reference implementation is order-management's
[PR #110](https://github.com/claudioed/order-management/pull/110) and its
own [ADR 0026](https://github.com/claudioed/order-management/blob/develop/docs/docs/adr/0026-horizontal-autoscaling-and-pgxpool-tuning.md)
("Per-workload HorizontalPodAutoscaler and pgxpool MaxConns/
statement_timeout tuning") — copied here with facility-layout's own
workload shapes and connection budget, not order-management's, verified
from this repo's actual code rather than assumed by analogy.

## Context

Before this change, facility-layout's Helm chart had exactly one
`HorizontalPodAutoscaler` template, unconditionally targeting the `api`
Deployment only, wired to a flat `autoscaling.enabled/minReplicas/
maxReplicas/targetCPUUtilizationPercentage` block — untested against the
other three Deployments this chart renders (`mcp`, `frontend`,
`analytics-projector`, `analytics-reports`), and never assessed for
whether scaling `analytics-projector` past 1 replica was even safe given
its Kafka consumer-group membership. `replicaCount` was hardcoded to 1
everywhere with no HPA anywhere else in the chart.

Separately, no pool in this codebase set an explicit
`pgxpool.Config.MaxConns`: the OLTP pool
(`internal/adapters/outbound/postgres/pool.go`, used by `cmd/facility`
and `cmd/mcp`) and the two analytics pools
(`internal/adapters/outbound/analyticsstore/pool.go`'s `NewPool` used by
`cmd/facility-projector`, and `NewReadOnlyPool` used by
`cmd/facility-reports`) all ran on pgx's library default,
`max(4, runtime.NumCPU())` connections per process. No pool set a
`statement_timeout` either, so a single runaway query could hold a
pooled connection indefinitely, with nothing to cancel it.

This matters together, not separately: turning on HPA for a
Postgres-backed workload without an explicit, bounded `MaxConns` means
the service's real connection ceiling becomes "however many CPUs the
node happens to have, times however many replicas the HPA happens to
have scaled to" — an unbounded, indirect function of cluster
autoscaling and CPU load, not a number anyone chose. This PR does both
together on purpose.

### Finding: shared Postgres, PgBouncer already fronting OLTP, and the real max_connections

Checked before picking any number, not assumed:
`warehouse-infra/terraform/postgres.tf` provisions a single Postgres
StatefulSet/service per environment — there is no per-service Postgres
instance or database-per-tenant split. Every service's OLTP and
analytics databases live in the SAME running Postgres server, each with
its own logical database, not its own Postgres process.
`max_connections=100` is the unmodified Bitnami chart default —
`warehouse-infra`'s Terraform has no explicit `max_connections`/
`postgresql.conf` override anywhere.

Also already true on `develop`, and load-bearing for this ADR's
`MaxConns` accounting: `warehouse-infra`
[PR #43](https://github.com/claudioed/warehouse-infra/pull/43) (merged)
put a PgBouncer instance, in transaction-pooling mode, in front of the
shared Postgres for OLTP traffic, and re-pointed every fleet service's
`DATABASE_URL` Secret — including this service's — at PgBouncer instead
of Postgres directly. That re-pointing needed no code change here (the
DSN is opaque to `postgres.NewPool`); this ADR's OLTP `MaxConns` bounds
each `cmd/facility`/`cmd/mcp` process's own demand on PgBouncer's
client-facing pool, not a direct server-side Postgres connection slot —
PgBouncer is what absorbs the real fleet-wide multiplexing down to a
much smaller number of actual backend connections. Per PR #43's own
documented reasoning, the two analytics DSNs
(`ANALYTICS_DATABASE_URL`, writer and reader) stay pointed directly at
Postgres, not through PgBouncer: low QPS, one long-lived Kafka-consumer
connection each (writer) or a small bounded pool (reader), no pooling
benefit from an extra hop. Confirmed directly against this repo's
`charts/facility-layout/values.yaml` and the analytics DSN wiring in
`internal/adapters/outbound/analyticsstore` before writing this record.

## Decision

### 1. HorizontalPodAutoscaler — one per independently-assessed workload

Assessed each of the four Deployments this chart renders on its own
merits — statefulness, and (for the one Kafka consumer) consumer-group-id
convention — rather than blanket-enabling HPA everywhere:

| Deployment | HPA? | min | max | target CPU | Why |
|---|---|---|---|---|---|
| `api` (`cmd/facility`) | Yes | 1 | 4 | 70% | Stateless OLTP HTTP. `cmd/facility/main.go` confirms it has **no inbound Kafka consumer at all** — this service is purely an outbound Kafka publisher (an Open Host Service, ADR 0009), so there is no consumer-group-collision class of risk to assess for this workload; unconditionally safe to run N independent copies of. |
| `analytics-projector` (`cmd/facility-projector`) | Yes, capped at **2**, not api's 4 | 1 | 2 | 70% | Its analytics Kafka consumer group, `kafka.AnalyticsConsumerGroup = "facility-analytics"` (`internal/adapters/inbound/kafka/analytics_consumer.go`), is a **stable, shared** group name with no per-instance uniqueness — the fleet's normal horizontally-scalable pattern, N replicas share partitions via ordinary Kafka group rebalancing. Every projection write is idempotent on `event_id` via `ConsumedEventsRepo.MarkProcessed` (`ON CONFLICT DO NOTHING`), so correctness does not regress at N>1. Capped at 2 rather than left at 4: this is a lightweight idempotent-upsert workload where a wide fan-out buys little extra throughput, mirroring order-management's identical reasoning for its own projector. |
| `analytics-reports` (`cmd/facility-reports`) | Yes | 1 | 3 | 70% | Stateless read-only REST reader over its own read-only pgxpool (`analyticsstore.NewReadOnlyPool`) — no in-memory state, no Kafka consumption. Same treatment as `api`. |
| `frontend` (nginx-unprivileged serving the built SPA bundle) | Yes | 1 | 3 | 70% | Pure static-asset serving. No server-side session, no per-request state. The most trivially horizontally-scalable workload in this chart. |
| `mcp` (`cmd/mcp`) | **No — deliberately excluded, not just disabled** | — | — | — | The `github.com/modelcontextprotocol/go-sdk` `StreamableHTTPHandler` this adapter wraps (`internal/adapters/inbound/mcp/server.go`) keeps per-process, in-memory session state keyed by the MCP protocol's own `Mcp-Session-Id` header — a real multi-request session, not just a TCP/HTTP connection. `charts/facility-layout/templates/mcp-service.yaml` is a plain `ClusterIP` Service with no `sessionAffinity` configured, so under >1 replica a second request carrying the same `Mcp-Session-Id` (e.g. `tools/call` following an earlier `initialize`) could land on a pod that never created that session. This is the identical risk order-management's ADR-0026 documented for its own `mcp` Deployment. Fixing it for real needs either `sessionAffinity: ClientIP` (a partial mitigation only) or wiring the SDK's session store to something shared/external — a real code change, out of scope for this chart-and-pool-tuning PR. `mcp.replicaCount` stays a plain, manually-set value; no `autoscaling.mcp` block exists in `values.yaml` at all. Revisit if/when `cmd/mcp` adopts an external session store or the SDK's stateless mode. |

Every enabled block is namespaced under a single top-level
`autoscaling:` key in `values.yaml`
(`autoscaling.<api|projector|reports|frontend>.{enabled,minReplicas,
maxReplicas,targetCPUUtilizationPercentage}`), **every `enabled` value
defaults to `false`**. This PR makes per-workload HPA possible and
verified-correct; it deliberately does not turn any of it on — the
fleet enables each workload's HPA later, once, as a conscious rollout
decision.

**No replicas-vs-HPA fight.** Each Deployment template guards its
`spec.replicas` field with `{{- if not .Values.autoscaling.<x>.enabled
}}` — when a workload's HPA is enabled, its Deployment renders with NO
`replicas` field at all (a hardcoded `replicas:` next to an active HPA
would otherwise fight it on every reconcile). Verified directly with
`helm template`:

- Default values → 0 `HorizontalPodAutoscaler` resources render, every
  Deployment (including `mcp`) keeps its static `replicas:` field.
- All four `autoscaling.*.enabled=true` → exactly 4
  `HorizontalPodAutoscaler` resources render (one per scalable
  workload, `mcp` has none by design), and none of those four
  Deployments has a `replicas:` field — `mcp`'s Deployment still does.

`helm lint` passes; `go vet`/`gofmt`/`make check`/`make arch-test` all
pass unchanged (no Go code path is affected by the chart changes).

### 2. pgxpool MaxConns

All three pools now set an explicit `pgxpool.Config.MaxConns` instead of
inheriting the CPU-derived library default:

| Pool | Used by | `MaxConns` | Reasoning |
|---|---|---|---|
| OLTP (`postgres.NewPool`) | `cmd/facility` (`api`), `cmd/mcp` (`mcp`) | **10** | `api`'s HPA ceiling of 4 replicas × 10 = 40 connections against PgBouncer's client-facing pool (warehouse-infra PR #43 — this DSN is already re-pointed at PgBouncer, not raw Postgres), leaving PgBouncer's own transaction-pooling multiplexing to absorb the real backend-connection count against the shared instance. |
| Analytics writer (`analyticsstore.NewPool`) | `cmd/facility-projector` | **5** | The projector's Kafka consumer group is stable/shared (see above) but even at its HPA ceiling of 2 replicas, a small flat pool is enough: writes are single-row `ON CONFLICT` upserts against one `(scope, day_bucket)` key at a time. Analytics DSNs stay direct to Postgres, not through PgBouncer, per PR #43's documented reasoning (low QPS, no pooling benefit). |
| Analytics reader (`analyticsstore.NewReadOnlyPool`) | `cmd/facility-reports` | **5** (`ReportsMaxConns`) | `reports` IS HPA-scalable (max 3); at that ceiling, 3 × 5 = 15 connections direct against the analytical database. |

Worst case across every workload simultaneously at its proposed HPA
maximum, direct against the shared Postgres instance (`api`'s 40 goes
through PgBouncer, not counted against `max_connections` at 1:1 —
PgBouncer's own backend pool size, a `warehouse-infra` concern, is what
bounds that): `projector` fixed at 1 × 5 = 5 (HPA disabled by default
today), `reports` 3 × 5 = 15, `mcp` at 1 manually-set replica × 10 = 10
direct (until PgBouncer is fronting it too, its DSN goes through the
same re-pointed Secret as `api` per PR #43, so this 10 is also through
PgBouncer, not direct) — the only genuinely direct-to-Postgres load
from this service today is the two analytics pools, 5 + 5 = 10 of the
shared instance's 100 connections even at every proposed ceiling
simultaneously. This is a materially smaller direct-Postgres footprint
than order-management's pre-PgBouncer accounting, precisely because
PgBouncer (PR #43) already absorbs this service's OLTP multiplexing.

### 3. statement_timeout

All three pools set `statement_timeout` via `pgxpool.Config.
AfterConnect`, running `SET statement_timeout = '<value>'` on every new
physical connection as it's established (not per-query, so it survives
connection reuse across pooled acquisitions):

| Pool | `statement_timeout` | Reasoning |
|---|---|---|
| OLTP (`postgres.StatementTimeout`) | **5s** | Every OLTP query (Site/Zone/Aisle/Slot reads and writes, keyed by id or location code) is a single-aggregate operation, normally low-single-digit milliseconds. 5s is generous headroom for real transient contention without ever being a normal-path concern. |
| Analytics writer (`analyticsstore.StatementTimeout`) | **10s** | A Kafka consumer replaying a backlog after a redeploy issues upserts in a tight loop; a transient lock wait here doesn't need to be as tight as an interactive OLTP request, but the projector has no replica to fail over to at its default (fixed at 1), so an unbounded query would stall the entire analytics pipeline. |
| Analytics reader (`analyticsstore.ReportsStatementTimeout`) | **15s** | The catalog-growth report aggregates rows across a caller-chosen date range — wider than the OLTP side's always-single-aggregate-by-id shape — so it gets more headroom, but still a hard ceiling. |

Verified with a real Postgres via testcontainers
(`internal/adapters/outbound/postgres/pool_limits_integration_test.go`,
`-tags=integration`), not a mock and not just reading `pg_settings`:

- `TestNewPool_AppliesStatementTimeoutToNewConnections` — opens a pool
  against a real `postgres:16-alpine` container with a short
  test-only timeout (200ms, via the shared `NewPoolWithLimits` the
  production `NewPool` wraps), confirms `SHOW statement_timeout` reads
  back `200ms` on a freshly acquired connection, then runs `SELECT
  pg_sleep(2)` and asserts Postgres itself cancels it (SQLSTATE 57014,
  "canceling statement due to statement timeout") rather than letting
  it run the full 2s — proving the setting is genuinely enforced
  server-side, not merely set and ignored — and finally confirms the
  pool is still usable afterward (the cancelled statement doesn't
  poison the connection).
- `TestNewPool_AppliesMaxConns` — acquires exactly `maxConns`
  connections from a pool configured with `MaxConns=2`, then asserts a
  further `Acquire` blocks until `context.DeadlineExceeded`, proving
  `MaxConns` is the pool's real, enforced ceiling rather than advisory.

Both tests pass locally against a real Postgres container.

## Consequences

- HPA is now possible, correct, and independently verified per
  workload for three of this chart's four HPA-eligible Deployments plus
  frontend — but **off by default everywhere**. Merging this PR
  changes nothing about production replica counts; `MaxConns`/
  `statement_timeout` are the only behavior change that takes effect on
  deploy, and both are conservative relative to today's unbounded
  defaults (they can only reduce, never increase, worst-case connection
  usage and hung-query duration).
- `mcp` remains explicitly un-autoscaled, with the exact reason
  (in-memory session state, no sticky routing) recorded here and in
  `values.yaml`'s comments, so a future contributor doesn't
  mechanically copy `api`'s HPA block onto it without re-solving the
  session-affinity problem first.
- This service's OLTP connection footprint against the shared Postgres
  instance is already smaller than a naive "MaxConns × HPA ceiling"
  count would suggest, because PgBouncer (warehouse-infra PR #43)
  already sits between `cmd/facility`/`cmd/mcp` and Postgres in
  transaction-pooling mode. This ADR's `MaxConns=10` bounds each
  process's demand on PgBouncer's client-facing pool; PgBouncer's own
  backend-connection sizing is a `warehouse-infra` concern, out of
  scope here.
- `max_connections=100` itself is an unexamined Bitnami chart default,
  not a value anyone has deliberately sized for this fleet's real
  demand. This ADR treats it as a hard external constraint for the
  direct-to-Postgres analytics pools to work within, not something in
  scope to change.
- Sourced from and matches the shape of order-management PR #110 / ADR
  0026, the fleet's Phase 3 reference implementation, adapted to this
  service's own workload facts (no inbound Kafka consumer on `api`;
  PgBouncer already fronting the OLTP DSN) rather than copied blind.
