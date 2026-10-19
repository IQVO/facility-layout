---
id: 0039-postgres-and-kafka-are-the-only-runtime
title: "39. Postgres and Kafka are the only runtime"
sidebar_label: 39. Postgres and Kafka only
sidebar_position: 39
description: "ADR 0039 — every cmd binary runs on real Postgres and real Kafka. No in-memory repositories, no log publisher, no direct fan-out, no optional relay. Mandatory configuration is a boot error naming the variable; /readyz checks Postgres and a Kafka broker; sensors, the contract job and the chart keep it true."
---

# 39. Postgres and Kafka are the only runtime

## Status

**Accepted** (2026-10-19). Supersedes the in-memory fallback, the log
publisher, the direct (non-outbox) Kafka fan-out and the "relay only with
`EVENT_PUBLISHER=kafka`" rule that
[ADR 0009](./0009-kafka-integration-publisher.md),
[ADR 0018](./0018-transactional-outbox.md) and
[ADR 0019](./0019-idempotency-key-middleware.md) allowed. Fleet decision of
2026-10-17, "everything must use real database and broker"; facility-layout
follows product-master (their ADR 0010) and workforce-management (their ADR
0038). No migration; REST, MCP and the CloudEvents envelope
([ADR 0024](./0024-cloudevents-mandatory-envelope.md)) are unchanged. One
contract detail follows from it: `Idempotency-Key` is now `required: true` in
`apis/openapi.yaml`, because the Postgres-backed check can no longer be absent.

## Context

The binaries could start in states that cannot serve the cluster, and did so
silently:

- `cmd/facility` and `cmd/mcp` without `DATABASE_URL` logged
  `database url not configured; using in-memory adapters` and served from
  their own empty in-memory repositories: data vanished on restart, the MCP
  server saw none of the API's data, and the Idempotency-Key check was
  skipped.
- `EVENT_PUBLISHER` defaulted to the log publisher (and the chart default was
  `config.eventPublisher: "log"`): no outbox row was written and nothing
  reached `warehouse.facility.events` or `warehouse.facility.analytics`, while
  `/readyz` said ready. With Kafka but without a database the service
  published directly, outside any transaction. `KAFKA_BROKERS` defaulted to
  `localhost:9092`.
- The projector hard-coded its consumer group (`facility-analytics`).
- `/readyz` was only a drain gate: ready while Postgres or Kafka was
  unreachable.
- The Schemathesis `contract` job booted the service in memory, so it tested a
  mode that is not deployed.

A fallback that works makes a mis-set environment look like a healthy
deployment.

## Decision

1. **One runtime.** `cmd/facility` and `cmd/mcp` run on real Postgres;
   `cmd/facility` and `cmd/facility-projector` run on real Kafka; the
   projector and `cmd/facility-reports` run on the analytical Postgres. There
   is no in-memory mode, no log sink and no direct publisher: events are
   written through the transactional outbox in the use case's transaction, and
   the relay always runs, fanning each event out to the integration topic and
   the analytics topic.
2. **Missing configuration is a boot error that names the variable**, raised
   before telemetry, any connection and the listener, every problem reported at
   once: `DATABASE_URL is required: this context runs on real Postgres`,
   `KAFKA_BROKERS is required: this context runs on real Kafka`,
   `ANALYTICS_DATABASE_URL`, `KAFKA_BROKERS` and `ANALYTICS_CONSUMER_GROUP`
   for the projector (no default in the binary). `EVENT_PUBLISHER` stays only
   because the chart and warehouse-infra set it: unset and `kafka` are
   accepted, `log` and anything else stop the boot.
3. **The composition roots build real adapters only.** The `memory` adapters
   stay in `internal/adapters/outbound/memory` as test doubles for unit and
   BDD tests and are never imported by a binary. The wall clock and id source
   the binaries need moved to `internal/adapters/outbound/system`.
4. **`/readyz` tells the truth.** `cmd/facility` is `200` only while the drain
   gate is open AND Postgres answers a ping AND a Kafka broker answers; the
   projector's admin `/readyz` follows its two dependencies and
   `cmd/facility-reports` pings its analytical Postgres. The verdict is cached
   a few seconds, bounded in time, and `/healthz` stays a pure liveness check.
5. **Readiness checks the broker, not the topics.** The broker auto-creates
   topics on the first write (fleet convention) and this service never creates
   topics through an admin API. A not-ready pod receives no traffic and so
   could never make the first write: requiring the topics would deadlock a
   cold cluster.
