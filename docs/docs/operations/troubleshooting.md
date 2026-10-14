---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
---

# Troubleshooting

Each row below follows a failure mode that exists in the code, from the symptom
you see to the cause, how to confirm it, and the fix. Log messages are quoted
exactly as the binaries emit them (see [Observability](./observability.md)).
Configuration names are in [Configuration](./configuration.md).

## Boot and readiness

| Symptom | Cause | Check | Fix |
|---|---|---|---|
| Pod in `CrashLoopBackOff`, never Ready | Postgres unreachable or migrations failing. Boot retry gives up after 5 attempts (1+2+4+8 = 15s of backoff) and the process exits 1. `/readyz` itself never checks dependencies. | Last log line: `service exited with error` (facility) / `mcp server exited with error`, with `run migrations (after 5 attempts): …` or `ping database (after 5 attempts): …` | Fix the DSN, network policy or Secret. For migration errors see the next row. |
| `pq: unnamed prepared statement does not exist` or `canceling statement due to statement timeout` during `run migrations`, with replicas racing | Migrations went through PgBouncer (transaction pooling), so the advisory lock is not a real mutex (ADR-0023) | Is `MIGRATIONS_DATABASE_URL` set in the pod? The chart reads it with `optional: true`, so a Secret without the key silently falls back to `DATABASE_URL`. | Add the direct DSN under the `MIGRATIONS_DATABASE_URL` key of the database Secret, then restart. |
| golang-migrate reports a dirty database version | A previous migration failed halfway | `SELECT * FROM schema_migrations;` shows `dirty = true` | Repair the partial change by hand, set `dirty = false` at the last good version, then restart. The pods re-run pending migrations. |
| `facility-projector` / `facility-reports` exit immediately with `ANALYTICS_DATABASE_URL is required` | The analytics Secret is missing or `analytics.database.*` is empty | `kubectl describe pod` env | Set `analytics.database.projectorUrl` / `reportsUrl` or `existingSecret` (keys `ANALYTICS_DATABASE_URL`, `ANALYTICS_READER_DATABASE_URL`). |
| API "works" but data disappears on restart, and MCP shows none of the API's data | `DATABASE_URL` is unset, so each binary uses its **own** in-memory store | Log line `database url not configured; using in-memory adapters` | Set `database.url` or `database.existingSecret`. |
| Startup probe kills the pod during boot | Boot (retry plus migrations) took longer than 60s (2s × 30) | Events: `Startup probe failed` | Usually a symptom of the first row. Raise `startupProbe.failureThreshold` only if migrations are legitimately slow. |
| Telemetry missing, no errors visible | Collector unreachable: export errors are logged only at **debug** | Run with `LOG_LEVEL=debug` and look for `opentelemetry error` | Fix `OTEL_EXPORTER_OTLP_ENDPOINT`. Both `host:4317` and `http://host:4317` work. |

## Events not reaching Kafka

