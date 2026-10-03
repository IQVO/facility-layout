---
name: how-to-add-an-integration-event
description: Publish or consume a cross-service Kafka event: CloudEvents 1.0 type naming, AsyncAPI, transactional outbox, consumer-group rules. Use when touching internal/adapters kafka or outbox code, a publisher/consumer, or apis/asyncapi*.yaml.
---

# How to add an integration event (publish and consume)

Use when asked to publish a new cross-context integration event from this
service, or to add a Kafka-fed local cache/consumer to this service
itself. This fleet's Kafka is ONE broker platform-wide — every design
decision below exists because that shared-broker reality has already
caused a real incident once (wes-work-planning#67, described below).

facility-layout is unusual in this fleet: it is an **Open Host Service**
whose domain events ARE its Published Language (ADR-0009). It currently
has no cross-service consumer of its own — its downstream services
(inventory-storage via `warehouse.facility.events`; wes-work-planning,
fulfillment-execution and warehouse-ops-agent via REST/MCP) are
Conformists reading FROM it, never the other way around (see this repo's
`AGENTS.md`: "It has NO inbound dependency on any other fleet service and
never will"). So
this guide's "publish" half is this repo's own lived pattern; its
"consume" half documents the rules a future consumer here would have to
follow, plus the one Kafka consumer this repo DOES already run — the
analytics projector, which is same-service, not cross-context.

## Publishing a new integration event

### 1. Is it actually new, or an addition to an existing event?

Unlike a service that forwards a single enriched event, this publisher
(`internal/adapters/outbound/kafka/publisher.go`) emits EVERY domain
event to the integration topic — the whole Published Language, by
design doc comment: "this publisher therefore emits EVERY domain event
to the integration topic". So there is rarely a decision to make about
WHETHER to publish; the real decision is whether your change is a new
event type or an additive field on an existing one, because that
distinction is what ADR-0013 makes into a compatibility contract. ADR-0016
added `role` to the already-live `LocationTypeRegistered`/
`LocationSlotRegistered` events (an additive field); ADR-0017 added four
brand-new event types (`LocationGeometryUpdated`, `AisleGeometryUpdated`,
`FixedStructureRegistered`, `CrossAisleRegistered`) specifically so
ADR-0013's existing field-level contract with inventory-storage's
`facilitycache` consumer stayed untouched. Check
`docs/docs/adr/0013-first-published-language-consumer.md`'s table of
fields a live downstream Conformist depends on before renaming or
removing anything on `ZoneRegistered`, `LocationSlotRegistered`, or
`LocationSlotDecommissioned` — those three are the fields actually
depended on today, not aspirational.

### 2. Envelope: CloudEvents 1.0, structured mode (MANDATORY, ADR-0024)

Every message on `warehouse.facility.events` (`Topic`) AND
`warehouse.facility.analytics` (`AnalyticsTopic`) is a CloudEvents 1.0
event, built ONLY through `internal/adapters/kafka/cloudevents`
(`cloudevents.New`, wrapping `github.com/cloudevents/sdk-go/v2/event`).
There is no flat envelope, no dual mode, no envelope toggle — never add
one. A message on the wire looks like:

```json
{
  "specversion": "1.0",
  "id": "3f2504e0-4f89-41d3-9a0c-0305e82c3301",
  "source": "/warehouse/facility-layout",
  "type": "com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered",
  "subject": "WH1-STOR-AMB-A07-03-02-B",
  "time": "2026-02-03T04:05:06Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:warehouse:facility-layout:events:LocationSlotRegistered:v1",
  "data": { "eventName": "LocationSlotRegistered", "...": "the event's own JSON" }
}
```

with Kafka header `content-type: application/cloudevents+json; charset=UTF-8`.
`data` is the domain event's own JSON verbatim (struct tags ARE the
contract). `type` is the event's own `EventType()`; the `<entity>` segment
is parsed from it, so a new event only needs `newBase("<entity>", ...)` in
`internal/domain/shared/events.go`. The analytics publisher emits the same
`type` and `id` with `dataschema=urn:warehouse:facility-layout:analytics:<EventName>:v1`.
`id` is minted once per event by `OutboxPublisher` and persisted in the
outbox row, so redelivery keeps the same id. A breaking payload change =
new `.v2` type + `:v2` dataschema, never a mutation.

### 3. Partition key and `subject`: the aggregate's identity

`aggregateKey(event)` (`publisher.go:101`) type-switches on the event and
returns its aggregate id (`SiteCode`, `ZoneID`, `AisleID`, etc.) so all
events for one aggregate land on the same partition, preserving
per-aggregate order. Adding a new event type means adding a `case` here
too — `FacilityLayoutImported` and the `default` both fall back to
`event.EventType()` because a bulk-import event has no single natural
aggregate id; that fallback still gives a stable, non-empty key rather
than an empty string. The CloudEvents `subject` comes from `SubjectOf`
(`publisher.go`): it must ALWAYS be the real aggregate id (never the type
fallback) — add a `case` there too if the key falls back.

### 4. Contract + docs

- Add the message to `apis/asyncapi.yaml` under this service's channel.
- Regenerate the AsyncAPI HTML reference:
  ```bash
  cd docs && npm run gen-async-docs:all
  ```
  This repo's `docs-api-drift` CI job fails the PR if the generated
  `static/asyncapi/` output doesn't match a fresh regen.

### 5. Test

Add a row to `goldenCases` in `publisher_test.go` — it asserts the exact
CloudEvent JSON (every attribute, type, data) and the content-type header
for BOTH the integration and analytics publishers, against a fake
`Writer` (see `publisher_test.go`/`analytics_publisher_test.go` — never a
real broker in a unit test). This repo's own architecture fitness test
`TestKafkaIntegrationTestsUseTestcontainers`
(`internal/architecture/fitness_test.go`) scans every
`*_integration_test.go` file that touches `segmentio/kafka-go` and fails
the build if it skip-gates on `os.Getenv("KAFKA_BROKERS")`, hardcodes
`localhost:9092`, or doesn't import
`testcontainers-go/modules/kafka` — enforced statically, not just by
convention, because this fleet's CI `integration` job
(`.github/workflows/ci.yml`) provisions Postgres only. This repo's
current integration tests (`postgres_integration_test.go`,
`travel_integration_test.go`, `telemetry_integration_test.go`,
`repos_integration_test.go`, `geometry_integration_test.go`) are all
Postgres-only, so the fitness test currently has nothing to flag — the
first Kafka-touching integration test added here is the one that has to
get this right.

## Consuming an integration event (this repo's own analytics projector, and any future cross-context consumer)

### 1. Never import a sibling's Go packages

If facility-layout ever consumes a SIBLING service's topic (it doesn't
today — see the intro above), the rule is the one inventory-storage's
`facilitycache/consumer.go` states in its own doc comment: know the
sibling's topic name and payload shape ONLY, never its Go types. Mirror
the payload struct locally rather than adding a module dependency.

