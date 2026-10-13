---
id: configuration
title: Configuration reference
sidebar_label: Configuration
---

# Configuration reference

facility-layout ships four binaries. Each one is configured only through
environment variables read in its composition root (`cmd/<binary>/main.go`)
plus a handful read by shared adapters. There are no config files and no
flags. This page has one table per binary and covers every variable a
production binary reads. Test-only variables (`BDD_TAGS`, `*_TEST_*`) are
left out.

How values are parsed:

- **String variables** go through each binary's `getenv(key, fallback)`. An
  unset **or empty** value falls back to the default.
- **`OUTBOX_RELAY_INTERVAL`** goes through `durationEnv`. An unset, malformed,
  zero or negative value falls back to the default, so the relay cannot be
  turned off this way.
- **`OUTBOX_MAX_ATTEMPTS`** goes through `intEnv`. Unset falls back to `20`;
  an explicit `0` is kept and means unlimited; a malformed or negative value
  logs `invalid integer env var, using default` at WARN and uses `20`, so a
  typo cannot silently remove the cap (ADR-0036).
- **Housekeeping knobs** (`HOUSEKEEPING_INTERVAL`, `IDEMPOTENCY_KEY_TTL`,
  `OUTBOX_RETENTION`) go through `durationEnvRaw`. Only an unset or malformed
  value falls back. An explicit `0`/`0s` is kept and disables that knob
  (ADR-0026).
- Durations use Go `time.ParseDuration` syntax (`500ms`, `30s`, `1h`, `168h`).

## Shared by every binary

All four binaries call `telemetry.Setup` and `telemetry.NewLogger`
(`internal/adapters/outbound/telemetry`), so each one reads these variables.
Only the default `OTEL_SERVICE_NAME` differs between binaries.

| Variable | Default | Required | Meaning | Source |
|---|---|---|---|---|
| `OTEL_SERVICE_NAME` | `facility-layout` (facility), `facility-projector`, `facility-reports`, `facility-layout-mcp` (mcp) | no | `service.name` resource attribute on every span, metric and log record. Also used as the otelchi server name. | `cmd/*/main.go` |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute. In `cmd/facility` a value injected at build time with `-ldflags "-X main.serviceVersion=..."` wins over this variable. | `cmd/*/main.go` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | OTLP/gRPC Collector endpoint for traces, metrics and logs. Both `host:port` and `http(s)://host:port` work, because the scheme and a trailing `/` are stripped. Export is insecure (no TLS) and non-blocking: if the Collector is unreachable, telemetry is dropped and the process still starts. | `cmd/*/main.go`, `internal/adapters/outbound/telemetry/telemetry.go` |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute. | `internal/adapters/outbound/telemetry/telemetry.go` |
| `LOG_LEVEL` | `info` | no | Minimum level for the JSON slog logger (`debug`, `info`, `warn` or `warning`, `error`; case-insensitive; anything else means `info`). | `cmd/*/main.go` |

## `facility` (REST API, outbox relay, housekeeping sweeper)

