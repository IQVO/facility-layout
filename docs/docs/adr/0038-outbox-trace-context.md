---
id: 0038-outbox-trace-context
title: "38. The W3C trace context crosses the outbox in outbox_events.headers"
sidebar_label: 38. Trace context across the outbox
sidebar_position: 38
description: "ADR 0038 — fleet decision D27 / gap 15d, after order-management ADR 0041: each outbox row stores the W3C trace context of the transaction that wrote it in a new nullable outbox_events.headers column (migration 0010), the relay sends it as Kafka headers next to the CloudEvents content-type and never injects its own context, and NULL rows relay unchanged. Pinned by testcontainers Postgres + Kafka tests."
---

# 38. The W3C trace context crosses the outbox in `outbox_events.headers`

## Status

**Accepted** (2026-10-13, final-gaps wave, tracker gap 15d; fleet decision
D27). Copies order-management ADR 0041, the fleet D27 reference. Builds on
[ADR 0018](./0018-transactional-outbox.md) (the outbox) and
[ADR 0009](./0009-kafka-integration-publisher.md) (W3C trace context in Kafka
headers on the direct publish path).

## Context

[ADR 0009](./0009-kafka-integration-publisher.md) injects the W3C trace
context into Kafka headers. That holds on the direct publish path
(`Publisher.Send`), but production writes through the transactional outbox:

- `outbox_events` (migration `0005`) had no column for headers, so
  `postgres.OutboxPublisher.Publish` dropped the use case's `ctx` trace
  context when it wrote the row;
- `kafka.RelaySink.Send` injected the relay pass's own `ctx`, which carries
  no span (the relay is a background loop), so relayed messages had no
  `traceparent` at all.

A trace therefore stopped at the HTTP request and never reached a consumer.
The observability page said so.

order-management ADR 0041 records the fleet answer (D27). If the outbox
already stores the propagator output, add the proof tests. If it does not,
add an additive nullable column, fill it from `ctx` in the outbox publisher,
send it from the relay, and leave rows without it unchanged.

## Decision

1. **Store.** Migration `0010_outbox_trace_headers` adds
   `outbox_events.headers JSONB` (nullable, additive, no backfill). It
   holds the trace-context headers only, as `[{"key":"traceparent","value":"00-…"}]`,
   the same shape order-management stores. The CloudEvents `content-type`
   is not stored: the relay adds it to every message, as before.
2. **Capture point.** `OutboxPublisher.Publish` captures
   `kafka.TraceHeaders(ctx)` once per domain event. That is whatever the global
   propagator injects for the use case's `ctx`: the otelchi server span of the
   request, or the span of the consumer that triggered the use case. The
   capture happens inside the same transaction as the aggregate write, and
   every row of that event (integration and analytics topics) stores it. With
   no active span the column is NULL.
3. **Relay.** `OutboxRelay` reads the column back into `Encoded.Headers`.
   `RelaySink.Send` writes the CloudEvents `content-type` followed by those
   stored headers. It no longer injects its own `ctx`: the relay has no span,
   and the stored context is the only correct parent.
4. **Rows without trace context relay unchanged.** A NULL row (no span at
   write time, or any row written before migration `0010`) relays with
   `content-type` only. It never carries an all-zero or foreign
   `traceparent`.
5. **Consumers.** This repo's only consumer, the analytics projector, starts
   no span, so it needs no `Extract`. A future consumer that starts a span
   calls `otel.GetTextMapPropagator().Extract` on the message headers first,
   as ADR 0041 says.
6. **Proof.** These testcontainers tests in
   `internal/adapters/outbound/postgres/outbox_trace_integration_test.go`
   (Postgres and Kafka) pin the behaviour:
   - `TestOutboxTrace_TraceparentCrossesTheOutboxToKafka`: `RegisterSite`
     runs in a request span. Both outbox rows store that span's
     `traceparent`. A relay pass on a context without the span sends the
     rows to a real broker, with the topics created first. Extracting the
     consumed message's headers yields the request's trace id and span id as
     a remote parent.
   - `TestOutboxTrace_RowWithoutTraceHeadersRelaysUnchanged`: a NULL-headers
     row reaches Kafka with the same key and value and `content-type` only.
   - `TestOutboxTrace_NoActiveSpanStoresNoHeaders`: an untraced use case
     writes NULL.

   Unit tests in `internal/adapters/outbound/kafka/relay_sink_trace_test.go`
   pin that `TraceHeaders` captures only the trace context, that
   `RelaySink.Send` sends the stored context rather than the relay's, and
   that a row with no stored context gets `content-type` only.

## Consequences

- A trace in Tempo or Jaeger runs from the HTTP request through the outbox to
  the downstream consumer's span (network-inventory-planning,
  inventory-storage, warehouse-planning), even though the Kafka write
  happens later in the relay goroutine. The outbox path still has no
  publish span.
- A row keeps its original parent even when it is sent much later (after a
  broker outage, or an `OUTBOX_MAX_ATTEMPTS` re-arm, ADR 0036). The consumer
  span then starts long after its parent ended, which is normal for
  asynchronous messaging.
- Rows written before the upgrade carry no trace context, which is the same
  behaviour as today. Nothing is backfilled.
- The column adds a few hundred bytes per row. Published rows are swept after
  `OUTBOX_RETENTION` ([ADR 0026](./0026-housekeeping-sweeper.md)).
- There is no new configuration, and the direct publish path
  (`Publisher.Send`) is unchanged.

## Alternatives considered

- **Name the column `trace_headers`, as the D27 addendum words it.** Rejected
  in favour of `headers`, which is what the task and order-management ADR
  0041 use. The shape is identical across the fleet, so a reader or an
  operator query works the same way in every repo.
- **Keep injecting the relay's context as a fallback.** Rejected. The relay
  pass has no span, and if one were added it would become the consumer's
  parent and cut the link to the request (the same reasoning as ADR 0041).
- **Start a publish span in the relay.** Rejected for the same reason.
