---
id: observability
title: Observability
sidebar_label: Observability
---

# Observability

All four binaries **push** traces, metrics and logs over OTLP/gRPC to an
OpenTelemetry Collector. Nothing is scraped and **there is no `/metrics`
endpoint**. The setup lives in `internal/adapters/outbound/telemetry`
(ADR-0012, ADR-0030).

## Exporter configuration

| Setting | Value | Source |
|---|---|---|
| Endpoint | `OTEL_EXPORTER_OTLP_ENDPOINT`, default `localhost:4317`. The chart sets `http://otel-collector.observability.svc.cluster.local:4317`. A `http(s)://` scheme is stripped. | `telemetry.go` |
| Transport | gRPC, insecure (no TLS), 5s export timeout per attempt | `telemetry.go` |
| Signals | traces (batch span processor), metrics (periodic reader, SDK default interval), logs (batch processor via the `otelslog` bridge) | `telemetry.go`, `slogotel.go` |
| Resource | `service.name` (`OTEL_SERVICE_NAME`), `service.version` (`SERVICE_VERSION` or ldflags), `deployment.environment.name` (`ENVIRONMENT`, default `local`), plus the SDK defaults | `telemetry.go` |
| Propagation | W3C `traceparent`/`tracestate` plus `baggage` | `telemetry.go` |
| Collector down | Non-blocking. Telemetry is dropped and export errors are logged at **debug** level only, so the service keeps running. | `telemetry.go` |

Service names default to `facility-layout`, `facility-projector`,
`facility-reports` and `facility-layout-mcp`. The chart sets exactly these.

## Metrics

### Service-defined

| Instrument | Type | Unit | Attributes | Recorded where |
|---|---|---|---|---|
| `facility.location_slot.registrations` | Int64Counter | `{registration}` | `outcome` = `accepted` \| `rejected_by_placement_rule` \| `rejected` | `RegisterLocationSlot` and `ImportFacilityLayout` use cases, once per slot attempt, so bulk-imported slots count the same as single POSTs (`internal/adapters/outbound/telemetry/metrics.go`, meter scope `github.com/claudioed/facility-layout`) |

In Prometheus (through the Collector) this counter appears as
`facility_location_slot_registrations_total`.

### Library-provided

| Instrument | Type | Emitted by | Processes |
|---|---|---|---|
| `http.server.request.duration` (s) | Float64Histogram | otelchi v0.12.3 `metric.NewServerRequestDuration`. Attributes are `http.method`, `http.scheme`, `http.route` (chi pattern) and `service.name`, with **no status-code attribute**. | facility, facility-projector (admin), facility-reports |
| `http.server.active_requests` | Int64UpDownCounter | otelchi `metric.NewServerActiveRequests` | same |
| `db.client.operation.duration` (s) | Histogram | otelpgx v0.12.0 tracer, buckets 1ms–10s | facility, mcp (OLTP pool only) |
| `db.client.operation.errors` | Int64Counter | otelpgx tracer | facility, mcp |
| `pgxpool.acquires`, `pgxpool.acquire_duration`, `pgxpool.acquired_connections`, `pgxpool.canceled_acquires`, `pgxpool.constructing_connections`, `pgxpool.empty_acquire`, `pgxpool.empty_acquire_wait_time`, `pgxpool.idle_connections`, `pgxpool.max_connections`, `pgxpool.max_idle_destroys`, `pgxpool.max_lifetime_destroys`, `pgxpool.new_connections`, `pgxpool.total_connections` | async gauges/counters | `otelpgx.RecordStats` in `postgres.NewPool` | facility, mcp |
| `go.memory.used`, `go.memory.limit`, `go.memory.allocated`, `go.memory.allocations`, `go.memory.gc.goal`, `go.goroutine.count`, `go.processor.limit`, `go.config.gogc` | runtime | `go.opentelemetry.io/contrib/instrumentation/runtime` v0.71.0 | all four |

The MCP binary's router is a plain chi router without otelchi, so it emits
no HTTP server metrics. The analytical pools (`analyticsstore.NewPool`,
`NewReadOnlyPool`) are not instrumented with otelpgx.

The service defines **no** metric for outbox backlog, relay failures or
consumer lag. Watch those through SQL, logs and broker-side lag (see the
alerts below).

## Traces

| Span | Origin | Notes |
|---|---|---|
| Route pattern, e.g. `GET /sites/{siteCode}/layout` | otelchi with `WithChiRoutes` | One server span per HTTP request in facility, facility-reports and the projector admin server. The route pattern keeps span-name cardinality bounded. |
| `query <normalized SQL>` (also `prepare`, `batch query`, connection acquire) | otelpgx (`WithQuerySpanNamePrefix`) | Child spans of the request or use case. Statements are normalized, without literal values. |
| `mcp.tool <tool-name>` | `internal/adapters/inbound/mcp/tools.go` | One span per tool call with attribute `mcp.tool.name`. Status is `Error` with the slug-prefixed message (ADR-0033). |

Kafka trace propagation:

- **Direct publishing** no longer exists (ADR 0039). Every event goes through
  the outbox:
