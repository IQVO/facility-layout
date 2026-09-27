---
id: 0020-resilience-kafka-dlq-graceful-shutdown
slug: /adr/0020-resilience-kafka-dlq-graceful-shutdown
title: 20. Kafka consumer dead-letter queue and graceful shutdown hardening
sidebar_label: 20. Kafka DLQ + graceful shutdown
sidebar_position: 20
description: "ADR 0020 — Phase 2 resilience for facility-layout, scoped to the two pieces that actually apply to this service: cenkalti/backoff/v4 bounded in-process retry then a dead-letter topic for the analytics Kafka consumer so one poison message cannot block its partition, and a readiness-flip-first graceful shutdown sequence across all three binaries. No circuit breaker is introduced — facility-layout has no synchronous outbound HTTP calls to sibling bounded contexts to wrap; it is Kafka-producer-only plus its own REST API, confirmed from the actual code before writing this record."
---

# 20. Kafka consumer dead-letter queue and graceful shutdown hardening

## Status

Accepted — implemented in the same change that introduced this record.
This is facility-layout's slice of the fleet's Phase 2 (resilience)
production-readiness plan. The reference implementation is
order-management's [PR #107](https://github.com/claudioed/order-management/pull/107)
and its own ADR 0025 ("Per-dependency circuit breakers, read-only retry,
Kafka DLQ, and graceful shutdown hardening") — copied here for the DLQ
and graceful-shutdown pieces **only**. Circuit-breaker work does **not**
apply to this service; see [Scope](#scope-no-circuit-breaker-here) below
for why, confirmed from the actual code rather than assumed.

## Scope: no circuit breaker here

order-management's ADR 0025 wraps two synchronous outbound HTTP clients
(`inventorystorage.Client`, `productclassification.Client`) — calls to
sibling bounded contexts — in per-dependency `gobreaker` circuit
breakers. facility-layout has **no synchronous outbound HTTP calls to
any sibling bounded context**: its only outbound integrations are (a)
Kafka producers (`internal/adapters/outbound/kafka`, ADR 0009's
integration publisher and ADR 0010's analytics publisher) and (b) its
own Postgres pools (OLTP and analytical). It exposes a REST API and an
MCP adapter inbound, but calls nothing else's REST API outbound. There
is therefore nothing here for a circuit breaker to wrap, and this ADR
does not introduce one — inventing a breaker around a call that does
not exist would be exactly the kind of unverified assumption this
record is written to avoid.

## Context

Before this change, facility-layout's inbound analytics Kafka consumer
(`internal/adapters/inbound/kafka/analytics_consumer.go`, consuming
`warehouse.facility.analytics.*` under the `facility-analytics` group
per ADR 0010) had no dead-letter handling: a message whose projection
apply always errors (a malformed payload from a future producer, a
constraint violation) would be retried forever on the same offset by
Kafka's own at-least-once redelivery, permanently blocking every other
event behind it on that partition. Graceful shutdown already existed in
all three binaries (`signal.Notify(syscall.SIGINT, syscall.SIGTERM)` +
`httpServer.Shutdown`/`srv.Shutdown`) but had no readiness-flip step, no
bounded wait for `cmd/facility`'s outbox relay or `cmd/facility-projector`'s
consumer loop to actually finish in-flight work before the pgx pool
closed, and the Helm chart's `readinessProbe` pointed at the same
`/healthz` endpoint as `livenessProbe`/`startupProbe` — so a draining pod
had no way to signal "stop routing me new traffic" without also risking
a liveness failure.

## Decision

### 1. Dead-letter queue for the analytics Kafka consumer

`AnalyticsConsumer.handleMessage` now retries `HandleMessage` in-process
via `handleWithRetry`, jittered exponential backoff
(`cenkalti/backoff/v4`, 100ms–2s) up to `maxHandlerAttempts` (3) total
attempts — mirroring `RepromiseConsumer.handleWithRetry` in
order-management verbatim. Once all attempts are exhausted, the raw,
byte-identical message payload is published — plus
`x-dlq-source-topic`/`x-dlq-error`/`x-dlq-failed-at` headers carrying
replay/debugging context — to `<source-topic>.dlq` via a `*kafkago.Writer`
the consumer now owns (`AnalyticsConsumer.dlqWriter`, closed alongside
the reader in `Close`), and **the offset is committed anyway**: one
poison message must never permanently block every event behind it on
the same partition. This is logged at ERROR level
(`"analytics: exhausted retries, sending to dead-letter topic"`) with
topic/dlq_topic/attempts/error context. `NewAnalyticsConsumer` derives
the DLQ topic as `<its own source topic>+".dlq"` (never a fixed
constant), so an isolated integration-test topic gets its own isolated
DLQ topic automatically, exactly like order-management's constructor.

#### 1a. A pre-existing idempotency-ordering bug, found and fixed while adding the retry

`HandleMessage`'s original shape called `ProcessedEvents.MarkProcessed`
(claim) **before** calling the matching `ProjectionStore.Apply*`
method. That ordering, combined with the new in-process retry, would
have silently defeated the DLQ entirely: if `Apply*` failed on attempt
1, `MarkProcessed` had already claimed the event id, so attempt 2 would
see `isNew=false` (in the interface's original two-bool shape) and
return `nil` — a false "success" — without ever calling `Apply*` again,
and the poison message would never reach the DLQ at all. This is not
hypothetical: it was caught by first driving the real
`TestAnalyticsConsumer_PoisonMessage_GoesToDeadLetterTopicWithoutBlockingPartition`
integration test against a real testcontainers Kafka broker and
observing the poison message being silently acknowledged instead of
dead-lettered.

The fix reorders `HandleMessage` to **apply first, then claim**:
`ProcessedEvents` gained a read-only `IsProcessed` pre-check (consulted
before `Apply*`, to still skip a genuine at-least-once redelivery of an
event already fully applied) and `MarkProcessed` is now called only
after `Apply*` has actually succeeded. This is safe because
`PostgresProjection.Apply*` was already idempotent per event_id on its
own (it claims `analytics_processed_events` inside the SAME transaction
as its effect) — calling it more than once for the same event_id on a
retry was always a no-op at the database layer; the bug was purely in
the in-memory/repo-level dedupe gate short-circuiting the retry before
`Apply*` ever ran a second time.

Proven end to end with a real testcontainers Kafka
(`analytics_dlq_integration_test.go`,
`TestAnalyticsConsumer_PoisonMessage_GoesToDeadLetterTopicWithoutBlockingPartition`):
a message whose handler is made to always fail for one specific
`event_id` lands on the `.dlq` topic after exactly 3 attempts, with the
raw original JSON payload and the error-context headers intact, and — a
well-formed message for a different event, published on the same
topic — is processed without delay, proving the partition was never
blocked. A second integration test drives only the healthy path against
the same real broker.

### 2. Graceful shutdown hardening, across all three binaries

`cmd/facility/main.go`, `cmd/facility-projector/main.go`, and
`cmd/facility-reports/main.go` each already had a
`signal.Notify`/`signal.NotifyContext`-driven shutdown; each is extended
into the same order order-management's ADR 0025 §8 established:

1. **Flip readiness to not-ready FIRST** — a new `internal/adapters/inbound/http.Readiness`
   type (atomic, thread-safe) backs a new `GET /readyz`, distinct from
   the pre-existing `GET /healthz` (which stays a pure liveness signal,
   never flipped by shutdown). `cmd/facility-projector` has no shared
   `inboundhttp` router to reuse (it only ever served `/healthz` on its
   admin port), so it gained its own small `atomic.Bool`-backed
   `/readyz` handler with the identical semantics.
2. **Stop accepting new HTTP/admin connections and drain in-flight
   requests** — `httpServer.Shutdown(shutdownCtx)` / `srv.Shutdown(ctx)`,
   unchanged in mechanism from before, just sequenced after step 1 now.
3. **Stop the outbox relay (`cmd/facility`) / analytics consumer loop
   (`cmd/facility-projector`) cleanly** — cancel each one's own context
   (no NEW work is picked up after this) and WAIT, bounded by the same
   shutdown deadline, for the goroutine to actually finish in-flight
   work — for the Kafka consumer this means a message already being
   handled runs to completion, including its offset commit (or
   dead-letter publish, §1), before `Run` returns.
4. **Close the Kafka producer/consumer, then the pgx pool LAST** —
   `closeKafka()`/`consumer.Close()` and `closePool()`/`pool.Close()`
   are `defer`red near the TOP of `run()`, so by `defer`'s LIFO order
   `closePool` runs AFTER every relay/consumer goroutine (and the Kafka
   producer/consumer's own `Close`) has already stopped touching it.
   `cmd/facility-reports` is a pure read-only reader with no Kafka
   producer/consumer of its own, so its sequence is simply
   readiness-flip → HTTP drain → pool close LAST.

`Readiness`'s zero value is always ready, mirroring order-management's
`Readiness` exactly — every existing test and any caller that predates
this type behaves exactly as before.

`charts/facility-layout/values.yaml`'s `readinessProbe` now points at
`/readyz` (was `/healthz`) for the main deployment; the projector and
reports deployment templates' inline probes were updated the same way.
`startupProbe`/`livenessProbe` are UNCHANGED, still `/healthz` on every
deployment, for the same reason order-management's ADR 0025 states:
liveness must never be flipped by a graceful drain or Kubernetes would
SIGKILL the pod mid-drain instead of letting it finish. A new
`terminationGracePeriodSeconds: 30` was added (previously unset, a
confirmed gap, same as order-management's finding) across all three
deployments via a single shared `.Values.terminationGracePeriodSeconds`.

### 3. `cenkalti/backoff/v4` promoted from indirect to direct

Was already present transitively (`v4.3.0`, matching order-management's
version exactly); `go mod tidy` promoted it to a direct dependency once
`analytics_consumer.go` imported it.

## Consequences

- The analytics consumer can no longer be permanently wedged by one
  poison message; every other event on the partition keeps flowing. The
  `.dlq` topic is a new operational surface: it needs monitoring/
  alerting (out of scope for this change — the ERROR-level log line is
  the interim signal) and a manual replay tool (also out of scope),
  identical to order-management's own stated consequence.
- The apply-then-claim reordering (§1a) is a behavioural fix, not purely
  additive: `HandleMessage`'s retry-safety property is now correct where
  it was previously silently broken. No external contract changed — the
  observable effect (each event applied exactly once to the projection)
  is identical; only the failure-path behavior (does a still-failing
  event actually get retried and eventually dead-lettered, or silently
  dropped) changed, for the better.
- `GET /readyz` is a new, distinct endpoint per binary that Kubernetes
  now points its `readinessProbe` at; `/healthz` alone is no longer
  sufficient for a pod that participates in a graceful drain, matching
  order-management's stated consequence verbatim.
- No circuit breaker, no new outbound HTTP client, no new sync
  cross-context call was introduced — confirmed out of scope per
  [Scope](#scope-no-circuit-breaker-here) above.

## Alternatives considered

- **Introduce a circuit breaker anyway, wrapping the Postgres pools or
  the Kafka producer "for consistency with order-management":**
  rejected. A circuit breaker over a database connection pool or a
  Kafka producer is not the pattern order-management's ADR 0025
  establishes (which is specifically for **synchronous outbound HTTP
  calls to sibling bounded contexts**) and facility-layout has none of
  those; forcing the pattern in here would be architecture-by-cargo-cult
  rather than a decision grounded in this service's actual dependency
  graph.
- **Claim-then-apply, but wrap `MarkProcessed`+`Apply*` in one
  transaction/UnitOfWork instead of reordering:** considered, since this
  is exactly how order-management's `RepromiseOrder.Execute` uses
  `UnitOfWork` to bracket its own claim+effect atomically. Rejected for
  this specific fix because `PostgresProjection.Apply*` already brackets
  its OWN claim (`analytics_processed_events`) and effect atomically
  inside ITS OWN transaction — introducing a second, outer
  `ConsumedEventsRepo` transaction wrapping both `MarkProcessed` and
  `Apply*` would mean two independent claim tables doing overlapping
  work across two different transactions, which is more moving parts
  than the read-check-then-claim-after-success reordering this ADR
  adopts instead.
