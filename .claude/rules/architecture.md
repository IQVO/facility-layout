---
paths:
  - "cmd/**"
  - "internal/**"
  - "migrations/**"
---

# Architecture reference (hexagonal layout, moved from CLAUDE.md)

Hexagonal / Ports & Adapters. Strict dependency rule: **domain depends on
nothing; application depends on domain; adapters depend on
application/domain.** No framework or SQL types in the domain layer.

```
cmd/facility/                     main.go — OLTP composition root
cmd/facility-projector/           analytics projector (only writer to analytics DB)
cmd/facility-reports/             read-only analytics reports API
cmd/mcp/                          MCP server composition root
internal/
  domain/
    site/ zone/ aisle/ slot/ placement/ structure/ travel/ shared/
  application/
    ports/                        OUT interfaces (repos, EventPublisher, Clock, LocationMetrics)
    usecases/                     one struct per use case
  adapters/
    inbound/http/                 chi handlers, DTOs, error mapping
    inbound/mcp/                  MCP tools/resources/prompts
    inbound/kafka/                analytics projector's consumer (FirstOffset replay)
    outbound/postgres/            pgxpool repos + migrations
    outbound/memory/              in-memory repos for tests/local
    outbound/events/              log/outbox publisher (default when EVENT_PUBLISHER unset)
    kafka/cloudevents/            the ONLY CloudEvents 1.0 helper (New/Decode/ContentTypeHeader)
    outbound/kafka/               integration + analytics publishers (CloudEvents only)
    outbound/analyticsstore/      analytics read-side Postgres repos
    outbound/telemetry/           OTel wiring
  analytics/report/               read-only analytics report queries (no domain/app imports)
  architecture/                   arch-go fitness tests
migrations/                       golang-migrate SQL (OLTP)
migrations/analytics/             golang-migrate SQL (analytics DB)
apis/openapi.yaml                 REST API contract (source of truth for docs/)
web/                               facility-mfe — Vite+React module-federation remote
docs/                              Docusaurus site (ADRs, ecosystem, API reference)
```

## Strategic classification and relationships

- **Strategic classification**: Generic Subdomain (same bucket as
  Cartonization/WCS in `warehouse-systems-ddd.md`) — well-understood, not a
  competitive differentiator, and explicitly a concern the domain doc says
  to *extract rather than duplicate*. `inventory-storage` (WMS tier) needs
  location validity to accept a stow; `wes-work-planning` /
  `fulfillment-execution` (WES tier) need zone/aisle adjacency for
  travel-path and congestion reasoning. Neither owns it; both consume it —
  that's why this is its own bounded context and its own service, never a
  package bolted onto `inventory-storage`.
- **Relationship to the rest of the system**: this service is an **Open
  Host Service** with a **Published Language** (its domain events and REST
  API). It has NO inbound dependency on any other fleet service and never
  will. Its live downstream **Conformists**: `inventory-storage` (consumes
  `ZoneRegistered`/`LocationSlotRegistered`/`LocationSlotDecommissioned`
  from `warehouse.facility.events`), `warehouse-planning` (consumes
  `LocationSlotRegistered`/`LocationSlotDecommissioned` from the same topic
  for its storage-capacity tally), `wes-work-planning` (`GET /distance`),
  `fulfillment-execution` (`GET /locations/{code}` for the slot's `role`),
  and `warehouse-ops-agent` (MCP tools + the catalog-growth report). This
  service never reaches into their aggregates, and none of them get write
  access to this one's.
