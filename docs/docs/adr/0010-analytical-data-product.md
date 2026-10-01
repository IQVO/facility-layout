---
id: 0010-analytical-data-product
title: 10. Per-service analytical data product (report) via a separate analytics topic
sidebar_label: 10. Analytical data product
sidebar_position: 10
description: "An analytical read model — the 'Layout Catalog Growth & Change' report — built from facility-layout's own domain events on a dedicated warehouse.facility.analytics topic, projected into a separate analytical database and served by a read-only reports binary over REST and MCP. A lightweight data mesh with no central data platform, additive to the OLTP service."
---

# 10. Per-service analytical data product (the "report")

## Status

**Accepted; analytics envelope (Envelope v1 / `schema_version`) superseded by
[ADR-0024](./0024-cloudevents-mandatory-envelope.md).** The analytics topic,
projector, analytical database and reports binary still stand; the
analytics stream now carries CloudEvents 1.0 with
`dataschema=urn:warehouse:facility-layout:analytics:<EventName>:v1` instead
of `schema_version` (Superseded by ADR-0024).

## Context

The warehouse-systems estate wants a per-service **report** that supports
analytics while each service stays the **OLTP** system of record for its own
bounded context. Stated deliberately simply: *follow data-mesh principles, but
without standing up a whole data platform.* No central warehouse, no lake, no
shared ETL team.

facility-layout already has the substrate the analytical side needs:

- Past-tense **domain events** — `SiteRegistered`, `ZoneRegistered`,
  `AisleRegistered`, `LocationTypeRegistered`, `PlacementRuleDefined`,
  `LocationSlotRegistered`, `LocationSlotDecommissioned`,
  `FacilityLayoutImported` — raised by the aggregates. These are this service's
  Published Language.
- A Kafka **integration** path (`warehouse.facility.events`) carrying the whole
  Published Language to downstream Conformists, with the CloudEvents-like
  `Envelope` established in [ADR-0009](./0009-kafka-integration-publisher.md).
- The dual inbound-adapter pattern (HTTP + MCP) from
  [ADR-0007](./0007-mcp-inbound-adapter.md).

So the event backbone exists; what is missing is the **analytical read side**.
The forces shaping the decision:

- **The integration contract must not become coupled to reporting.** The report
  bucket cadence and its derived fields evolve on a different schedule from the
  integration contract. Adding analytics-only shape to
  `warehouse.facility.events` would entangle two contracts that should evolve
  separately and risk surprising existing Conformist consumers.
- **Analytics must never contend with OLTP.** A report query, a long
  aggregation, or a projection rebuild must not touch the transactional database
  that answers "does this coded location exist and is it legal for this unit".
- **The service still owns its data as a product.** Data-mesh domain ownership
  means the read side lives in this repo, owned by the same team, with a
  contract, an owner, and a freshness SLA — not shipped off to a central team.
- **No new central platform.** Reuse what the estate already runs: Kafka,
  Postgres, chi, the MCP SDK.

## Decision

**facility-layout owns an analytical data product built solely from its own
domain events, delivered on a dedicated analytics topic, projected into a
separate analytical database, and served read-only over REST and MCP. Three
processes; one writer. Purely additive — the OLTP domain and application layers
are unmodified.**

### 1. Separate analytics topic

A **second** outbound adapter (`internal/adapters/outbound/kafka/analytics_publisher.go`)
publishes the catalog-change event set to **`warehouse.facility.analytics`**,
using the shared **Envelope v1** wrapper (`event_id`, `event_type`,
`occurred_at`, `source`, `schema_version`, `data`) — *superseded by
[ADR-0024](./0024-cloudevents-mandatory-envelope.md): now a CloudEvents 1.0
event with an analytics `dataschema`*. The ADR-0009 integration
publisher (`publisher.go`) and `warehouse.facility.events` are **left
untouched**. Because facility-layout has no observability/OTel package, the
analytics publisher and consumer are **trace-free**, consistent with the
integration publisher.

The composition root (`cmd/facility`) **fans out** when `EVENT_PUBLISHER=kafka`,
publishing every domain event to BOTH topics through a small fan-out publisher,
so the integration and analytics streams stay independent.

**Data payload choice.** facility-layout's domain events already serialize
themselves to their wire shape (their struct tags ARE the contract), so — as the
ADR-0009 integration publisher does — the analytics envelope carries the event's
own JSON as its `data` field verbatim, rather than building a bespoke snake_case
payload per event type. The consumer decodes the fields it needs (camelCase, as
the events emit them) and ignores the rest.

