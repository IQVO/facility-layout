# facility-layout

> **⚠️ Study project.** This repository is an educational exercise in
> Domain-Driven Design applied to warehouse management/execution systems. It
> follows real industry-standard patterns and terminology (the
> Site-Area-Zone-Aisle-Bay-Level-Position location-code hierarchy,
> RFC 7807, hexagonal architecture) but is **not a production system** and
> is **not affiliated with, endorsed by, or representative of any real-world
> other company**.

The **Facility Layout** bounded context of the `warehouse-systems` platform:
the system of record for **where things physically are in the building**.

It owns the site's structural hierarchy — Site → Area → Zone → Aisle → Bay →
Level → Position — and the coded storage slots inside it. It does **not** own
occupancy or stock; that stays in `inventory-storage`'s `Bin`/`StockUnit`
aggregates. What it owns is whether a coded location **exists, is active, and
is legal for a given kind of storage unit**: the warehouse map that other
contexts read but never write.

**Strategic classification:** Generic Subdomain, extracted rather than
duplicated. It is an **Open Host Service** with a **Published Language** (its
domain events on the `warehouse.facility.events` Kafka topic, plus its REST
API). It depends on no other service. Its downstream **Conformists** today:
`inventory-storage` (consumes the Kafka topic to validate stows),
`warehouse-planning` (consumes slot registrations/decommissions for its
storage-capacity tally), `wes-work-planning` (`GET /distance`),
`fulfillment-execution`
(`GET /locations/{code}` for a location's role), and `warehouse-ops-agent`
(the MCP tools and the catalog-growth report).

---

## Documentation

Full documentation is published at
**<https://iqvo.github.io/facility-layout/>** — business context and
ubiquitous language, the DDD model (subdomain classification, aggregates,
invariants, domain events) plus a ddd-crew DDD artifact pack (Core Domain
Chart, Bounded Context Canvas, Aggregate Design Canvas, EventStorming, domain
message flow, UML class/sequence and ER diagrams — source in
[`docs/docs/ddd/`](docs/docs/ddd/ddd-artifacts.md)), an interactive API reference generated from
`apis/openapi.yaml`, the ecosystem context map, and the Architecture Decision
Records. The site source lives in [`docs/`](docs/) and deploys to GitHub Pages
via `.github/workflows/docs.yml`.

---

## The location code

Not a made-up scheme — the widely-used WMS industry pattern, hyphen-joined
and human-parsable:

```
WH1-STOR-AMB-A07-03-02-B
 |    |    |   |   |  |  `-- Position: left-to-right slot on the level
 |    |    |   |   |  `----- Level:    vertical level / shelf
 |    |    |   |   `-------- Bay:      bay / section along the aisle
 |    |    |   `------------ Aisle:    physical corridor
 |    |    `---------------- Zone:     behavioral class (AMB/CHL/FRZ/HAZ/FWD/RSV)
 |    `--------------------- Area:     coarse functional area (STOR/RCV/PACK/STAGE)
 `-------------------------- Site:     the physical facility
```

`LocationCode` is a **value object**, never free text: seven typed segments,
each non-empty and `[A-Z0-9]` only, always round-tripping through
`String()`/`ParseLocationCode()`.

---

## Layering

Hexagonal / ports & adapters. Dependencies point **inward only**:

```
cmd/facility/                 main.go — composition root for the REST service (the only place that knows all layers)
cmd/mcp/                      read-only MCP server (Streamable HTTP)
cmd/facility-projector/       analytics projector (the only analytical-DB writer)
cmd/facility-reports/         analytics reader (serves /reports/catalog-growth)
internal/
  domain/                     pure business logic; imports nothing but stdlib and itself
    shared/                   LocationCode, Capacity, geometry values, enums, the 12 events
    site/  zone/  aisle/      the structural aggregates (aisle/ also holds CrossAisle)
    placement/                LocationType, LocationRole, PlacementRule, RuleSet evaluation
    slot/                     LocationSlot — the coded leaf aggregate — and its functional attributes
    structure/                FixedStructure (walls, columns, offices, conveyors)
    travel/                   the travel graph and shortest-path search
  application/
    ports/                    OUT interfaces only (repos, EventPublisher, Clock, LocationMetrics)
    usecases/                 one struct per use case, including the read-model assemblers
  adapters/
    inbound/http/             chi handlers, DTOs, RFC 7807 error mapping, SVG rendering
    inbound/mcp/              MCP tools, resource template and prompt
    inbound/kafka/            analytics topic consumer (projector)
    outbound/postgres/        pgxpool repos, golang-migrate runner, UnitOfWork, transactional outbox + relay, sweeper
    outbound/memory/          thread-safe in-memory repos for tests and local runs
    outbound/events/          log + buffered publishers
    outbound/kafka/           integration + analytics topic publishers
    outbound/analyticsstore/  analytical DB projection and report queries
    outbound/telemetry/       OpenTelemetry wiring
  analytics/report/           the catalog-growth report model
migrations/                   golang-migrate SQL (migrations/analytics for the analytical DB)
```

The rule is enforced as an executable test, not a convention:
`internal/architecture/architecture_test.go` (arch-go) fails the build if the
domain ever imports the application layer, if an adapter reaches sideways
into its sibling, or if `ports` grows a concrete type.

---

## Ubiquitous language

| Term | Meaning |
|---|---|
| **Site** | A physical facility/building. Root of the hierarchy. |
| **Zone** | A behavioral classification scoped to a Site, bundling the Area+Zone segments. Carries `TemperatureClass` (Ambient/Chilled/Frozen) and a `Hazmat` flag. Every `PlacementRule` is keyed by it. |
| **Aisle** | A physical corridor scoped to a Zone. Carries a `SequenceHint` (walk-order position — the concrete travel-distance input the WES tier needs) and a `Direction` (OneWay/TwoWay). |
| **LocationType** | A reusable classification of slot shape/kind (`PalletRack`, `Shelf`, `ToteWall`, `BulkFloor`, `Staging`, `Amnesty`) with a default capacity envelope and a `LocationRole`. |
| **LocationRole** | What a location is *for* (ADR-0016): `Storage` (default), `Dock`, `Yard`, `WorkCenter`, `Drop`, `Staging`, `QC`, `Consolidation`, `Shipping`. Capacity is required only for `Storage`/`Staging`/`Drop`/`Consolidation`. A `Dock` slot carries a `dockFlow` (`Inbound`/`Outbound`/`Both`); a `WorkCenter` slot carries `activities` (`Pack`, `Sort`, `QC`, `VAS`, `Deconsolidate`, `Receive`, `Kit`). |
| **LocationSlot** | The coded leaf aggregate. Its identity **is** its `LocationCode`. Has a LocationType (and so a role), a capacity envelope, optional physical geometry, and a Status (`Active`/`UnderMaintenance`/`Decommissioned`). |
| **CrossAisle / FixedStructure** | Physical geometry (ADR-0017): a walkable connection between two aisles of a zone, and a wall/column/office/conveyor footprint on a site. Together with aisle centrelines they form each zone's **travel graph**. |
| **PlacementRule** | Declares which LocationTypes are legal in which Zones. The mechanism that prevents "ambient product in the frozen zone" — enforced once, at registration time. |
| **Facility layout** | The readable, drawable projection of the whole structure: zones → aisles → slots, shaped for a UI to render directly. |

### How PlacementRules evaluate

A rule is `(LocationType, Effect, ZonePredicate)`. The predicate matches on
`zoneCode`, `temperatureClass` and/or `hazmat` — every set field must match
(AND); unset fields are wildcards; at least one must be set.

For a slot of type `T` going into a zone `Z`, `RuleSet.Check` runs, in order:

1. Any **matching `Deny` rule naming `T`** rejects it. Deny always wins.
2. If **any matching `Allow` rule exists for `Z` at all**, the zone becomes an
   allow-list: `T` must be named by one of them, or it is rejected.
3. Otherwise the zone is unconstrained and the placement is permitted.

The rejection error always names the specific rule that was violated.

### Lifecycle

Decommission is **one-way** in v1 for every aggregate: there is no
reactivation use case, and re-registering a decommissioned LocationCode is
rejected as a duplicate rather than quietly resurrecting the slot.
`UnderMaintenance` is a legal persisted state (e.g. loaded from an external
facility-management system) that the read models render; v1 exposes no use
case that sets it.

---

## Domain events (Published Language)

`SiteRegistered`, `ZoneRegistered`, `AisleRegistered`,
`LocationTypeRegistered`, `PlacementRuleDefined`, `LocationSlotRegistered`,
`LocationSlotDecommissioned`, `FacilityLayoutImported`,
`LocationGeometryUpdated`, `AisleGeometryUpdated`,
`FixedStructureRegistered`, `CrossAisleRegistered`.

With `EVENT_PUBLISHER=kafka` all twelve are published to
`warehouse.facility.events` (specified in `apis/asyncapi.yaml`) and to
`warehouse.facility.analytics`. `inventory-storage` consumes
`ZoneRegistered`, `LocationSlotRegistered` and `LocationSlotDecommissioned`
from the integration topic; `warehouse-planning` consumes
`LocationSlotRegistered` and `LocationSlotDecommissioned`.

Every Kafka message (integration and analytics topics) is a **CloudEvents 1.0**
event in structured content mode — mandatory, no other envelope
([ADR-0024](docs/docs/adr/0024-cloudevents-mandatory-envelope.md)):
`source=/warehouse/facility-layout`, `subject`=aggregate id,
`dataschema=urn:warehouse:facility-layout:<events|analytics>:<EventName>:v1`,
`data`=the domain event's own JSON, Kafka header
`content-type: application/cloudevents+json; charset=UTF-8`.

CloudEvents `type` convention, identical to the other warehouse-systems services:

```
com.warehouse.<subdomain>.facility-layout.<entity>.<EventName>
```

This service's subdomain segment is `wms`. Examples:

```
com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered
com.warehouse.wms.facility-layout.zone.ZoneRegistered
com.warehouse.wms.facility-layout.placementrule.PlacementRuleDefined
```

---

## Running it

### Local, no database (in-memory adapters)

```sh
go run ./cmd/facility
# {"time":"...","level":"INFO","msg":"telemetry configured","service_name":"facility-layout","service_version":"dev","environment":"local","otlp_endpoint":"localhost:4317"}
# {"time":"...","level":"INFO","msg":"database url not configured; using in-memory adapters"}
# {"time":"...","level":"INFO","msg":"http server listening","addr":":8080"}
```

### With Postgres

```sh
docker compose up -d postgres
export DATABASE_URL="postgres://facility:facility@localhost:5432/facility?sslmode=disable"
go run ./cmd/facility          # migrations run automatically at startup
```

Migrations can also be applied standalone:

```sh
migrate -source file://migrations -database "$DATABASE_URL" up
```

### Configuration

| Env var | Default | Meaning |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Listen address |
| `DATABASE_URL` | *(unset)* | Postgres DSN. Unset ⇒ in-memory adapters + log-only event publishing |
| `EVENT_PUBLISHER` | *(unset)* | `kafka` publishes the full Published Language to `warehouse.facility.events` (and the analytics topic); with `DATABASE_URL` also set, publishing goes through the transactional outbox (ADR-0018) and a background relay drains it — unset ⇒ log publisher |
| `KAFKA_BROKERS` | `localhost:9092` | Comma-separated broker list, used when `EVENT_PUBLISHER=kafka` |
| `OUTBOX_RELAY_INTERVAL` | `1s` | How long the outbox relay sleeps between empty passes (ADR-0018; only relevant with `DATABASE_URL` + `EVENT_PUBLISHER=kafka`) |
| `HOUSEKEEPING_INTERVAL` | `1h` | Housekeeping sweeper interval (ADR-0026; `0` disables; only relevant with `DATABASE_URL`) |
| `IDEMPOTENCY_KEY_TTL` | `24h` | How long an idempotency_keys row is kept before the sweeper deletes it (ADR-0026; `0` disables) |
| `OUTBOX_RETENTION` | `168h` | How long a PUBLISHED outbox_events row is kept before the sweeper deletes it (ADR-0026; `0` disables) |
| `MIGRATIONS_PATH` | `migrations` | Directory of golang-migrate SQL files |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error`, case-insensitive |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | OTel Collector, `host:port` or a full URL |
| `OTEL_SERVICE_NAME` | `facility-layout` | `service.name` on every span, metric and log record |
| `SERVICE_VERSION` | `dev` | `service.version`; overridden by `-ldflags "-X main.serviceVersion=..."` |
| `ENVIRONMENT` | `local` | `deployment.environment.name` |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:5173,http://localhost:5186` | Comma-separated browser origins allowed to call the API (the `web/` micro-frontend) |

`cmd/mcp` reads `MCP_ADDR` (default `:8090`), `DATABASE_URL`,
`MIGRATIONS_PATH` and `REPORTS_BASE_URL`.

See [Observability](#observability) for what each of the OTel variables
actually changes.

### Container

```sh
docker build -t claudioed/facility-layout .
docker run --rm -p 8080:8080 claudioed/facility-layout
```

### Analytics data product (Layout Catalog Growth & Change)

An additive analytical read side (ADR-0010) built from this service's own domain
events, delivered on a **separate** Kafka topic `warehouse.facility.analytics`,
projected into a **separate** analytical database, and served read-only. It is
three processes with one writer; the OLTP `cmd/facility` binary is unchanged
except that with `EVENT_PUBLISHER=kafka` it now fans events out to BOTH the
integration topic and the analytics topic.

```sh
docker compose up -d postgres
# Kafka itself is NOT in docker-compose.yml: the fleet runs one shared
# broker (in-cluster Bitnami via warehouse-infra) with externalAccess
# exposed on localhost:9092, which is why KAFKA_BROKERS defaults there.
# Point at that broker, or your own, for a local run.

# OLTP service, fanning out to both warehouse.facility.events and .analytics
export DATABASE_URL="postgres://facility:***@localhost:5432/facility?sslmode=disable"
EVENT_PUBLISHER=kafka KAFKA_BROKERS=localhost:9092 go run ./cmd/facility

# Projector (the ONLY writer): consumes the analytics topic, runs migrations/analytics,
# projects into the analytical database.
export ANALYTICS_DATABASE_URL="postgres://facility:***@localhost:5432/facility_analytics?sslmode=disable"
KAFKA_BROKERS=localhost:9092 go run ./cmd/facility-projector    # admin/health on :8091

# Reports (read-only reader): serves the report over REST.
ANALYTICS_DATABASE_URL="$ANALYTICS_DATABASE_URL" go run ./cmd/facility-reports   # :8092

# Query the report (bucketed by DAY):
curl "http://localhost:8092/reports/catalog-growth?from=2026-01-01T00:00:00Z&to=2026-12-31T00:00:00Z"
curl "http://localhost:8092/reports/catalog-growth/freshness"

# Expose it as a curated MCP tool by pointing the MCP server at the reports service:
REPORTS_BASE_URL="http://localhost:8092" go run ./cmd/mcp
```

Analytics processes are trace-free (facility-layout has no OTel package for
them). `ANALYTICS_DATABASE_URL` should point at a **separate** database with a
**read-only role** for `cmd/facility-reports`; the reader additionally pins each
connection to `default_transaction_read_only=on` for defence in depth. See
[the report contract](docs/docs/analytics/catalog-growth-report.md).

| Env var | Default | Meaning |
|---|---|---|
| `ANALYTICS_DATABASE_URL` | *(required for projector/reports)* | Analytical Postgres DSN (separate DB from `DATABASE_URL`) |
| `ANALYTICS_MIGRATIONS_PATH` | `migrations/analytics` | Analytical golang-migrate SQL files (projector only) |
| `ADMIN_ADDR` | `:8091` | Projector health endpoint |
| `HTTP_ADDR` | `:8092` | Reports REST listen address (reader) |
| `REPORTS_BASE_URL` | *(unset)* | When set, `cmd/mcp` registers `get_facility_catalog_growth_report` calling the reports REST |


---

## Observability

The service exports **all three OpenTelemetry signals — traces, metrics and
logs — over OTLP/gRPC** to a Collector. Nothing is scraped from the pod;
there is no `/metrics` endpoint, because the Collector owns Prometheus
exposition for the whole fleet.

### Configuration

| Env var | Default | Meaning |
|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector's gRPC receiver. Both `host:4317` and `http://host:4317` are accepted |
| `OTEL_SERVICE_NAME` | `facility-layout` | `service.name` resource attribute |
| `SERVICE_VERSION` | `dev` | `service.version`; a binary built with `-ldflags "-X main.serviceVersion=v1.2.3"` reports that instead |
| `ENVIRONMENT` | `local` | `deployment.environment.name` resource attribute |
| `LOG_LEVEL` | `info` | Minimum level for both the stdout JSON logs and the OTLP log export |

A Collector is **expected** at `OTEL_EXPORTER_OTLP_ENDPOINT`, but is never
required: the OTLP exporters are non-blocking, so with nothing listening the
service starts, serves and shuts down normally and the telemetry is simply
dropped. In the `warehouse-infra` kind cluster the Helm chart points this at
the in-cluster Collector Service
(`otel-collector.observability.svc.cluster.local:4317`).

### What gets exported

**Traces**

- One server span per HTTP request, via
  [`otelchi`](https://github.com/riandyrn/otelchi). Span names are the chi
  **route pattern** (`GET /locations/{locationCode}`), never the raw path,
  so one span name does not become one span name per location code.
- A child span per database call, via
  [`otelpgx`](https://github.com/exaring/otelpgx) installed as the pgxpool
  tracer. The SQL statement is recorded normalized — no literal values.
- W3C `traceparent` is both extracted from inbound requests and injected
  into outbound context, so a call arriving from another warehouse-systems
  service continues that service's trace rather than starting a new one.

**Metrics**

| Instrument | Kind | Source |
|---|---|---|
| `http.server.request.duration` | histogram (seconds) | otelchi, per OTel HTTP semantic conventions |
| `http.server.active_requests` | up/down counter | otelchi |
| `facility.location_slot.registrations` | counter, attribute `outcome` | **this service's business metric** |
| `go.*` (goroutines, GC, memory) | various | `contrib/instrumentation/runtime` |
| `pgxpool_*` | various | otelpgx pool stats (only with `DATABASE_URL` set) |

`facility.location_slot.registrations` is recorded in the
`RegisterLocationSlot` **use case**, not in the HTTP handler, so a slot
created by `POST /locations/import` counts exactly like one created by
`POST /locations`. Its `outcome` attribute is one of:

- `accepted` — the slot now exists on the map
- `rejected_by_placement_rule` — the chain of custody resolved but a
  `PlacementRule` forbids that `LocationType` in that zone; this is the
  invariant the service exists to enforce, so it gets its own value
- `rejected` — any other refusal (unknown/inactive parent, duplicate code,
  unknown location type, invalid capacity)

**Logs**

Structured `log/slog` JSON to stdout *and* the same records bridged to OTLP
via `contrib/bridges/otelslog`. Any record emitted inside an active span
also carries `trace_id` and `span_id`, which is what joins a log line to its
trace:

```json
{"time":"2026-08-23T20:03:23.32Z","level":"INFO","msg":"http request","method":"POST","path":"/locations","status":201,"duration_ms":0,"bytes":302,"request_id":"...","trace_id":"69b0970c53414467b350b36f7a1b04ac","span_id":"27d2246a94c0462b"}
```

The OpenTelemetry SDK's own diagnostics are bridged onto `slog` too, so the
process never emits a non-JSON log line.

### Layering

Everything OTel lives in `internal/adapters/outbound/telemetry` — an
outbound adapter, the same tier as `outbound/postgres`. The domain layer is
untouched. The application layer knows only about the
`ports.LocationMetrics` interface; a use case with no recorder wired behaves
identically, so telemetry is never load-bearing.

### Trying it locally

```sh
# No collector running: the service must still work.
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:14317 \
  ENVIRONMENT=smoketest SERVICE_VERSION=smoke-1 go run ./cmd/facility

curl -s localhost:8080/healthz
# ... and the log line for that request carries trace_id/span_id.
```

---

## API

Errors use **RFC 7807** (`application/problem+json`) throughout, from day
one. Every `201` carries a `Location` header that resolves.

### Idempotency

Every true resource-creation `POST` (`/sites`, `/sites/{siteCode}/zones`,
`/sites/{siteCode}/structures`, `/zones/{zoneId}/aisles`,
`/zones/{zoneId}/cross-aisles`, `/location-types`, `/placement-rules`,
`/locations`) requires a caller-supplied `Idempotency-Key` header **when the
service is running with `DATABASE_URL` set** (Postgres-backed
`IdempotencyPool`; in-memory/no-database runs skip the check entirely). A
retry with the same key and the same request body replays the original
response verbatim instead of double-creating; the same key with a
different body is rejected `422`. Missing the header on a protected route is
`400`. `POST /locations/import` (bulk, partial-success) and
`POST /locations/{locationCode}/decommission` (acts on an existing resource)
are deliberately excluded. See
[ADR-0019](docs/docs/adr/0019-idempotency-key-middleware.md).

### Structural (write side)

| Method | Path | Purpose | Success |
|---|---|---|---|
| `POST` | `/sites` | RegisterSite | `201` + `Location` |
| `GET` | `/sites` | List sites | `200` |
| `GET` | `/sites/{siteCode}` | Get one site | `200` |
| `POST` | `/sites/{siteCode}/zones` | RegisterZone | `201` + `Location` |
| `GET` | `/sites/{siteCode}/zones` | List a site's zones | `200` |
| `GET` | `/zones/{zoneId}` | Get one zone | `200` |
| `POST` | `/zones/{zoneId}/aisles` | RegisterAisle | `201` + `Location` |
| `GET` | `/zones/{zoneId}/aisles` | List a zone's aisles (walk order) | `200` |
| `GET` | `/zones/{zoneId}/aisles/{aisleCode}` | Get one aisle | `200` |
| `POST` | `/location-types` | RegisterLocationType | `201` + `Location` |
| `GET` | `/location-types` | List location types | `200` |
| `GET` | `/location-types/{name}` | Get one location type | `200` |
| `POST` | `/placement-rules` | DefinePlacementRule | `201` + `Location` |
| `GET` | `/placement-rules` | List placement rules | `200` |
| `GET` | `/placement-rules/{ruleId}` | Get one placement rule | `200` |
| `POST` | `/locations` | RegisterLocationSlot | `201` + `Location` |
| `GET` | `/locations/{locationCode}` | Get one slot | `200` |
| `POST` | `/locations/{locationCode}/decommission` | DecommissionLocationSlot | `204` |
| `POST` | `/locations/import` | ImportFacilityLayout (bulk, partial success) | `200` |
| `PUT` | `/locations/{locationCode}/geometry` | SetLocationGeometry (position, dimensions, pick sequence) | `200` |
| `PUT` | `/zones/{zoneId}/aisles/{aisleCode}/geometry` | SetAisleGeometry (travel centreline) | `200` |
| `POST` | `/zones/{zoneId}/cross-aisles` | RegisterCrossAisle | `201` |
| `GET` | `/zones/{zoneId}/cross-aisles` | List a zone's cross-aisles | `200` |
| `POST` | `/sites/{siteCode}/structures` | RegisterFixedStructure | `201` + `Location` |
| `GET` | `/sites/{siteCode}/structures` | List a site's fixed structures | `200` |

The four single-resource `GET`s (`/zones/{zoneId}`,
`/zones/{zoneId}/aisles/{aisleCode}`, `/location-types/{name}`,
`/placement-rules/{ruleId}`) exist so that every `201`'s `Location` header
points at something that actually has a representation — a `Location` with no
`GET` behind it is not REST maturity level 2.

### "Draw the warehouse" (read side)

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/sites/{siteCode}/layout` | Full nested structure: zones → aisles → slots, pre-ordered for rendering |
| `GET` | `/sites/{siteCode}/layout?format=svg` | The same data rendered server-side as an SVG floor plan |
| `GET` | `/zones/{zoneId}/grid` | One zone as an explicit 2D matrix: rows = Level, columns = (Aisle, Bay) in walk order |
| `GET` | `/sites/{siteCode}/locations?role=` | Every slot at a site with one `LocationRole` (e.g. all `Dock` doors) |
| `GET` | `/locations/{locationCode}/classification` | The slot's resolved `hazmat` / `temperatureClass` (ADR-0008) |
| `GET` | `/zones/{zoneId}/travel-graph` | The zone's travel graph: aisle/bay waypoints and metre-weighted edges |
| `GET` | `/distance?from=&to=` | Shortest travel distance between two slots: `{metresM, estimated, route}` — routed over the zone's travel graph within a zone; across zones a straight-line estimate (`estimated: true`) only when both slots have position geometry, else `422 no-route-between-zones` |
| `GET` | `/healthz` | Liveness |
| `GET` | `/readyz` | Readiness (flips not-ready at the start of graceful shutdown, ADR-0020) |

Every route above, including `/healthz` and `/readyz`, is declared at the
same path in `apis/openapi.yaml` (33 operations over 26 paths).

### Status codes

| Code | When |
|---|---|
| `200` | Successful read, or a bulk-import report |
| `201` | Resource created (always with `Location`) |
| `204` | Successful action with no body (decommission) |
| `400` | Malformed input: bad JSON, a location code that is not seven `[A-Z0-9]` segments, a missing required identifier, a resource-creation POST missing its required `Idempotency-Key` header |
| `404` | The named site/zone/aisle/slot/type/rule does not exist |
| `409` | State conflict: a code already taken, a parent that exists but is not Active, a slot already decommissioned |
| `422` | Semantically invalid: non-positive capacity, unknown enum value, **PlacementRule violation**, missing `dockFlow`/`activities` for a Dock/WorkCenter, no route between two locations, an `Idempotency-Key` reused with a different request body |

---

## Worked example

Every response below is **real output** captured from a running instance.

### 1 — Register the site

```sh
curl -i -X POST localhost:8080/sites -H 'Content-Type: application/json' \
  -d '{"siteCode":"WH1","name":"Fulfilment Centre One"}'
```

```http
HTTP/1.1 201 Created
Content-Type: application/json
Location: /sites/WH1

{"siteCode":"WH1","name":"Fulfilment Centre One","status":"Active"}
```

### 2 — Register a zone

```sh
curl -i -X POST localhost:8080/sites/WH1/zones -H 'Content-Type: application/json' \
  -d '{"areaCode":"STOR","zoneCode":"AMB","temperatureClass":"Ambient","hazmat":false}'
```

```http
HTTP/1.1 201 Created
Content-Type: application/json
Location: /zones/WH1-STOR-AMB

{"zoneId":"WH1-STOR-AMB","siteCode":"WH1","areaCode":"STOR","zoneCode":"AMB","temperatureClass":"Ambient","hazmat":false,"status":"Active"}
```

### 3 — Register an aisle

```sh
curl -i -X POST localhost:8080/zones/WH1-STOR-AMB/aisles -H 'Content-Type: application/json' \
  -d '{"aisleCode":"A07","sequenceHint":7,"direction":"TwoWay"}'
```

```http
HTTP/1.1 201 Created
Content-Type: application/json
Location: /zones/WH1-STOR-AMB/aisles/A07

{"aisleId":"WH1-STOR-AMB-A07","zoneId":"WH1-STOR-AMB","aisleCode":"A07","sequenceHint":7,"direction":"TwoWay","status":"Active"}
```

### 4 — Register a location type

```sh
curl -i -X POST localhost:8080/location-types -H 'Content-Type: application/json' \
  -d '{"name":"PalletRack","defaultCapacity":{"maxWeightKg":1200,"maxVolumeM3":2.4}}'
```

```http
HTTP/1.1 201 Created
Content-Type: application/json
Location: /location-types/PalletRack

{"name":"PalletRack","role":"Storage","defaultCapacity":{"maxWeightKg":1200,"maxVolumeM3":2.4}}
```

### 5 — Define a placement rule

Pallet racking is not rated for the cold, so deny it anywhere `Frozen`:

```sh
curl -i -X POST localhost:8080/placement-rules -H 'Content-Type: application/json' \
  -d '{"ruleId":"RULE-FRZ-NO-SHELF","locationType":"PalletRack","effect":"Deny","zone":{"temperatureClass":"Frozen"}}'
```

```http
HTTP/1.1 201 Created
Content-Type: application/json
Location: /placement-rules/RULE-FRZ-NO-SHELF

{"ruleId":"RULE-FRZ-NO-SHELF","locationType":"PalletRack","effect":"Deny","zone":{"temperatureClass":"Frozen"},"description":"RULE-FRZ-NO-SHELF: Deny PalletRack where temperatureClass=Frozen"}
```

### 6 — Register a slot

```sh
curl -i -X POST localhost:8080/locations -H 'Content-Type: application/json' \
  -d '{"locationCode":"WH1-STOR-AMB-A07-03-02-B","locationType":"PalletRack"}'
```

```http
HTTP/1.1 201 Created
Content-Type: application/json
Location: /locations/WH1-STOR-AMB-A07-03-02-B

{"locationCode":"WH1-STOR-AMB-A07-03-02-B","zoneId":"WH1-STOR-AMB","aisleId":"WH1-STOR-AMB-A07","coordinates":{"site":"WH1","area":"STOR","zone":"AMB","aisle":"A07","bay":"03","level":"02","position":"B"},"locationType":"PalletRack","role":"Storage","capacity":{"maxWeightKg":1200,"maxVolumeM3":2.4},"status":"Active"}
```

(Two more slots were added the same way for the examples below:
`WH1-STOR-AMB-A07-03-02-A` and `WH1-STOR-AMB-A07-03-01-A`.)

### 7 — GET the layout

```sh
curl localhost:8080/sites/WH1/layout
```

```json
{
    "site": {
        "siteCode": "WH1",
        "name": "Fulfilment Centre One",
        "status": "Active"
    },
    "zones": [
        {
            "zoneId": "WH1-STOR-AMB",
            "siteCode": "WH1",
            "areaCode": "STOR",
            "zoneCode": "AMB",
            "temperatureClass": "Ambient",
            "hazmat": false,
            "status": "Active",
            "aisles": [
                {
                    "aisleId": "WH1-STOR-AMB-A07",
                    "zoneId": "WH1-STOR-AMB",
                    "aisleCode": "A07",
                    "sequenceHint": 7,
                    "direction": "TwoWay",
                    "status": "Active",
                    "slots": [
                        {
                            "locationCode": "WH1-STOR-AMB-A07-03-01-A",
                            "zoneId": "WH1-STOR-AMB",
                            "aisleId": "WH1-STOR-AMB-A07",
                            "coordinates": {
                                "site": "WH1", "area": "STOR", "zone": "AMB", "aisle": "A07",
                                "bay": "03", "level": "01", "position": "A"
                            },
                            "locationType": "PalletRack",
                            "role": "Storage",
                            "capacity": { "maxWeightKg": 1200, "maxVolumeM3": 2.4 },
                            "status": "Active"
                        },
                        {
                            "locationCode": "WH1-STOR-AMB-A07-03-02-A",
                            "zoneId": "WH1-STOR-AMB",
                            "aisleId": "WH1-STOR-AMB-A07",
                            "coordinates": {
                                "site": "WH1", "area": "STOR", "zone": "AMB", "aisle": "A07",
                                "bay": "03", "level": "02", "position": "A"
                            },
                            "locationType": "PalletRack",
                            "role": "Storage",
                            "capacity": { "maxWeightKg": 1200, "maxVolumeM3": 2.4 },
                            "status": "Active"
                        },
                        {
                            "locationCode": "WH1-STOR-AMB-A07-03-02-B",
                            "zoneId": "WH1-STOR-AMB",
                            "aisleId": "WH1-STOR-AMB-A07",
                            "coordinates": {
                                "site": "WH1", "area": "STOR", "zone": "AMB", "aisle": "A07",
                                "bay": "03", "level": "02", "position": "B"
                            },
                            "locationType": "PalletRack",
                            "role": "Storage",
                            "capacity": { "maxWeightKg": 1200, "maxVolumeM3": 2.4 },
                            "status": "Active"
                        }
                    ]
                }
            ]
        }
    ],
    "fixedStructures": [],
    "totals": {
        "zones": 1,
        "aisles": 1,
        "slots": 3
    }
}
```

Zones come back ordered by id, aisles in **`sequenceHint` walk order** (not
registration order), and slots ordered bay → level → position. A client
renders it top-down with no joins and no sorting.

### 8 — GET the grid

```sh
curl localhost:8080/zones/WH1-STOR-AMB/grid
```

```json
{
    "zone": {
        "zoneId": "WH1-STOR-AMB",
        "siteCode": "WH1",
        "areaCode": "STOR",
        "zoneCode": "AMB",
        "temperatureClass": "Ambient",
        "hazmat": false,
        "status": "Active"
    },
    "columns": [
        { "aisleId": "WH1-STOR-AMB-A07", "aisleCode": "A07", "bay": "03", "sequenceHint": 7 }
    ],
    "levels": [ "01", "02" ],
    "rows": [
        {
            "level": "01",
            "cells": [
                { "positions": [
                    { "locationCode": "WH1-STOR-AMB-A07-03-01-A", "position": "A", "locationType": "PalletRack", "status": "Active" }
                ] }
            ]
        },
        {
            "level": "02",
            "cells": [
                { "positions": [
                    { "locationCode": "WH1-STOR-AMB-A07-03-02-A", "position": "A", "locationType": "PalletRack", "status": "Active" },
                    { "locationCode": "WH1-STOR-AMB-A07-03-02-B", "position": "B", "locationType": "PalletRack", "status": "Active" }
                ] }
            ]
        }
    ]
}
```

`rows[i].cells` is **index-aligned with `columns`**, and a cell is literal
JSON `null` where the rack has a gap. Iterate rows × columns and paint.

### 9 — GET the SVG floor plan

```sh
curl "localhost:8080/sites/WH1/layout?format=svg" -o wh1.svg
open wh1.svg
```

```
200 image/svg+xml
```

```xml
<svg xmlns="http://www.w3.org/2000/svg" width="640" height="150" viewBox="0 0 640 150">
  <title>WH1 facility layout</title>
  <rect x="0" y="0" width="640" height="150" fill="#ffffff"/>
  <text x="24" y="26" font-family="monospace" font-size="16" font-weight="bold" fill="#1f2933">WH1 — Fulfilment Centre One</text>
  ...
</svg>
```

One colored band per zone (hue = temperature class, amber = hazmat), one row
of rects per aisle, one rect per slot with a `<title>` tooltip. It is a thin
adapter-only concern: the render function lives in the HTTP layer and
consumes the same read model the JSON endpoint does.

---

## Bulk import

`POST /locations/import` takes a JSON array of fully-specified rows and
bootstraps a whole building in one call, creating the Site/Zone/Aisle
structure on first sight. Validation is **atomic per row**: every row is
processed, and the report says exactly which rows failed and why. A 500-row
export with 3 bad rows still creates the other 497.

```sh
curl -X POST localhost:8080/locations/import -H 'Content-Type: application/json' -d '[
 {"siteCode":"WH1","siteName":"Fulfilment Centre One","areaCode":"STOR","zoneCode":"FRZ","temperatureClass":"Frozen","hazmat":false,"aisleCode":"A02","sequenceHint":2,"direction":"TwoWay","bay":"01","level":"01","position":"A","locationType":"PalletRack"},
 {"siteCode":"WH1","areaCode":"STOR","zoneCode":"FRZ","temperatureClass":"Frozen","hazmat":false,"aisleCode":"A02","sequenceHint":2,"direction":"TwoWay","bay":"01","level":"01","position":"b","locationType":"PalletRack"}
]'
```

```json
{
    "rowsSubmitted": 2,
    "slotsImported": 0,
    "rowsRejected": 2,
    "results": [
        {
            "index": 0,
            "locationCode": "WH1-STOR-FRZ-A02-01-01-A",
            "succeeded": false,
            "error": "location type violates a placement rule for this zone: PalletRack is denied in zone WH1-STOR-FRZ by rule [RULE-FRZ-NO-SHELF: Deny PalletRack where temperatureClass=Frozen]"
        },
        {
            "index": 1,
            "locationCode": "",
            "succeeded": false,
            "error": "location code segment must contain only uppercase letters and digits: position segment \"b\""
        }
    ]
}
```

Row 0 was rejected by the rule defined in step 5 — this is the
"ambient product in the frozen zone" guard doing its job, at registration
time, naming the exact rule violated. Row 1 was rejected by the
`LocationCode` value object before it ever reached the domain.

The endpoint answers `200 OK`, not `201`: a bulk import is a partial-success
report over many rows, not the creation of one addressable resource, so there
is no single `Location` to hand back.

---

## Errors (RFC 7807)

```sh
curl -i localhost:8080/sites/NOPE
```

```http
HTTP/1.1 404 Not Found
Content-Type: application/problem+json

{"type":"https://errors.facility-layout.warehouse-systems.dev/site-not-found","title":"Site not found","status":404,"detail":"site not found","instance":"/sites/NOPE"}
```

```sh
curl -i -X POST localhost:8080/locations -H 'Content-Type: application/json' \
  -d '{"locationCode":"WH1-STOR-AMB-A07-03-02-B","locationType":"PalletRack"}'
```

```http
HTTP/1.1 409 Conflict
Content-Type: application/problem+json

{"type":"https://errors.facility-layout.warehouse-systems.dev/duplicate-location-code","title":"A location slot with this code already exists","status":409,"detail":"a location slot with this code already exists","instance":"/locations"}
```

---

## API specification

`apis/openapi.yaml` (OpenAPI 3.0.3) documents every route with full
request/response schemas, a shared `Problem` component, and real
domain-grounded examples. `apis/asyncapi.yaml` (AsyncAPI 2.6.0) specifies the
`warehouse.facility.events` topic, its CloudEvents 1.0 envelope (ADR-0024) and all twelve messages. The
OpenAPI document is linted in CI:

```sh
spectral lint apis/openapi.yaml --ruleset .spectral.yaml --fail-severity=warn
```

---

## Local development / quality gate

Every CI sensor is also a `make` target, so the same feedback is available
before a commit leaves the machine. `make help` lists them all.

```sh
make check-fast # quickest gate: fmt-check, vet, arch-test
make check      # fast pre-commit loop: fmt-check, vet, build, lint, test
make check-all  # pre-push gate: check + coverage (90% gate) + arch-test + bdd
make vuln       # govulncheck ./... (supply-chain sensor)
make mutation   # gremlins over ./internal/domain, thresholds from .gremlins.yaml
```

`make check` needs no database and runs in well under a minute; `make
integration` (testcontainers Postgres/Kafka — needs only Docker) and the exhaustive scheduled mutation run are
deliberately outside both bundles.

Git hooks are managed by [lefthook](https://github.com/evilmartians/lefthook)
and configured in `lefthook.yml` (pre-commit: `fmt-check` + `vet` + `lint`;
pre-push: `make check`). Activate them once per clone:

```sh
brew install lefthook          # or: go install github.com/evilmartians/lefthook@latest
lefthook install
```

## Tests & quality gates

```sh
go build ./...
go vet ./...
go test ./... -race
gofmt -l .
golangci-lint run ./...

# combined domain + application coverage (CI gate: >= 90%)
go test ./internal/domain/... ./internal/application/... -race \
  -coverprofile=coverage.out \
  -coverpkg=./internal/domain/...,./internal/application/...
go tool cover -func=coverage.out | tail -1

# integration tests (build-tagged; testcontainers boots its own Postgres/Kafka — needs only Docker)
go test -tags=integration ./... -race -count=1

# BDD acceptance suite (godog/Gherkin, over the real HTTP API)
go test ./... -run TestFeatures -v

# architecture fitness tests (arch-go)
go test ./internal/architecture/... -v

# mutation testing — blocking fast run, config in .gremlins.yaml (see MUTATION.md)
gremlins unleash ./internal/domain

# vulnerability scan (dependencies + Go stdlib)
govulncheck ./...
```

CI (`.github/workflows/ci.yml`) runs `lint`, `complexity` (gocyclo/gocognit/
cyclop/funlen/nestif gates), `test` (with the 90% coverage gate), `bdd`,
`contract` (Schemathesis property-based tests over `apis/openapi.yaml`),
`evals-tests` (the MCP eval suites as their own named, individually-gating
check), `integration`, `mutation-fast` (the blocking mutation subset,
thresholds from `.gremlins.yaml`), `vuln` (govulncheck), `api-lint`,
`docs-api-drift` (regenerates the REST reference from `apis/openapi.yaml`
and fails if it differs from what's committed), `helm-lint`, `arch-test` and
`web` (lint/typecheck/test/build of `web/` against `warehouse-ui-kit`) on
every push and PR; `trivy-scan` builds the image and blocks on
CRITICAL/HIGH CVEs with a fix, but only for a PR targeting `main`; the
exhaustive `mutation` job and the advisory `drift` sensor (dead code,
`go mod tidy -diff`, unused frontend exports, mutation-vs-coverage quality)
run only on schedule/manual dispatch; `docker-publish` pushes to GHCR on
`main` once every other job is green, and `release` then tags the next
`vX.Y.Z` version and publishes the Helm chart alongside it. Dependency
drift is watched by `.github/dependabot.yml` (gomod + github-actions,
weekly).

## Helm chart

```sh
helm install facility-layout ./charts/facility-layout \
  --set image.repository=claudioed/facility-layout \
  --set database.url="postgres://facility:facility@postgres:5432/facility?sslmode=disable"
```

Telemetry is a first-class `otel` values block (see
[Observability](#observability)), on by default and safe to leave on even
where no Collector is deployed:

```yaml
otel:
  enabled: true
  endpoint: "http://otel-collector.observability.svc.cluster.local:4317"
  serviceName: "facility-layout"

environment: "local"     # deployment.environment.name
```

`SERVICE_VERSION` is taken from `.Values.image.tag`, falling back to the
chart's `appVersion`, so a deployed pod always reports the image it is
actually running.

### Running the MCP server in Kubernetes

The MCP server (ADR-0007) ships in the same image as `/app/mcp` and is a
separate, opt-in deployable: set `mcp.enabled=true` and the chart renders a
`<release>-mcp` Deployment and a ClusterIP Service on port `8090`. The pod
reads the same `DATABASE_URL` secret as the main deployment, runs the OLTP
migrations on start (idempotent), and — when `analytics.enabled=true` — is
wired to the in-cluster reports Service so the catalog-growth report tool
is registered. Its surface is read-only: six tools (`list_sites`,
`get_site_layout`, `get_zone_grid`, `list_functional_locations`,
`get_zone_travel_graph`, `estimate_travel_distance`) plus
`get_facility_catalog_growth_report` when `REPORTS_BASE_URL` is set, the
`layout://facility/{siteCode}` resource template, and the `explore_layout`
prompt. `GET /healthz` backs the liveness/readiness probes; the MCP
Streamable HTTP endpoint is served at both `/` and `/mcp`, so a client
connects to `http://<release>-mcp.<namespace>.svc.cluster.local:8090/mcp`
directly — there is no authentication layer in front of it (the fleet's
static-bearer-key rollout was removed; this deployable is reachable only
from inside the cluster).

```sh
helm upgrade --install facility-layout ./charts/facility-layout \
  --set database.url="postgres://facility:***@postgres:5432/facility?sslmode=disable" \
  --set mcp.enabled=true
```

Linted in CI with `ct lint --charts charts/facility-layout`.

## Operator micro-frontend (`web/`)

`web/` is `facility_mfe`, this context's Module Federation remote. It talks only to
this service's own REST API and is never part of `make check`.

**Standalone development** is unchanged:

```bash
cd web && npm install && npm run dev     # http://localhost:5186
```

**Deployed to the kind cluster**, it is built into a static bundle and served
by its own `nginx-unprivileged` pod:

```bash
cd web
docker build --build-context uikit=../../warehouse-ui-kit \
  -t warehouse/facility-layout-frontend:local .
```

The cluster's localhost topology separates the two kinds of traffic onto two
independent entrypoints, and neither proxies to the other:

| URL | Served by | Carries |
|---|---|---|
| `http://localhost/mfes/facility-layout/` | Nginx web gateway → this remote's nginx pod | HTML, JS, CSS, fonts, `remoteEntry.js` |
| `http://localhost:8000/api/facility-layout/` | Kong | this service's REST API |

Kong never serves frontend assets, and the Nginx gateway never proxies an API.
Enable the workload with `frontend.enabled=true` in the Helm chart; the Service
is deliberately `ClusterIP` with no Ingress/HTTPRoute, because frontend path
routing belongs to the Nginx web gateway in `warehouse-infra`.

Because one image must work in more than one environment, the remote reads its
API origin at runtime from `window.__WAREHOUSE_CONFIG__.apiOrigin` (published
by the console shell) rather than baking a hostname in at build time. A
production build with no runtime config **fails loudly** instead of silently
falling back to a developer port; standalone `npm run dev` still uses
`http://localhost:8081`. See `web/src/config.ts`.

Chart invariants are asserted by:

```bash
python3 charts/facility-layout/tests/test_service_selectors.py
```

which proves every Service selects exactly one Deployment — the OLTP Service
must never select the frontend, analytics or MCP pods.