| Symptom | Cause | Check | Fix |
|---|---|---|---|
| inventory-storage / warehouse-planning never see new slots, and there is nothing on `warehouse.facility.events` | `EVENT_PUBLISHER` is not `kafka`. The chart default is `config.eventPublisher: "log"`, which writes **no** outbox rows. | Log line `event publisher configured` is absent. Events show up in logs from the log publisher instead. | Set `config.eventPublisher=kafka` and `kafka.enabled=true`. |
| `outbox_events` rows with `published_at IS NULL` piling up | The relay cannot send: broker down, wrong `KAFKA_BROKERS`, or topic or ACL problems | `SELECT id, event_type, attempts, last_error FROM outbox_events WHERE published_at IS NULL ORDER BY id LIMIT 20;` and logs `outbox relay pass failed` | Fix broker connectivity. The relay retries every `OUTBOX_RELAY_INTERVAL` and resumes in order. No data is lost. |
| A consumer's span is not linked to the facility-layout request that caused the event | The row was written with no active span (a background job, or a request outside otelchi) or before migration `0010`. Such rows carry no trace context (ADR-0038) | `SELECT id, event_type, headers FROM outbox_events WHERE event_type = '<type>' ORDER BY id DESC LIMIT 5;` shows `headers` NULL | Expected for those rows. For new rows, check that the use case runs under the request's `ctx`, and that the consumer calls `Extract` before it starts its span. |
| One row with high `attempts` blocks everything behind it | The relay stops a pass at the first failure to keep per-aggregate order, until the row reaches `OUTBOX_MAX_ATTEMPTS` (default 20; with `0` it blocks forever) | Same query: the lowest-id unpublished row has a growing `attempts` | Fix the cause in `last_error`. Do not delete the row unless you accept losing that event. If the cap is `0`, set `OUTBOX_MAX_ATTEMPTS` (ADR-0036). |
| ERROR `outbox relay: row reached the attempt cap and is excluded from future claims` | A row failed `OUTBOX_MAX_ATTEMPTS` sends ([ADR-0036](../adr/0036-outbox-relay-attempt-cap.md)); later rows, including same-key ones, now overtake it and consumers never got it | `SELECT id, topic, event_type, key, attempts, last_error FROM outbox_events WHERE published_at IS NULL AND attempts >= 20;` (use the configured cap) | Fix the cause, then `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;` ([Runbook](./runbook.md#an-outbox-row-hit-the-attempt-cap)). The event is then delivered late, with its original CloudEvents `id`. |
| Consumers see duplicate events | Delivery is at-least-once: a crash between send and mark republishes the event | Same CloudEvents `id` on two messages | Expected. Consumers must dedupe on `id`. |
| network-inventory-planning refuses every transfer for a site / never sees `SiteCapabilityChanged` | The site's capability was never set: a site starts capable of nothing at `capabilityRevision` 0 and nothing is published until the first `PUT` (ADR-0035) | `GET /sites/{siteCode}/transfer-capability` shows `capabilityRevision: 0`; `SELECT event_type, published_at FROM outbox_events WHERE event_type LIKE '%SiteCapabilityChanged' ORDER BY id DESC LIMIT 5;` | `PUT /sites/{siteCode}/transfer-capability` with both flags. Repeating the current snapshot publishes nothing, by design. |
| A site shows `siteRole: UNSPECIFIED` after the upgrade | Migration `0009` only classified names matching `<CODE> - <Role words> (<city>)` with one of the 15 role phrases (or the whole name `Simulated FC`); anything else stays `UNSPECIFIED` (ADR-0037) | `SELECT code, name FROM sites WHERE site_role = 'UNSPECIFIED';` | `PUT /sites/{siteCode}/role` with the right role. Renaming the site does not reclassify it: the name is display text only. |
| A role change did not enable/disable transfers | By design: a role never flips capability (ADR-0037); capability is only `PUT /sites/{siteCode}/transfer-capability` (ADR-0035) | `GET /sites/{siteCode}/transfer-capability` | Apply the role's row of `GET /site-roles/transfer-defaults` with the capability PUT. |
| Tuning `config.outboxRelayInterval` / housekeeping values in Helm does nothing | A chart older than gap fix D14 (2026-10-12) renders the ConfigMap but mounts it in no Deployment. Current charts mount it in the api Deployment with `envFrom`. | `kubectl get deploy <fullname> -o yaml` shows no `envFrom`/`configMapRef`; `kubectl exec … env` does not show `OUTBOX_RELAY_INTERVAL` | Upgrade the chart. On an old chart, pass the variables through `extraEnv`. An `extraEnv` entry wins over the ConfigMap for the same key. |

## Analytics pipeline

| Symptom | Cause | Check | Fix |
|---|---|---|---|
| Report stops moving, `lagSeconds` keeps growing | The projector's consume loop ended after a commit or DLQ write failure. The pod is still Live. | Log `analytics consumer stopped` | Fix Kafka connectivity, then `kubectl rollout restart deploy/<fullname>-projector`. |
| Messages on `warehouse.facility.analytics.dlq` | Projection failed 3 times (`analytics: exhausted retries…`), or the message was not a CloudEvent (`analytics: message is not a valid CloudEvent…`) | Read the DLQ message headers `x-dlq-error`, `x-dlq-source-topic`, `x-dlq-failed-at` | Fix the cause (analytical DB, schema), then re-produce the body onto the source topic ([Runbook](./runbook.md#routine-procedures)). |
| A brand-new projector replays the whole history | By design: a new group starts at the earliest offset | Group `facility-analytics` offsets | Expected. Idempotency on the event id makes it safe. |
| `GET /reports/catalog-growth` → 400 `invalid-report-query` | `from`/`to` missing or not RFC3339, or `granularity` is not `day` | Problem `detail` | Send `from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z`. |
| `GET /reports/...` → 500 `report-store-error` | Analytical DB unreachable or schema missing (the projector never ran migrations) | Problem `detail` | Start the projector first, since it owns the analytical schema. |
| MCP has no catalog-growth tool | `REPORTS_BASE_URL` is unset (the chart only sets it when `analytics.enabled` or `mcp.reportsBaseUrl` is set) | Log `catalog-growth report tool wired` is absent | Enable analytics or set `mcp.reportsBaseUrl`. |

## REST problem responses

All errors are RFC 7807 `application/problem+json`. The `type` ends in the
slug below (`internal/adapters/inbound/http/errors.go`, `idempotency.go`).
There is no `If-Match`/ETag handling, so the API never returns **412**.
Optimistic concurrency surfaces as a 409.

| Status | Slug(s) | Meaning / fix |
|---|---|---|
| 400 | `idempotency-key-required` | A resource-creation POST (sites, zones, aisles, location types, placement rules, locations, fixed structures, cross-aisles) was sent without `Idempotency-Key` while Postgres is configured. Send a fresh UUID per logical request. Bulk import and decommission are exempt (ADR-0019). Browser clients: the CORS allow-list includes the header. |
| 422 | `idempotency-key-reused` | Same key, **different** body (SHA-256 of the body differs). Use a new key. Same key with the same body replays the cached status, headers and body. Keys live for `IDEMPOTENCY_KEY_TTL` (24h), after which a reused key is treated as new. |
| 400 | `malformed-request-body`, `malformed-location-code`, `invalid-site-code`, `invalid-zone-code`, `invalid-aisle-code`, `invalid-location-type`, `invalid-layout-format` | Syntax or shape errors. Read `detail`. Location codes follow `SITE-AREA-ZONE-AISLE-BAY-LEVEL-POSITION` (see [Location code](../business-context/location-code.md)). |
| 404 | `site-not-found`, `zone-not-found`, `aisle-not-found`, `location-slot-not-found`, `location-type-not-found`, `placement-rule-not-found` | Register the parent first. Order: site → zone → aisle → location type → placement rule → slot. |
| 409 | `duplicate-*` (site code, zone, aisle, location type, placement rule, location code, fixed structure, cross-aisle) | Natural key already exists. Codes are permanent: there is no rename. |
| 409 | `site-not-active`, `zone-not-active`, `aisle-not-active`, `already-decommissioned` | Decommission is one-way (ADR-0005). Register a new code instead. |
| 409 | `concurrent-modification` | Another writer changed the slot or site between load and save (version column, ADR-0025; sites since ADR-0035). Re-fetch and retry. On `PUT /sites/{siteCode}/transfer-capability` it means two operators changed the same site at once: re-read `GET …/transfer-capability` and PUT again. |
| 400 | `missing-transfer-capability-field` | `PUT /sites/{siteCode}/transfer-capability` without both `transferOriginEnabled` and `transferDestinationEnabled` as booleans. The body is a full snapshot; send both (ADR-0035). |
| 422 | `invalid-site-role` | `POST /sites` with a `siteRole` that is not one of the known roles, or `PUT /sites/{siteCode}/role` with a missing, unknown or `UNSPECIFIED` role. Values are exact UPPER_SNAKE (`NON_SORTABLE_FC`, not `Non-Sortable Fulfillment Center`); the list is `GET /site-roles/transfer-defaults` (ADR-0037). |
| 422 | `placement-rule-violated` | The location type is not legal in this zone (ADR-0003). Check the placement rules for the zone. This is also counted as `outcome=rejected_by_placement_rule`. |
| 422 | `invalid-max-weight`, `invalid-max-volume`, `invalid-dimensions`, `invalid-z`, `invalid-pitch`, `unknown-*`, `dock-flow-required`, `work-center-activities-required`, `functional-attributes-not-allowed`, `zone-mismatch`, `cross-aisle-*`, `segment-*`, `empty-*`, `negative-*` | Semantically invalid values. `detail` names the field. |
| 500 | `internal-error` | Unmapped error, usually a database error. Look for the `http request` error log with the same `request_id`. |

## MCP

| Symptom | Cause | Fix |
|---|---|---|
| A tool call fails with `session not found`-style errors after a scale-out | MCP sessions live in process memory (`Mcp-Session-Id`). A second replica does not know sessions created on the first. | Keep `mcp.replicaCount: 1` (ADR-0022). |
| Tool error text starts with a slug, e.g. `site-not-found: …` | Expected: tool errors are slug-prefixed (ADR-0033) | Map the slug as in the REST table above. |

## Not applicable to this service

- **Circuit breaker open:** facility-layout has no circuit breaker. It makes
  no synchronous outbound calls except `mcp` → `facility-reports`, which
  fails per call.
- **OLTP consumer lag:** `facility` consumes no Kafka topic. The only
  consumer group is the projector's `facility-analytics`.