6. **Sensors keep it true.** `TestRuntimeBinaryHasNoInMemoryAdapters`
   (`internal/architecture`, with `TestRuntimeBinarySensorBites`) fails when a
   non-test file under `cmd/**` imports `internal/adapters/outbound/memory`,
   and `TestNoGoFilesAtRepoRoot` keeps the repository root free of Go files:
   the four godog files moved from the root to `features/`.
7. **The deployment mirrors it.** The chart fails the render, naming the value,
   without `database.url`/`database.existingSecret` or `kafka.brokers`, with
   `config.eventPublisher` other than `kafka`, with `kafka.enabled=false`, with
   an `EVENT_PUBLISHER` or `*_MODE` entry in `extraEnv`, with a grace period
   under 20 s, and with analytics enabled but no analytical DSN source or
   `analytics.projector.consumerGroup` (default `facility-analytics`, the group
   the projector has always used, so its committed offsets survive the
   rollout). `KAFKA_BROKERS` is always rendered. `kafka.enabled` is still
   accepted (warehouse-infra sets it to `true`) but is no longer a switch.
   `charts/facility-layout/tests/warehouse-infra-values.yaml` mirrors what
   warehouse-infra sets and `render_test.sh` renders it, so infra needs no
   change.
8. **The contract job runs the real service.** `docker-compose.yml` now carries
   `postgres:16` and `confluentinc/confluent-local:7.6.1`; the CI `contract`
   job uses the same two images as service containers and
   `scripts/contract-test.sh` requires `DATABASE_URL` and `KAFKA_BROKERS`.
   The old in-memory boot seeded nothing, and the new boot starts on an empty
   database equally. A Schemathesis hook (`scripts/contract_hooks.py`) gives
   every request a fresh `Idempotency-Key`, since a reused key with a
   different body is by design a `422`.
9. **A NUL is a client error.** The first CI run of the real-service contract
   job found what the in-memory boot hid: a fuzzed JSON body with U+0000 in a
   string reached a Postgres text parameter, which aborted the idempotency
   transaction and surfaced as `500 internal-error` (SQLSTATE 25P02).
   `rejectNUL` (`internal/adapters/inbound/http/nul.go`) now answers a NUL in a
   body with `400 malformed-request-body` before the Idempotency-Key
   middleware records anything, and a NUL in the path with `404`. The
   contract script passes `--generation-allow-x00 false`, because OpenAPI 3.0
   cannot express "no U+0000" and the rejection would otherwise read as "API
   rejected schema-compliant request"; the behaviour is pinned by `TestRejectNUL`
   and `TestNUL_IsAClientErrorAndDoesNotConsumeTheKey` (real Postgres).

## Consequences

- A mis-set environment fails at boot with a message that names the missing
  variable; it no longer degrades into a pod that is up, empty and silent.
- Local `go run ./cmd/facility` needs a Postgres and a Kafka broker:
  `docker compose up -d --wait` provides both. Fast feedback stays in unit
  and BDD tests over the memory doubles.
- A broker outage takes the api pods out of the Service (readiness `503`)
  where the probe is `/readyz`; liveness is unaffected, so nothing restarts.
  Writes that were accepted stay in the transactional outbox and are relayed
  when the broker returns
  ([ADR 0018](./0018-transactional-outbox.md),
  [ADR 0036](./0036-outbox-relay-attempt-cap.md)).
- Tests: `TestRunOnRealPostgresAndKafka` (`cmd/facility/e2e_integration_test.go`,
  testcontainers) boots the real `run()` with nothing but the deployment's
  environment, drives REST only, asserts the Postgres rows, the CloudEvents
  envelope byte for byte on both output topics, `Idempotency-Key` replay, an
  in-process restart (no loss, no duplicate) and `/readyz` following the
  dependencies. Every boot error is a table test
  (`cmd/facility/bootconfig_test.go`, `cmd/facility-projector/config_test.go`,
  `cmd/mcp/migrations_test.go`).

## Out of scope

facility-layout reads no `*_MODE` variable (it has no outbound REST sync
edge), so nothing of that kind is left behind here; the `*_MODE` switches that
exist in sibling contexts (for example `LOCATION_ROLE_MODE` and
`TRAVEL_DISTANCE_MODE` in the consumers of this service's REST API) are theirs
to retire. warehouse-infra's readiness probe still points at `/healthz`;
switching it to `/readyz` is an infra follow-up.