### 2. Separate analytical database

Projections land in a **separate analytical database** with its own credentials
(`ANALYTICS_DATABASE_URL`), its own golang-migrate migration set
(`migrations/analytics/`), and a **read-only role** for the reader. Baseline is a
dedicated `*_analytics` database in the existing Postgres release; the
`ANALYTICS_DATABASE_URL` seam allows promotion to a physically separate instance
later without code changes. The OLTP `DATABASE_URL` database is never opened by
the analytical side. The reader additionally pins every connection to
`default_transaction_read_only=on` — defence in depth on top of the read-only
role.

### 3. Three processes, one writer

- **`cmd/facility`** — the OLTP binary. Unchanged, except its composition root
  additionally fans out domain events to the analytics topic.
- **`cmd/facility-projector`** — the analytics **writer**. Consumes
  `warehouse.facility.analytics` (consumer group `facility-analytics`, reading
  from the earliest offset), applies idempotent projections, and is the **only**
  writer of the analytical database. Runs the analytical migrations on start.
- **`cmd/facility-reports`** — the **read-only reader**. Opens the analytical
  database read-only and serves `GET /reports/catalog-growth` and
  `GET /reports/catalog-growth/freshness`. Never writes, never migrates.

### 4. Served over REST and MCP

The reports binary serves the REST report resource. A curated, read-only MCP
tool (`get_facility_catalog_growth_report`) — following the intent-level tool
discipline of [ADR-0007](./0007-mcp-inbound-adapter.md) — calls the reports REST
(via `REPORTS_BASE_URL`) rather than opening the analytical database itself, so
no process touches a datastore it does not own.

### 5. The report

A **Layout Catalog Growth & Change** read model, keyed per **scope × day**. The
scope is a site code (site- and zone-level growth), a zone id (aisle- and
slot-level growth), or the catalog-wide empty scope (location types, placement
rules, bulk imports). Per bucket it counts slots registered and decommissioned,
zones/aisles/location-types registered, placement rules defined, sites
registered, and bulk imports with their submitted/imported/rejected row tallies.

Crucially, the layout is a **slow-changing reference catalog** — a Generic
Subdomain the rest of the estate conforms to, not a high-frequency transactional
stream — so rows are bucketed by **DAY**, not by hour. This is the deliberate
difference from the fulfillment-execution pilot's hourly throughput report.

It is a **projection** from events, eventually consistent to a freshness SLA
(p95 event-to-report lag < 30s), not real-time.

The analytical read model lives in a new `internal/analytics/report` region that
**depends on nothing**; the consumer and store adapters depend on it. The OLTP
**domain and application layers are not modified**, and `arch-test` enforces that
neither they nor the read-model region import the analytics store.

## Consequences

### Easier

- **The integration contract is untouched**, so evolving the report never risks
  an integration consumer. Analytics retention is tuned independently.
- **Analytics cannot contend with OLTP** — separate database, separate
  connection, read-only reader role. A runaway report query cannot touch the
  transactional path that validates a stow.
- **The report is rebuilt purely from events** — no dual-write from OLTP, so the
  transactional write path gains no new failure mode. The read model can be
  rebuilt from scratch by replaying the topic from the earliest offset.
- **No central platform.** Everything reuses the estate's existing Kafka,
  Postgres, chi and MCP SDK.
- **Least privilege by construction.** The read-only DB role plus
  `default_transaction_read_only` makes "a report can never corrupt the
  analytical store" a hard guarantee, not a convention.

### Harder

- **One more topic, two more binaries, and a second database** to operate.
  Mitigated by reusing the OLTP Postgres and consumer/publisher scaffolding.
- **Eventual consistency.** The report lags OLTP truth by the freshness SLA; it
  is not a real-time view. The correct data-mesh tradeoff, but it must be
  communicated to report consumers.
- **The analytics publisher is a second producer path** for the same domain
  events. The event set it publishes must be kept in step with the report's
  inputs.
- **First deploy has an empty report** until events flow; historical backfill
  requires replaying `warehouse.facility.analytics` from earliest into a fresh
  projector, so Kafka retention must cover the desired backfill window. (The
  freshness reader handles the empty-store case explicitly: `max(occurred_at)`
  over an empty table returns a NULL row, read as zero lag.)

## References

- Report contract: [Layout Catalog Growth & Change report](../analytics/catalog-growth-report.md)
- [ADR-0007 — MCP inbound adapter](./0007-mcp-inbound-adapter.md)
- [ADR-0009 — Kafka integration publisher for the Published Language](./0009-kafka-integration-publisher.md)