| Variable | Default | Required | Meaning | Source |
|---|---|---|---|---|
| `HTTP_ADDR` | `:8080` | no | Listen address of the REST API. Also serves `/healthz` and `/readyz`. | `cmd/facility/main.go` |
| `DATABASE_URL` | *(unset)* | no (yes in any real deployment) | OLTP Postgres DSN for the pgx pool (`MaxConns` 10, `statement_timeout` 5s, both fixed in `internal/adapters/outbound/postgres/pool.go`). **When unset, the binary uses in-memory repositories.** There is then no outbox, no sweeper and no idempotency store, and all data is lost on restart. | `cmd/facility/main.go` |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct (non-PgBouncer, session-mode) DSN used **only** by the golang-migrate step at boot. The step needs a session-scoped `pg_advisory_lock`, which PgBouncer transaction pooling breaks (ADR-0023). | `cmd/facility/main.go` |
| `MIGRATIONS_PATH` | `migrations` | no | Directory of the OLTP SQL migrations, relative to the working directory. | `cmd/facility/main.go` |
| `EVENT_PUBLISHER` | *(empty → log publisher)* | no | `kafka` turns on Kafka publishing. Any other value, including unset, logs events instead of publishing them. See the mode matrix below. | `cmd/facility/main.go` |
| `KAFKA_BROKERS` | `localhost:9092` | no | Comma-separated bootstrap brokers for the integration publisher, analytics publisher and outbox relay sink. Read only when `EVENT_PUBLISHER=kafka`. | `cmd/facility/main.go` |
| `OUTBOX_RELAY_INTERVAL` | `1s` | no | Sleep between empty passes of the outbox relay (ADR-0018). Zero, negative or malformed values fall back to `1s`. | `cmd/facility/main.go` |
| `OUTBOX_MAX_ATTEMPTS` | `20` | no | Outbox relay attempt cap ([ADR-0036](../adr/0036-outbox-relay-attempt-cap.md)): a row that has failed this many sends is excluded from the relay's claims and logged once at ERROR (`outbox_id`, `topic`, `event_type`, `key`). It stays in `outbox_events` until re-armed (see [Runbook](./runbook.md#an-outbox-row-hit-the-attempt-cap)); later rows, including rows with the same key, overtake it. `0` = unlimited (one poison row blocks the relay forever). A malformed or negative value logs a WARN and uses `20`. Read only when the relay runs (`DATABASE_URL` + `EVENT_PUBLISHER=kafka`). | `cmd/facility/main.go`, `internal/adapters/outbound/postgres/outbox_relay.go` |
| `HOUSEKEEPING_INTERVAL` | `1h` | no | How often the housekeeping sweeper wakes up (ADR-0026). `0` disables the sweeper. | `cmd/facility/main.go`, `internal/adapters/outbound/postgres/sweeper.go` |
| `IDEMPOTENCY_KEY_TTL` | `24h` | no | Age after which `idempotency_keys` rows are deleted. This bounds how late a client retry still gets the cached response (ADR-0019). `0` disables this half of the sweep. | same |
| `OUTBOX_RETENTION` | `168h` (7 days) | no | Age after which **published** `outbox_events` rows are deleted. Unpublished rows are never swept. `0` disables this half of the sweep. | same |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:5173,http://localhost:5186` | no | Comma-separated browser origins allowed by the CORS middleware. The defaults are the warehouse-console shell's dev port and this service's MFE remote dev port. Allowed methods are GET/POST/PUT/DELETE. Allowed headers are `Content-Type`, `Authorization` and `Idempotency-Key`. | `internal/adapters/inbound/http/server.go` |

Publisher mode matrix (`buildAdapters` in `cmd/facility/main.go`):

| `DATABASE_URL` | `EVENT_PUBLISHER` | Publisher wired | Outbox relay |
|---|---|---|---|
| unset | unset / anything but `kafka` | log publisher | no |
| unset | `kafka` | direct Kafka fan-out to both topics, no outbox | no |
| set | unset / anything but `kafka` | log publisher | no |
| set | `kafka` | transactional outbox, fanned out to `warehouse.facility.events` and `warehouse.facility.analytics` | yes |

## `facility-projector` (analytics writer)

| Variable | Default | Required | Meaning | Source |
|---|---|---|---|---|
| `ADMIN_ADDR` | `:8091` | no | Listen address of the admin server (`/healthz`, `/readyz` only). | `cmd/facility-projector/main.go` |
| `ANALYTICS_DATABASE_URL` | *(none)* | **yes** | Analytical Postgres DSN (pool `MaxConns` 5). If unset, the binary exits immediately with `ANALYTICS_DATABASE_URL is required`. The same DSN runs the analytical migrations, so it must be a direct, session-capable connection. | `cmd/facility-projector/main.go` |
| `KAFKA_BROKERS` | `localhost:9092` | no | Comma-separated brokers for the `warehouse.facility.analytics` consumer (group `facility-analytics`) and its DLQ writer (`warehouse.facility.analytics.dlq`). | `cmd/facility-projector/main.go` |
| `ANALYTICS_MIGRATIONS_PATH` | `migrations/analytics` | no | Directory of the analytical schema migrations. The projector is the only binary that runs them. | `cmd/facility-projector/main.go` |

## `facility-reports` (analytics reader)

