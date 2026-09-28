---
id: 0023-migrations-direct-postgres-connection
slug: /adr/0023-migrations-direct-postgres-connection
title: 23. Run golang-migrate against a direct Postgres connection, not PgBouncer
sidebar_label: 23. Migrations bypass PgBouncer
sidebar_position: 23
description: "ADR 0023 — Phase 4 fleet-wide finding, ported from order-management's reference implementation (PR #115, ADR-0029): golang-migrate's postgres driver takes a session-scoped pg_advisory_lock to serialize concurrent migration runs, which is incompatible with PgBouncer's transaction-pooling mode (warehouse-infra PR #43). Two or more facility-layout replicas of cmd/facility or cmd/mcp starting concurrently (HPA scale-out, or an ordinary rolling deploy) would crash-loop until one won the advisory-lock race. Fix: a second env var, MIGRATIONS_DATABASE_URL, carries a direct (non-pooled) connection string used ONLY for the migration step; DATABASE_URL/the runtime pgxpool is untouched and keeps going through PgBouncer."
---

# 23. Run golang-migrate against a direct Postgres connection, not PgBouncer

## Status

Accepted — implemented in the same change that introduces this record.
This is facility-layout's slice of a fleet-wide fix found during Phase 4
(k6/HPA load-test validation) cleanup. The reference implementation is
order-management's [PR #115](https://github.com/claudioed/order-management/pull/115)
and its own [ADR 0029](https://github.com/claudioed/order-management/blob/develop/docs/docs/adr/0029-migrations-direct-postgres-connection.md)
("Run golang-migrate against a direct Postgres connection, not
PgBouncer") — copied here with facility-layout's own composition roots
and chart shape, not order-management's, verified from this repo's actual
code rather than assumed by analogy.

## Context

warehouse-infra's PgBouncer rollout ([PR #43](https://github.com/claudioed/warehouse-infra/pull/43),
Phase 3) repointed every one of the fleet's 9 OLTP services' `DATABASE_URL`
secret — including facility-layout's — at PgBouncer
(`terraform/pgbouncer.tf`), in **transaction-pooling** mode
(`pool_mode = "transaction"`). That's the correct mode for this fleet's
steady-state traffic — application code never holds session state across
statements — and PR #43 already carved out one deliberate exception:
analytics DSNs (`analytics_database_urls` in `terraform/locals.tf`) were
left pointed directly at Postgres, because a single low-QPS analytics
consumer gets no pooling benefit and some analytics access patterns don't
tolerate transaction pooling. facility-layout's own `ANALYTICS_DATABASE_URL`
(`cmd/facility-projector`, `cmd/facility-reports`) already lives on that
carve-out and is untouched by this change.

What PR #43 did not carve out: **migrations**. Both of facility-layout's
OLTP composition roots that run migrations at startup —
`cmd/facility/main.go` (via `dialPostgres`) and `cmd/mcp/main.go` (via
`buildAdapters`) — call golang-migrate's postgres driver
(`github.com/golang-migrate/migrate/v4/database/postgres`,
`internal/adapters/outbound/postgres/migrate.go`'s `RunMigrations`)
against the same `DATABASE_URL`, before serving any traffic.
golang-migrate's postgres driver calls `SELECT pg_advisory_lock($1)` to
serialize concurrent migration runs — this is by design: if two processes
start at once and both try to run the same migration, whichever loses the
lock should block, not race.

`pg_advisory_lock` is **session-scoped**: the lock is held by whichever
physical backend connection issued it, and is expected to be released by
that same connection (or the session ending). PgBouncer's
transaction-pooling mode does not preserve that mapping — each statement
in a client's logical session can be routed to a different physical
backend connection, because the client's backend connection is returned
to the pool the instant its transaction commits. So:

- Pod A dials PgBouncer, gets backend connection #1, takes the advisory
  lock, runs migrations.
- Pod B dials PgBouncer *concurrently*, gets a **different** backend
  connection, and PgBouncer may freely reuse/rotate backend connections
  for either pod's subsequent statements mid-"session" from the
  application's point of view.
- The advisory lock never behaves as a real mutex across the two pods.
  Whichever pod's statements land on a backend connection with
  unexpected transaction/prepared-statement state gets errors like
  `pq: unnamed prepared statement does not exist` or `pq: canceling
  statement due to statement timeout`, and crash-loops for roughly 1-2
  minutes until the race resolves (one pod happens to hold real
  exclusivity long enough to finish).

This is a **latent, fleet-wide, production-blocking bug**, not a
load-test artifact: it fires on any ordinary rolling ArgoCD deploy with
more than 1 replica of `cmd/facility` or `cmd/mcp`, and on every HPA
scale-out event for either workload (ADR-0022 already gives `api` an HPA
with max 4). It was found and reproduced live against order-management
first; the root cause — every OLTP service in this fleet shares the same
`buildAdapters`/`dialPostgres` shape and the same PgBouncer-fronted
`DATABASE_URL` — applies identically to facility-layout.

## Decision

Give facility-layout a **second** connection string,
`MIGRATIONS_DATABASE_URL` — a direct (non-pooled, session-mode) Postgres
connection string, same user/password/dbname as `DATABASE_URL`, pointed
at Postgres itself rather than PgBouncer — used **only** for the
golang-migrate startup step in both `cmd/facility` and `cmd/mcp`.
`DATABASE_URL` and the pgxpool built from it are completely unchanged:
every request either binary serves still goes through PgBouncer in
transaction-pooling mode, exactly as PR #43 set up.

This is architecturally identical to the analytics-DSN carve-out PR #43
already made, and to universal Postgres/PgBouncer operational guidance:
**migrations need a direct/session connection; steady-state application
traffic goes through the pooler.** We are not weakening or changing
PgBouncer's `pool_mode` (still `transaction`, still correct for this
fleet's traffic) — this fix is entirely about routing one specific,
short-lived, startup-only operation around the pooler, not about
changing how the pooler behaves for anyone else.

`warehouse-infra`'s [PR #44](https://github.com/claudioed/warehouse-infra/pull/44)
already provisions `MIGRATIONS_DATABASE_URL` as a new key alongside the
existing `DATABASE_URL` key in each of the 9 OLTP services'
`kubernetes_secret.service_db` — including facility-layout's — so no
further warehouse-infra work is needed for this change; it is purely
additive to a secret key that already exists live in the cluster.

`cmd/facility/main.go` (`publisherConfigFromEnv`, threading
`publisherConfig.migrationsDatabaseURL` into `dialPostgres`) and
`cmd/mcp/main.go` (`buildAdapters`) now read `MIGRATIONS_DATABASE_URL` for
the migration step:

```go
migrationsDatabaseURL := getenv("MIGRATIONS_DATABASE_URL", databaseURL)
...
postgres.RunMigrations(migrationsDatabaseURL, migrationsPath)  // migrations only
// the pgxpool opened right after this still uses databaseURL, unchanged
```

The fallback to `databaseURL` when `MIGRATIONS_DATABASE_URL` is unset
keeps every environment that doesn't provision the split — local dev, CI
integration tests, or any cluster whose Terraform predates this fix —
working exactly as before, byte-for-byte. Nothing about local dev or CI
changes as a result of this PR.

The chart (`charts/facility-layout`) gets a new
`database.migrationsExistingSecretKey` value (default
`"MIGRATIONS_DATABASE_URL"`), rendering a `MIGRATIONS_DATABASE_URL` env
var sourced from the same `existingSecret` in both the `api`
(`templates/deployment.yaml`) and `mcp` (`templates/mcp-deployment.yaml`)
Deployments, with `optional: true` on the `secretKeyRef` — a secret that
predates this key still starts the pod, since the Go binary's own
`getenv` fallback then makes migrations run against `DATABASE_URL` exactly
as before this key existed. `cmd/facility-projector`/`cmd/facility-reports`
(the analytics binaries) are unaffected: they already use a dedicated,
never-pooled `ANALYTICS_DATABASE_URL` and do not read `MIGRATIONS_DATABASE_URL`.

### Why not just make PgBouncer's pool_mode session for this fleet?

Rejected. Session pooling would fix the advisory-lock problem but throws
away the entire point of PgBouncer for this fleet: transaction pooling
is what lets many short-lived HTTP-request-scoped OLTP connections share
a small number of physical Postgres backends. Switching to session mode
fleet-wide to accommodate a ~1-2 second startup-time lock call is the
tail wagging the dog.

### Why not just remove the advisory lock / skip migrations on non-leader replicas?

Rejected. golang-migrate's advisory lock is exactly the right mechanism
*given a session-scoped connection* — the bug is the mismatch between
that mechanism and the pooling mode we run migrations through, not the
mechanism itself. Bypassing it (e.g. an init-container Job that runs
migrations exactly once before any replica starts) was considered but
rejected for this fix, for the same reasons order-management's ADR-0029
rejected it: it's a bigger architectural change (a new Kubernetes
resource type per service, coordination with each Deployment's rollout
strategy) for the same outcome this two-line env-var fallback already
achieves, and it would still need a direct-vs-pooled connection decision
for the Job itself.

## Consequences

- **Fixes** the same fleet-wide crash-loop bug for facility-layout that
  order-management's ADR-0029 fixed there; both `cmd/facility` and
  `cmd/mcp` now run migrations over a direct connection.
- **No runtime behavior change**: `DATABASE_URL` is untouched, so
  request-serving connection pooling, `pool_mode`, and PgBouncer's own
  configuration are all unaffected by this PR.
- **No behavior change for environments without the split**: the
  `getenv("MIGRATIONS_DATABASE_URL", databaseURL)` fallback means local
  dev and CI integration tests keep using `DATABASE_URL` for everything,
  exactly as before.
- **Unblocks safely scaling `api` (ADR-0022's HPA, max 4) and any future
  `mcp` HPA**: an HPA scale-out is exactly the "2+ replicas start
  concurrently" trigger for this bug.
- One more secret key to keep in sync going forward; already
  mechanically provisioned by warehouse-infra PR #44 from the same
  `local.services` map as `DATABASE_URL`, so there's no new per-service
  manual step.

## Verification

`go build ./...`, `go vet ./...`, `make check`, `make check-all`, and
`make integration` all pass locally (see the PR for the full transcript).
New regression tests prove the fallback and the actual wiring:

- `TestMigrationsDatabaseURLFallback` (`cmd/facility`): the `getenv`
  fallback in both directions.
- `TestPublisherConfigFromEnv_ThreadsMigrationsDatabaseURL`
  (`cmd/facility`): `publisherConfigFromEnv` itself populates
  `publisherConfig.migrationsDatabaseURL` with the fallback value.
- `TestDialPostgres_UsesMigrationsDatabaseURLNotDatabaseURLForMigrations`
  (`cmd/facility`): `dialPostgres` threads `migrationsDatabaseURL` into
  the migration step, not `databaseURL` — proven with a schemeless
  `MIGRATIONS_DATABASE_URL` that produces a distinctive parse error a
  dial/"connection refused" error against `databaseURL` could never
  produce.
- `TestMigrationsDatabaseURLFallback` / `TestBuildAdapters_UsesMigrationsDatabaseURLNotDatabaseURLForMigrations`
  (`cmd/mcp`): the same two properties for `cmd/mcp/main.go`'s
  `buildAdapters`.

`helm lint charts/facility-layout` passes, and
`helm template ... --set database.existingSecret=facility-layout-db --set mcp.enabled=true | grep MIGRATIONS_DATABASE_URL`
confirms the env var, with `optional: true`, renders in both the `api`
and `mcp` Deployment templates.

Live cluster verification (forcing 2+ replicas of an OLTP service to
start concurrently against the PgBouncer-fronted `DATABASE_URL` with both
this fix and warehouse-infra PR #44 deployed) was already performed for
order-management, the reference implementation this ADR ports — see its
ADR-0029's Verification section for the full transcript (clean
concurrent-replica startup, `RESTARTS: 0`, no `pq:`/advisory/panic/fatal
log lines across two repeated runs). The fix here is the identical code
and chart shape applied to facility-layout's own composition roots.
