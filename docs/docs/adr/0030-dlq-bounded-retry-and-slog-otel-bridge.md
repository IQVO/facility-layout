---
id: 0030-dlq-bounded-retry-and-slog-otel-bridge
title: 30. DLQ topic-not-ready bounded retry and the slog-to-OTel bridge
sidebar_label: 30. DLQ retry & slog→OTel bridge
sidebar_position: 30
description: "The analytics consumer's DLQ writer retries a not-ready DLQ topic under a bounded budget instead of dropping or crashing, and log records bridge into OTel via the otel slog handler so logs carry the same trace ids as spans."
---

# 30. DLQ topic-not-ready bounded retry and the slog-to-OTel bridge

## Status

**Accepted.** Records two operational details of the analytics pipeline
that ADR-0010/ADR-0020 shipped without their own record; written
retroactively 2026-10.

## Context

Two behaviours of the analytics consumer
(`internal/adapters/inbound/kafka/analytics_consumer.go`) had no ADR
home:

1. When a message exhausts its retries and is dead-lettered, the DLQ
   topic may not exist yet (auto-creation racing the first DLQ publish,
   or the topic simply not provisioned). The original DLQ writer's
   publish could fail after the source offset was about to be committed
   — the poison message would be lost, silently.
2. The process logs via `log/slog`, but its traces go to the OTel
   Collector; without a bridge, log lines and spans cannot be correlated
   by trace id in the Collector.

## Decision

- **Bounded retry around DLQ publishes.** A DLQ write to a not-ready
  topic is retried under a small bounded budget (the consumer's
  existing backoff discipline). If the budget is exhausted the source
  offset is NOT committed — the batch is re-delivered, so the poison
  message is never silently dropped; at worst it pauses the partition,
  which is visible. The DLQ writer itself is a first-class
  `NewDeadLetterWriter` construction with the fleet's durability
  config (RequireAll acks, 10ms batch timeout, Hash balancer) — see
  ADR-0021 and the DLQ-durability fix in this change set.
- **slog→OTel bridge.** The telemetry package
  (`internal/adapters/outbound/telemetry`) installs an OTel slog
  handler as the process logger, so every log record is exported with
  the active span's trace/span ids. Console output remains plain slog
  text for local development.

## Consequences

- A broken DLQ topic surfaces as a stuck consumer group (loud, alertable)
  instead of silent message loss — the deliberate trade.
- Log volume through the Collector grows; the bridge keeps attributes
  minimal (level, msg, trace ids) and lets the Collector's own sampling
  decide.
- Both behaviours are covered by unit tests pinning the retry budget
  and the writer configuration.

## Related

- ADR-0010 — the analytics data product this consumer serves.
- ADR-0020 — DLQ and graceful-shutdown hardening (the frame this fits).
- ADR-0021 — writer balancer/durability conventions.