### 2. Choose the right consumer-group pattern — this is the part that bites

Two DIFFERENT correct patterns exist. Picking the wrong one for the use
case is THE most common integration-event mistake in this fleet, learned
from a real incident (wes-work-planning#67): a fixed shared consumer
group id on a consumer meant to run as exactly one instance per
environment let a local dev/test harness process join the SAME broker's
SAME group as a live in-cluster Deployment, and Kafka's rebalance
protocol handed the partition to only ONE of the two group members — the
other silently starved.

**Pattern A — long-lived, single-instance consumer group (a named
constant).** Use when exactly ONE instance of this consumer ever runs at
a time. This repo's OWN analytics projector is the concrete example:
`AnalyticsConsumerGroup = "facility-analytics"`
(`internal/adapters/inbound/kafka/analytics_consumer.go:28`) is a plain
named constant, reused across restarts — correct because Kafka's
committed-offset resume semantics are exactly what a single, always-one-
instance projector wants: pick up where it left off. Note this constant
is a named symbol a reader can trace, not an inline literal buried in a
`kafkago.ReaderConfig{...}` call — see the fitness-test point below.

**Pattern B — per-process-unique consumer group (a generated id).** Use
when the consumer rebuilds a complete read model from a topic's FULL
history on every start (an event-sourced local cache, not a fixed
single-instance worker) — see inventory-storage's `facilitycache/
consumer.go` `consumerGroupPrefix` + `uniqueConsumerGroup()` for the
reference shape. The group id MUST be unique per process instance
(hostname+PID+timestamp), NEVER a fixed shared string, because a
brand-new process joining a group an EARLIER instance already consumed
resumes from that instance's committed offset — the new process gets
marked "ready" with an empty local cache having replayed nothing.

**This repo's own fitness test enforces the naming discipline, not the
pattern choice.** `TestKafkaConsumerGroupNeverHardcodedInline`
(`internal/architecture/fitness_test.go`) bans `GroupID: "literal-string"`
anywhere in the codebase — it does NOT ban long-lived named groups (the
projector's `AnalyticsConsumerGroup` constant is a deliberate, explicitly
whitelisted-by-comment exception); it bans an inline string literal a
reviewer cannot trace back to a definition and reasoning. This repo
currently has no cross-context (Pattern B) consumer, so the test has
nothing to flag on that front today — it exists for fleet consistency and
to catch a future one that reintroduces the incident's shape.

### 3. Readiness gate, if a future consumer here backs a local cache

If a consumer replays a topic's full history to build a cache other code
depends on, expose a `Ready()` gate the health check consults, and block
readiness (not process startup) until the initial replay finishes. A
readiness check that only re-evaluates on a NEW message arriving
deadlocks forever on an ordinary restart of a shared/already-caught-up
group. This repo's existing analytics projector sidesteps the whole
question by using Pattern A with `StartOffset: kafkago.FirstOffset`
(`analytics_consumer.go:95`, only affecting the FIRST join since a
committed offset takes precedence after that) plus idempotent
`ProcessedEvents.MarkProcessed` per CloudEvents `id` — no separate readiness
concept was needed because the projector's own read path already handles
"haven't caught up yet" by simply not having the data.

### 4. Decode with `cloudevents.Decode`, dispatch on the full type

Every consumer decodes with `cloudevents.Decode` (SDK unmarshal +
`Validate()`), dispatches on the FULL `type` string (never a suffix or a
short name; unknown types are ignored), reads `time`/`subject` from the
attributes and the payload via `DataAs`, and dedupes on `id`. A value that
fails decoding (`cloudevents.ErrNotCloudEvent`, e.g. a legacy flat
message) is deterministic poison: DLQ it immediately (no retries) or, with
no DLQ, WARN-log topic/partition/offset and commit past it. See
`analytics_consumer.go`.

## Verify before opening the PR

```bash
make check-all    # includes arch-test — will catch a reintroduced
                    # inline GroupID literal or an auth-middleware regression
```

Prove any new fitness-test-adjacent behavior actually matters by running
the specific scenario against a real broker if this repo has
testcontainers-based integration tests for the consumer/publisher
touched.