- **Outbox mode** stores the writing transaction's W3C trace
  context (`traceparent`, `tracestate`, `baggage`) in
  `outbox_events.headers` (migration `0010`), and the relay sends it as Kafka
  headers next to the CloudEvents `content-type`
  ([ADR-0038](../adr/0038-outbox-trace-context.md)). A consumer that extracts
  the headers parents its span on the originating request. The relay never
  injects its own context. Rows written with no active span, or before
  migration `0010`, have `headers` NULL and relay with `content-type` only.
  Check one row with
  `SELECT id, event_type, headers FROM outbox_events ORDER BY id DESC LIMIT 5;`.
- The analytics consumer never extracts trace context and starts no spans,
  so projector work is not traced.

## Logs

Logs are JSON on stdout (`slog.NewJSONHandler`) and are fanned out to the
OTel log bridge. Inside a span, every record also gets `trace_id` and
`span_id`. The level comes from `LOG_LEVEL`.

| Message | Level | Fields | Emitted by |
|---|---|---|---|
| `http request` | info, or error when status ≥ 500 | `method`, `path` (sanitized), `status`, `duration_ms`, `bytes`, `request_id` | facility, facility-reports (`RequestLogger`) |
| `telemetry configured` | info | `service_name`, `service_version`, `environment`, `otlp_endpoint` | facility |
| `event publisher configured` | info | `publisher` (`kafka`), `mode` (`outbox`), `integration_topic`, `analytics_topic`, `brokers` | facility |
| `retrying` / `succeeded after retry` | warn / info | `op`, `attempt`, `in`, `err` | boot retry, all binaries |
| `outbox relay pass failed` | error | `error` | facility |
| `outbox relay: row reached the attempt cap and is excluded from future claims; ...` | error | `outbox_id`, `topic`, `event_type`, `key`, `attempts`, `max_attempts`, `error` | facility relay, once per row when it reaches `OUTBOX_MAX_ATTEMPTS` ([ADR-0036](../adr/0036-outbox-relay-attempt-cap.md)) |
| `invalid integer env var, using default` | warn | `key`, `value`, `default`, `error` | facility, at boot, when `OUTBOX_MAX_ATTEMPTS` is malformed or negative |
| `outbox relay published events` | **debug** | `count` | facility |
| `housekeeping sweep` / `housekeeping sweep failed` | info / error | `idempotency_keys_deleted`, `outbox_events_deleted`, `error` | facility |
| `analytics consumer starting` | info | `topic`, `group`, `brokers` | facility-projector |
| `analytics: exhausted retries, sending to dead-letter topic` | error | `topic`, `dlq_topic`, `attempts`, `error` | facility-projector |
| `analytics: message is not a valid CloudEvent, sending to dead-letter topic` | warn | `topic`, `partition`, `offset`, `error` | facility-projector |
| `analytics consumer stopped` | error | `error` | facility-projector (the consume loop has ended) |
| `catalog-growth report tool wired` | info | `reports_base_url` | mcp |

## Dashboards

warehouse-infra provisions a Grafana dashboard **"Warehouse — Facility
Layout"** (`terraform/dashboards/contexts/facility-layout.json`, generated by
`scripts/gen-context-dashboards.py`). It has these rows:

- Kong north-south (request rate, 5xx rate, p95 latency);
- service HTTP RED by process (OLTP, projector, reports, MCP);
- business metrics (`facility_location_slot_registrations_total`);
- Go runtime (goroutines, memory used);
- logs (live stream, volume by level, errors and warnings).

This repo contains no dashboard of its own.

## Suggested alerts

| Alert | Signal | Why |
|---|---|---|
| Outbox backlog growing | `SELECT count(*) FROM outbox_events WHERE published_at IS NULL` above 0 for more than 5 min, or repeated `outbox relay pass failed` logs | The relay cannot reach Kafka. Events are safe but downstream consumers (inventory-storage, warehouse-planning) are going stale. |
| Outbox row excluded by the attempt cap | any ERROR `outbox relay: row reached the attempt cap ...`, or `SELECT count(*) FROM outbox_events WHERE published_at IS NULL AND attempts >= 20` above 0 | A row failed `OUTBOX_MAX_ATTEMPTS` sends and downstream contexts will never get that event until it is re-armed ([Runbook](./runbook.md#an-outbox-row-hit-the-attempt-cap), ADR-0036). |
| Projector stopped | log `analytics consumer stopped`, or broker-side lag of group `facility-analytics` rising | The consume loop does not restart by itself. The pod needs a restart. |
| DLQ receiving messages | any new message on `warehouse.facility.analytics.dlq` | A poison or invalid CloudEvent: investigate the `x-dlq-error` header. |
| Report freshness | `GET /reports/catalog-growth/freshness` `lagSeconds` above the expected write cadence | The read model is behind the OLTP writes. |
| Placement-rule rejections spike | `rate(facility_location_slot_registrations_total{outcome="rejected_by_placement_rule"}[15m])` | A bad import or a misconfigured placement rule. |
| HTTP 5xx | `http.server.request.duration` count filtered by error logs (the metric has no status attribute) or Kong 5xx | Use Kong's status-coded metrics for rates. |
| Crash-looping boot | pod restarts with `(after 5 attempts)` in the last log line | Database unreachable or a migration failure. See [Troubleshooting](./troubleshooting.md). |
