# Facility Layout (Generic Subdomain — physical warehouse structure)

System of record for **where things physically are in the building**: the
site hierarchy (site, area, zone, aisle) and the coded storage slots in it.
It owns whether a coded location **exists, is active, and is legal for a
given kind of storage unit**. It does NOT own occupancy or stock — that
stays in `inventory-storage`'s `Bin`/`StockUnit` aggregates.

Domain source of truth: `/Users/claudioed/docs/amazon-fulfillment-ddd.md`
and `/Users/claudioed/warehouse-systems-ddd.md`. Honor that ubiquitous
language everywhere. Module `github.com/claudioed/facility-layout`, Go 1.26.

## Hard rules

- **Own bounded context, own service**: consumed by `inventory-storage`
  (WMS), `wes-work-planning`/`fulfillment-execution`/`warehouse-planning`
  (WES) and `warehouse-ops-agent` (MCP). Never a package bolted onto
  `inventory-storage`. This service is an Open Host
  Service with a Published Language (events + REST) and has NO inbound
  dependency on any other fleet service, ever. Downstream Conformists never
  get write access, and this service never reaches into their aggregates.
- **Hexagonal (NON-NEGOTIABLE)**: domain depends on nothing; application
  depends on domain; adapters depend on application/domain. No framework or
  SQL types in the domain layer. Layout: `.claude/rules/architecture.md`.
- **Analytics isolation (ADR-0010)**: OLTP domain/application must NOT
  import the analytics store (arch-tests enforce it); `internal/analytics/report/`
  depends on nothing. Only `cmd/facility-projector` writes the analytics DB.
- **Publisher is trace-free (ADR-0009)**: no OTel package on the Kafka
  publisher. Default (`EVENT_PUBLISHER` unset) is the `outbound/events`
  log/outbox publisher; `kafka` publishes to `warehouse.facility.events`.
- **No auth layer (ADR-0014 reverted by ADR-0015)**: do not re-add
  `AUTH_MODE`/`API_READ_KEY`/etc. on REST or MCP without re-reading ADR-0015.
- **Idempotency (ADR-0019)**: every true resource-creation POST (sites,
  zones, fixed structures, aisles, cross-aisles, location types, placement
  rules, location slots) requires a caller-supplied `Idempotency-Key` header
  when `DATABASE_URL` is set. Bulk import and decommission are excluded.
  No database ⇒ check skipped (`IdempotencyPool` nil ⇒ passthrough).
- **Errors**: typed domain errors mapped to HTTP status in the adapter; RFC
  7807 `application/problem+json` for every error (ADR-0004) — never a
  bespoke `{"error": ...}` shape.
- **`.golangci.yml`** is copied VERBATIM from `../inventory-storage/.golangci.yml`
  — do not hand-edit; re-copy if the fleet's shared config changes.
- **`web/`** (`facility-mfe`) has its own `package.json`/build and is NOT
  part of the Go module or its quality gate.
- Every package has a doc comment; gofmt/go vet clean. Table-driven tests
  (domain + application with in-memory adapter), one httptest per endpoint,
  build-tagged Postgres integration test (skipped without `DATABASE_URL`).
- Config via env: `DATABASE_URL`, `HTTP_ADDR` (default `:8080`),
  `ANALYTICS_DATABASE_URL`, `EVENT_PUBLISHER` (`kafka` or unset).

## Events: CloudEvents 1.0 is MANDATORY (ADR-0024)

Every Kafka message produced or consumed (integration `warehouse.<ctx>.events`
AND analytics `warehouse.<ctx>.analytics`) is a CloudEvents 1.0 event in
structured content mode — a hard fleet rule.

- No flat envelope, no dual-write/dual-read, no envelope toggle env var.
- Build/validate/(un)marshal ONLY via `internal/adapters/kafka/cloudevents/`
  (`github.com/cloudevents/sdk-go/v2/event`); transport stays kafka-go.
- Breaking payload change => new `.v2` type + new dataschema, never mutate.
- Consumers dispatch on the FULL `type`, ignore unknown types, dedupe on
  `id`, and DLQ/skip (never crash, never parse a legacy shape) anything that
  fails validation.
- Required attributes, header, `type`/`dataschema` formats, analytics
  details: `.claude/rules/events-and-analytics.md`.

## Commands

Run from the repo root, not the docs site:

```
make check-fast  # quick gate — run before saying "done"
make check       # fmt-check + vet + build + lint + test — before every commit
make check-all   # check + coverage(90%) + arch-test + bdd — before every push
make integration # Postgres integration tests — needs DATABASE_URL
make vuln        # govulncheck — after touching go.mod/go.sum
make mutation    # gremlins on internal/domain — after changing domain behaviour
```

Docs site: `cd docs && npm ci && npm run gen-api-docs` (regenerates the API
reference from `apis/openapi.yaml`); `npm run build`, `npm start`.

## Rules map (`.claude/rules/`)

- `domain-model.md` — location-code hierarchy, ubiquitous language,
  aggregate invariants, domain events, use cases.
- `architecture.md` — directory layout, strategic classification, downstream
  consumers.
- `events-and-analytics.md` — integration publishing, analytics data
  product, full CloudEvents attribute spec.
- `rest-api-and-frontend.md` — REST endpoint table, CORS, `web/` contract.
- `testing-and-quality.md` — Definition of Done, quality gates, CI parity.

Fleet-wide rules: `.claude/rules/fleet/*.md` (canonical in IQVO/warehouse-docs `agents/fleet/`; never hand-edit).

<!-- harness:scoped-rules:start (generated by tools/migrate_v3.py in warehouse-harness-template; do not hand-edit) -->
## Scoped rules and harness

Claude Code loads each rule below automatically when you touch the matching paths. OpenCode and Codex do NOT: read the rule BEFORE editing matching files.

| When touching | Read |
|---|---|
| `cmd/**`, `internal/**`, `migrations/**` | `.claude/rules/architecture.md` |
| `internal/domain/**`, `internal/application/**`, `internal/adapters/inbound/mcp/**` ... | `.claude/rules/domain-model.md` |
| `internal/adapters/kafka/**`, `internal/adapters/outbound/kafka/**`, `internal/adapters/outbound/events/**` ... | `.claude/rules/events-and-analytics.md` |
| `internal/adapters/inbound/http/**`, `apis/openapi*.yaml`, `apis/openapi/**` ... | `.claude/rules/rest-api-and-frontend.md` |
| `.github/**`, `Makefile`, `.gremlins.yaml` ... | `.claude/rules/testing-and-quality.md` |

Hooks (`scripts/harness/hook.py`, wired for Claude Code, Codex and OpenCode) block pushes to develop/main, `--no-verify`, bare `rm -rf`, and edits to generated files, and feed gofmt/vet findings back after each edit. Before saying "done" run `make check-fast`; the full gate is `make check-all`. `HARNESS_OFF=1` disables the hooks when debugging the harness itself.
<!-- harness:scoped-rules:end -->
