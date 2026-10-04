---
paths:
  - "internal/adapters/kafka/**"
  - "internal/adapters/outbound/kafka/**"
  - "internal/adapters/outbound/events/**"
  - "internal/adapters/inbound/kafka/**"
  - "internal/adapters/outbound/analyticsstore/**"
  - "internal/analytics/**"
  - "internal/application/ports/**"
  - "cmd/facility-projector/**"
  - "cmd/facility-reports/**"
  - "migrations/analytics/**"
---

# Events, integration publishing and analytics (moved from CLAUDE.md)

## Integration publishing & analytics data product (ADR-0009, ADR-0010)

This is an **Open Host Service**: its domain events are its Published
Language.

- **Integration (ADR-0009)**: `outbound/kafka` publishes every domain event
  to `warehouse.facility.events` when `EVENT_PUBLISHER=kafka`. Default
  (unset) uses the `outbound/events` log/outbox publisher. No OTel package
  on this publisher — it is trace-free by design.
- **Analytics (ADR-0010)**: additive read side built from this service's
  OWN events. OLTP domain/application must NOT import the analytics store
  (enforced by `internal/architecture` arch-tests); `internal/analytics/report/`
  depends on nothing. A second kafka adapter publishes to
  `warehouse.facility.analytics`. Separate `ANALYTICS_DATABASE_URL`,
  `migrations/analytics/`, read-only reader role. Three processes:
  `cmd/facility` (OLTP), `cmd/facility-projector` (only analytics writer,
  consumes from FirstOffset, idempotent on the CloudEvents `id`), `cmd/facility-reports`
  (read-only, `GET /reports/...`); an MCP report tool exposes the same data.
- **Report**: **Layout Catalog Growth & Change**, per site/zone × DAY
  bucket. `GET /reports/.../freshness` reports lag.
- **Auth status (ADR-0014, reverted by ADR-0015)**: REST/MCP static-bearer
  auth was added then fully reverted (commit `73d6068`, 2026-09-09) — there
  is currently NO auth layer on REST or MCP endpoints. Do not re-add
  `AUTH_MODE`/`API_READ_KEY`/etc. without re-reading ADR-0015 first.
- **Idempotency (ADR-0019)**: every true resource-creation POST (eight of
  them — sites, zones, fixed structures, aisles, cross-aisles, location
  types, placement rules, location slots) requires a caller-supplied
  `Idempotency-Key` header when `DATABASE_URL` is set, so a lost-response
  retry replays the original outcome instead of double-creating. Bulk
  import and decommission are deliberately excluded. In-memory/no-database
  runs skip the check entirely (`IdempotencyPool` nil ⇒ no-op passthrough).

## Events: CloudEvents 1.0 is MANDATORY

Every Kafka message this service produces or consumes (integration
`warehouse.<ctx>.events` AND analytics `warehouse.<ctx>.analytics`) is a
CloudEvents 1.0 event in structured content mode. This is a hard fleet rule,
not a preference:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write,
  no dual-read, no envelope toggle env var (`EVENT_ENVELOPE_MODE` is gone).
- Build/validate/(un)marshal with `github.com/cloudevents/sdk-go/v2/event`
  via `internal/adapters/kafka/cloudevents/`; transport stays kafka-go.
- Kafka header `content-type: application/cloudevents+json; charset=UTF-8`.
- Required attributes: `specversion=1.0`, `id` (UUID, stable across outbox
  redelivery), `source=/warehouse/facility-layout`, `type`, `subject` (aggregate id), `time`
  (occurred-at, UTC), `datacontenttype=application/json`,
  `dataschema=urn:warehouse:facility-layout:<events|analytics>:<EventName>:v<N>`.
- `type` = `com.warehouse.<subdomain>.<bounded-context>.<entity>.<EventName>`;
  for this service: `com.warehouse.wms.facility-layout.<entity>.<EventName>`. Breaking payload
  change => new `.v2` type + new dataschema version, never mutate.
- Consumers dispatch on the FULL `type`, ignore unknown types, dedupe on
  `id`, and DLQ/skip (never crash, never parse a legacy shape) anything that
  fails CloudEvents validation.

Full standard and the fleet's cross-service type catalogue: ADR-0024
(`docs/docs/adr/`).
