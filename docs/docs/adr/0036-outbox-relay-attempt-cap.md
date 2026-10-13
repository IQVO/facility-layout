---
id: 0036-outbox-relay-attempt-cap
title: "36. Outbox relay attempt cap"
sidebar_label: 36. Outbox relay attempt cap
sidebar_position: 36
description: "ADR 0036 — the outbox relay excludes a row after OUTBOX_MAX_ATTEMPTS failed sends (default 20, 0 = unlimited) and logs it once at ERROR with id, topic, type and key. The row stays in outbox_events until an operator re-arms it, and later rows with the same key overtake it."
---

# 36. Outbox relay attempt cap

## Status

**Accepted** (2026-10-12, ecosystem gap fix D12, widened to every relay in
the fleet). Amends the per-key ordering consequence of
[ADR 0018](./0018-transactional-outbox.md); everything else in ADR 0018 (one
row per event and topic, written in the use case's transaction, drained in
`id` order, `FOR UPDATE SKIP LOCKED`) is unchanged.

## Context

The outbox relay ([ADR 0018](./0018-transactional-outbox.md)) sends rows one
at a time in `id` order and stops a pass at the first failed row, so a later
row never overtakes an earlier one. It had no attempt cap: a failed row was
retried every `OUTBOX_RELAY_INTERVAL`, forever.

That is right for a broker outage, which ends. It is wrong for a **poison
row**, one the broker will never accept: a message over the broker's size
limit, a topic that does not exist and cannot be created, a value the sink
rejects every time. Such a row fails every pass, every pass stops at it, and
every row behind it, for every site and both topics
(`warehouse.facility.events` and `warehouse.facility.analytics`), waits
forever. Nothing pages: the relay logs `outbox relay pass failed` once per
pass and the process stays Ready. Downstream contexts (inventory-storage,
wes-work-planning, network-inventory-planning, the analytics projector) stop
hearing about any layout change.

The fleet already settled this in its inventory reference implementation:
an option on the relay that excludes a row after N failed sends, with an
operator re-arm. This ADR adopts the same convention here.

## Decision

1. **The relay takes an attempt cap.** `postgres.WithMaxAttempts(n)`: with
   `n > 0`, a row whose `attempts` has reached `n` is excluded from the
   relay's claim (`AND attempts < $max`). The relay's own zero value stays
   `0` (unlimited), so the option is always an explicit choice.
2. **Every composition root that builds a relay passes it.** `cmd/facility`
   (the only binary here that runs a relay; `cmd/mcp` writes no outbox rows
   and relays nothing) reads `OUTBOX_MAX_ATTEMPTS` and passes it as
   `postgres.WithMaxAttempts`. Default `20`
   (`postgres.DefaultOutboxMaxAttempts`); `0` means unlimited, the old
   behaviour. An invalid or negative value logs a WARN and uses the default,
   so a typo cannot silently remove the cap. The Helm chart exposes it as
   `config.outboxMaxAttempts`, rendered into the api ConfigMap.
3. **The excluded row is kept, not moved.** It stays in `outbox_events` with
   `published_at IS NULL`, its `attempts` and its `last_error`, for
   inspection. The housekeeping sweeper
   ([ADR 0026](./0026-housekeeping-sweeper.md)) deletes only published rows,
   so it never removes an excluded row. There is no DLQ topic: a message the
   broker rejects would most likely be rejected on a DLQ topic of the same
   broker too, and the table already is the inspectable store.
4. **The failure that reaches the cap is logged once at ERROR**, after the
   attempt is committed: message `outbox relay: row reached the attempt cap
   and is excluded from future claims; ...` with `outbox_id`, `topic`,
   `event_type`, `key`, `attempts`, `max_attempts` and `error`. Failures
   below the cap keep the existing `outbox relay pass failed` log.
5. **Re-arming is an operator action.** After fixing the cause,
   `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;` makes the row
   claimable on the next pass (runbook, "Outbox relay").
6. **Sensor.** The architecture fitness test `TestOutboxRelaysPassAttemptCap`
   fails when a non-test file under `cmd/` calls `NewOutboxRelay` without a
   `WithMaxAttempts(...)` argument; `TestOutboxRelayCapSensorFailsOnBadFixtures`
   proves it can fail.

## Consequences

- **Per-key ordering is no longer absolute.** Once a row is excluded, later
  rows overtake it, **including rows with the same key** (the same site code
  or aggregate identity, [ADR 0032](./0032-aggregate-partition-keys-for-geometry-events.md)):
  a consumer can see a later event for an aggregate whose earlier event never
  arrived, or receive the earlier event late once it is re-armed. This is the
  deliberate trade: the cap exists so one poison row cannot stop the whole
  relay, for every key, forever. Consumers are already idempotent on the
  CloudEvents `id` and must already tolerate redelivery
  ([ADR 0018](./0018-transactional-outbox.md)); a re-armed row is a late
  delivery of the same `id`.
- Ordering among rows that are **not** excluded is unchanged: within a pass
  the relay still stops at the first failure, so a transient failure below
  the cap never lets a later row overtake.
- With the defaults (1 s interval, cap 20) a row that fails every send is
  excluded after roughly 20 seconds of continuous failure. A broker outage
  longer than that also excludes the rows at the head of the queue, one per
  pass, as each reaches the cap; each is logged at ERROR and must be
  re-armed. Raise `OUTBOX_MAX_ATTEMPTS` (or set `0`) where long broker
  outages are expected and a stalled relay is preferable to manual re-arming.
- Excluded rows are listed by
  `SELECT id, topic, event_type, key, attempts, last_error FROM outbox_events WHERE published_at IS NULL AND attempts >= 20;`
  (replace `20` with the configured cap).

## Verification

- `go test -tags=integration ./internal/adapters/outbound/postgres/ -run TestOutboxRelay -count=1`:
  `TestOutboxRelay_MaxAttempts_PoisonRowExcludedThenGoodRowPublishes`,
  `TestOutboxRelay_MaxAttempts_ReArmedRowIsClaimedAgain`,
  `TestOutboxRelay_NoCap_PoisonRowBlocksLaterRows`.
- `go test ./cmd/facility/ -run 'TestOutboxMaxAttempts|TestDefaultOutboxMaxAttemptsIsTwenty'`.
- `go test ./internal/architecture/ -run OutboxRelay`.
- `bash charts/facility-layout/tests/render_test.sh` (default `20`,
  `config.outboxMaxAttempts` rendered as `OUTBOX_MAX_ATTEMPTS`).
