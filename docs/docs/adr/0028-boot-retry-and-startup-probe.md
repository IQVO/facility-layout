---
id: 0028-boot-retry-and-startup-probe
title: 28. Boot retry for Istio sidecar resets and a matching startupProbe
sidebar_label: 28. Boot retry & startupProbe
sidebar_position: 28
description: "Every injected pod's first outbound TCP dial is reset ~10s after start (Istio native sidecars). Boot-time steps (migrations, pool ping, Kafka topic checks) retry under a bounded budget, and the chart pairs that with a startupProbe so CrashLoopBackOff is not the retry mechanism."
---

# 28. Boot retry for Istio sidecar resets and a matching startupProbe

## Status

**Accepted.** Recorded retroactively 2026-10 (the code shipped as
`internal/adapters/outbound/bootretry` plus chart probe wiring without
its own ADR).

## Context

In this fleet every pod with an injected service-mesh sidecar (Istio
native sidecars; `holdApplicationUntilProxyStarts` is a no-op for them)
has its first outbound TCP connection reset roughly ten seconds after
the application starts. Any boot step that dials out — running
golang-migrations, pinging the pgxpool, checking Kafka topic readiness —
therefore has a high-probability transient failure on a fresh pod. A
single boot attempt turns that known condition into CrashLoopBackOff,
which then relies on Kubernetes restart backoff (30s, 60s, 120s…) as the
retry mechanism — slow, noisy, and visible as alarms.

## Decision

- Boot-time outbound steps run under `internal/adapters/outbound/bootretry`:
  a bounded exponential-backoff retry (with jitter) around each step —
  migrations, database ping, Kafka topic-not-ready checks. The budget is
  generous relative to the ~10s sidecar window; after the budget is
  exhausted the process still refuses to boot and reports the real
  underlying error, so the fail-closed rule is not weakened.
- The Helm chart pairs this with a `startupProbe` on the affected
  workloads so Kubernetes does not count boot-time retries as liveness
  failures: the probe gates liveness/readiness until the container's
  own boot path finishes.
- Runtime (post-boot) failures are NOT retried this way — request-path
  failures keep their per-request semantics; only the boot sequence is
  special, because only it runs exactly once per pod life.

## Consequences

- A pod booting into a healthy cluster starts cleanly despite the
  sidecar reset; a pod booting into a genuinely broken dependency fails
  after the budget with the true error, not a confusing connection-reset
  loop.
- Boot takes longer in the failure case (bounded by the retry budget)
  than a crash-restart loop would — that is the trade: deterministic
  slowness over alarm-noise.
- The retry wrapper is one small package with tests; it must not grow
  into a general resilience framework. Runtime retries belong to the
  callers that own those semantics (the consumer's DLQ path, the
  outbox relay).

## Related

- ADR-0020 — consumer resilience (where runtime retries DO live).
- ADR-0023 — the direct-Postgres migrations connection boot retry wraps.
