# Project: Facility Layout (Generic Subdomain — physical warehouse structure)

The system of record for **where things physically are in the building**:
the site's structural hierarchy (site, area, zone, aisle) and the coded
storage slots inside it. This context does NOT own occupancy or stock —
that stays in `inventory-storage`'s `Bin`/`StockUnit` aggregates. It owns
whether a coded location **exists, is active, and is legal for a given kind
of storage unit** — the "warehouse map" that other contexts read but never
write.

Source of truth for the domain model: `/Users/claudioed/docs/amazon-fulfillment-ddd.md`
and `/Users/claudioed/warehouse-systems-ddd.md`. Honor that ubiquitous
language everywhere in this codebase.

This file is a concise index. Detailed reference material lives under
`.claude/rules/` (see the map at the bottom) — read the relevant rule file
before touching that area of the code.

## Project Overview

- **Module**: `github.com/claudioed/facility-layout`, Go 1.26.
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
  from `warehouse.facility.events`), `wes-work-planning` (`GET /distance`),
  `fulfillment-execution` (`GET /locations/{code}` for the slot's `role`),
  and `warehouse-ops-agent` (MCP tools + the catalog-growth report). This
  service never reaches into their aggregates, and none of them get write
  access to this one's.
- **Full ubiquitous language, aggregate invariants, domain events, and use
  cases**: `.claude/rules/domain-model.md`.

## Architecture (NON-NEGOTIABLE — identical shape across the fleet)

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
    site/ zone/ aisle/ slot/ placement/ shared/
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

Full REST surface and the `facility-mfe` frontend contract:
`.claude/rules/rest-api-and-frontend.md`.

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

## Key Commands

Local quality gate — run these from the repo root, not the docs site:

```
make check       # fmt-check + vet + build + lint + test — run before every commit
make check-all   # check + coverage(90%) + arch-test + bdd — run before every push
make coverage    # unit coverage gate, identical to the CI test job
make integration # build-tagged Postgres integration tests — needs DATABASE_URL
make vuln        # govulncheck ./... — run after touching go.mod/go.sum
make mutation    # gremlins on internal/domain — run after changing domain behaviour
```

Docs site (Docusaurus, OpenAPI-generated reference):

```
cd docs
npm ci
npm run gen-api-docs   # regenerate docs/docs/api-reference/rest/* from apis/openapi.yaml
npm run build          # runs gen-api-docs all, then docusaurus build
npm start               # local dev server
```

`.golangci.yml` is copied VERBATIM from `../inventory-storage/.golangci.yml`
— do not hand-edit; re-copy if the fleet's shared config changes.

Full testing discipline, coverage gates, and CI parity: `.claude/rules/testing-and-quality.md`.

## Code Standards

- Go 1.26, modules. chi (`github.com/go-chi/chi/v5`), pgx/v5 + pgxpool,
  golang-migrate SQL migrations.
- Config via env: `DATABASE_URL`, `HTTP_ADDR` (default `:8080`),
  `ANALYTICS_DATABASE_URL`, `EVENT_PUBLISHER` (`kafka` or unset).
- Typed domain errors mapped to HTTP status in the adapter; RFC 7807
  `application/problem+json` for every error response (ADR-0004) — do not
  invent a bespoke `{"error": ...}` shape.
- Table-driven tests: domain + application (in-memory adapter); one
  httptest per endpoint; build-tagged Postgres integration test (skipped
  without `DATABASE_URL`).
- gofmt/go vet clean; every package has a doc comment.
- `web/` (the `facility-mfe` frontend) has its own `package.json`/build/dev
  server and is NOT part of the Go module or its quality gate.

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

## Rules directory map (`.claude/rules/`)

Read the relevant file before working in that area — this index only
summarizes.

- **`domain-model.md`** — the location-code hierarchy (Site→Area→Zone→
  Aisle→Bay→Level→Position), full ubiquitous language glossary, every
  aggregate's invariants, the domain event list and CloudEvents naming
  convention, and the 10 application-layer use cases.
- **`rest-api-and-frontend.md`** — the complete REST endpoint table (write
  side + "draw the warehouse" read side + the stretch-goal SVG endpoint),
  CORS policy, and the `web/` facility-mfe module-federation contract.
- **`testing-and-quality.md`** — Definition of Done, the local quality-gate
  command sequence and why it exists, and the full tech/standards list.