| Variable | Default | Required | Meaning | Source |
|---|---|---|---|---|
| `HTTP_ADDR` | `:8092` | no | Listen address of the reports API (`/reports/catalog-growth`, `/reports/catalog-growth/freshness`, `/healthz`, `/readyz`). | `cmd/facility-reports/main.go` |
| `ANALYTICS_DATABASE_URL` | *(none)* | **yes** | Analytical Postgres DSN. If unset, the binary exits immediately with `ANALYTICS_DATABASE_URL is required`. The pool is opened read-only (`default_transaction_read_only=on`, `MaxConns` 5), so give it a read-only role. This binary never runs migrations. | `cmd/facility-reports/main.go`, `internal/adapters/outbound/analyticsstore/pool.go` |

`facility-reports` does **not** install the CORS middleware, so
`CORS_ALLOWED_ORIGINS` has no effect on it.

## `mcp` (MCP server, Streamable HTTP)

| Variable | Default | Required | Meaning | Source |
|---|---|---|---|---|
| `MCP_ADDR` | `:8090` | no | Listen address. The MCP Streamable HTTP endpoint is mounted at both `/` and `/mcp`, and `/healthz` answers liveness. | `cmd/mcp/main.go` |
| `DATABASE_URL` | *(unset)* | no (yes in any real deployment) | OLTP Postgres DSN (same database as `facility`, read-only use cases only). **When unset, the binary uses its own empty in-memory store**, which is not shared with a separately running `facility` process. | `cmd/mcp/main.go` |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the migration step. The MCP binary also runs the OLTP migrations at boot (ADR-0023). | `cmd/mcp/main.go` |
| `MIGRATIONS_PATH` | `migrations` | no | Directory of the OLTP SQL migrations. | `cmd/mcp/main.go` |
| `REPORTS_BASE_URL` | *(unset)* | no | Base URL of `facility-reports`, for example `http://facility-reports:8092`. When set, the catalog-growth report tool is registered and calls the reports REST API. When unset, that tool is not registered at all. | `cmd/mcp/main.go` |

## Boot-time behaviour that depends on configuration

- **Boot retry.** Each binary retries its first Postgres operations (running
  migrations, then pinging the pool) through `internal/adapters/outbound/bootretry`:
  5 attempts with backoff delays of 1s, 2s, 4s and 8s, so **15s of waiting
  in total** before it gives up and exits with the last real error. No delay
  follows the fifth attempt, so the real budget is 15s plus the duration of
  the attempts. The retry covers the Istio native-sidecar first-dial reset
  (ADR-0028).
- **Fail-closed analytics.** `facility-projector` and `facility-reports`
  refuse to start without `ANALYTICS_DATABASE_URL`. `facility` and `mcp`
  instead fall back to in-memory storage when `DATABASE_URL` is empty. That
  fallback is convenient locally and wrong in a cluster, so check the
  `database url not configured; using in-memory adapters` log line after
  every deploy.
- **Kafka is only dialled when needed.** `facility` creates no Kafka writer
  unless `EVENT_PUBLISHER=kafka`. Brokers are not pinged at boot. A
  broker that cannot be reached shows up later as outbox relay errors (see
  [Troubleshooting](./troubleshooting.md)).

See also: [Runbook](./runbook.md) for how the Helm chart sets these
variables, and [Observability](./observability.md) for the telemetry they
control.

## How the Helm chart delivers `facility`'s variables

`charts/facility-layout/templates/configmap.yaml` renders `config.*` (plus
`kafka.brokers` when `kafka.enabled`) into a ConfigMap named after the
release: `HTTP_ADDR`, `MIGRATIONS_PATH`, `EVENT_PUBLISHER`,
`OUTBOX_RELAY_INTERVAL`, `OUTBOX_MAX_ATTEMPTS` (`config.outboxMaxAttempts`,
default `20`), `HOUSEKEEPING_INTERVAL`, `IDEMPOTENCY_KEY_TTL`,
`OUTBOX_RETENTION`, `KAFKA_BROKERS`. The api Deployment mounts it with
`envFrom: configMapRef` (gap fix D14, 2026-10-12), so every `config.*` value
reaches `cmd/facility`. Explicit `env` entries (the same values, plus the
Secret-backed DSNs and the OTel variables) and `extraEnv` win over the
ConfigMap for the same key. The projector, reports and MCP Deployments do not
mount the ConfigMap; their variables are set explicitly in their own
templates.
