---
id: 0041-site-capability-heartbeat
title: "41. Site capability heartbeat keeps SiteCapabilityChanged facts fresh"
sidebar_label: 41. Site capability heartbeat
sidebar_position: 41
description: "ADR 0041 — a periodic in-process heartbeat re-publishes the current SiteCapabilityChanged of every active site with a declared transfer capability through the outbox, with a fresh time and capability_revision + 1, so network-inventory-planning's freshness window holds; PUT keeps its ADR 0035 no-op semantics."
---

# 41. Site capability heartbeat keeps SiteCapabilityChanged facts fresh

## Status

**Accepted** (2026-10-25, owner decision after a live finding). Extends
[ADR 0035](./0035-site-transfer-capability-command.md) (the revision now also
advances on a heartbeat) and reuses the in-process loop shape of
[ADR 0026](./0026-housekeeping-sweeper.md), the outbox of
[ADR 0018](./0018-transactional-outbox.md) and the version guard of
[ADR 0025](./0025-optimistic-concurrency-version-column.md).

## Context

network-inventory-planning (NIP) approves an inter-warehouse transfer only
from a planning snapshot whose every site-capability fact is fresh: a fact
whose `as_of` is more than **10 minutes** old fails the snapshot as stale
(`planning.snapshot`, `maxStaleness`). Its `as_of` is the CloudEvents `time`
of the last applied `SiteCapabilityChanged`.

facility-layout raises `SiteCapabilityChanged` only when a capability
**changes** (ADR 0035): a `PUT` of the value a site already holds is a no-op
and publishes nothing. In a steady fleet nobody changes capabilities, so every
fact ages out ten minutes after the last change and no transfer can ever be
approved. This was found on the live cluster.

### What NIP does with a re-emitted fact

Read from NIP `origin/develop`
(`internal/adapters/inbound/kafka/consumers.go`,
`internal/adapters/outbound/postgres/planning_repositories.go`):

1. `SiteCapabilityConsumer.HandleMessage` claims the CloudEvents `id` in
   `processed_events` (consumer `site-capability-consumer`); an id seen before
   is skipped. A new id is claimed.
2. `SiteCapabilityRepo.Upsert` then writes the row (values **and** `as_of`)
   `ON CONFLICT (site_id) DO UPDATE ... WHERE EXCLUDED.capability_revision >
   site_capability.capability_revision`. The supersede rule is a **strictly
   greater `capability_revision`**; the event `time` plays no part in it.

So an event with the same content, a new id and a newer `time`, but the
**same** `capability_revision`, is claimed and then ignored: `as_of` is not
refreshed. Re-emitting the unchanged revision cannot work. The fix has to give
the consumer a revision it will accept, and it has to be made on this side:
the contract is the Published Language of this context (ADR 0013), NIP is a
Conformist downstream and is not changed.

## Decision

1. **A periodic heartbeat re-publishes the current capability.** Every
   `SITE_CAPABILITY_HEARTBEAT_INTERVAL` (chart
   `config.siteCapabilityHeartbeatInterval`, **default `5m`**, half of NIP's
   window) a background loop in the `facility` process publishes a
   `SiteCapabilityChanged` for every **active** site with a **declared**
   capability (`capability_revision > 0`; a site never configured has nothing
   to assert, a decommissioned site's capability is frozen and is allowed to
   age out, which is the fail-closed outcome). It is the same event type, the
   same key and subject (the site code), the same payload fields and values,
   with a **new CloudEvents `id`** and a **fresh `time`** (the consumers'
   `as_of` = the moment of emission).
2. **The heartbeat advances `capability_revision` by exactly one.** This is the
   one change to the contract: the revision moves on every event of a site,
   change or heartbeat, so NIP's strictly-greater rule accepts it and its
   `as_of` moves. The values never change on a heartbeat; consumers that care
   whether a capability changed compare the values, not the revision step.
   The AsyncAPI message description and the `capability_revision` field
   description say so. No payload field is added (NIP's decoder is
   hand-mirrored; the heartbeat is indistinguishable on the wire by design).
3. **ADR 0035 semantics are untouched.** `ChangeTransferCapability` still
   treats an identical snapshot as a no-op (no revision, no event); a real
   change continues from whatever revision the heartbeats reached. The
   heartbeat is a separate aggregate behaviour,
   `Site.HeartbeatTransferCapability(now, interval)`, never reachable from the
   PUT.
4. **Through the outbox, in the site's unit of work.** The use case
   `PublishSiteCapabilityHeartbeats` loads each due site, advances it, and in
   one `atomically` unit of work saves the site (revision, and the new
   nullable `sites.capability_heartbeat_at`, migration `0012`) and publishes
   the event, so the integration and analytics outbox rows exist exactly when
   the revision moved. The inbound timer adapter
   (`internal/adapters/inbound/heartbeat`) runs one pass immediately at start
   and then every `interval/5`.
5. **Replicas do not flood.** Every replica runs the loop, as the sweeper does
   (ADR 0026), and no advisory lock is needed. A site is *due* only when
   `capability_heartbeat_at` is NULL or at least one interval old, a column
   shared by all replicas, so a site is re-asserted once per interval across
   the fleet. The residual race (two replicas load the same due site) is
   settled by the site's version guard (ADR 0025): the loser's `Save` returns
   `ErrConcurrentModification`, its outbox row rolls back with its
   transaction, and the pass counts it as skipped. The same guard serialises a
   heartbeat against a concurrent `PUT`. (A Postgres advisory lock would be
   session-scoped and meaningless behind the PgBouncer transaction pool,
   ADR 0023; the version guard works there.)
6. **Cadence and freshness.** The loop wakes five times per interval. A site's
   worst gap between two heartbeats is therefore `interval + interval/5`
   (6 minutes at the default), so the 10 minute window holds with the default
   and with any interval of at most half the window. The first pass after a
   deploy refreshes every declared site at once (the column starts NULL).
7. **Configuration follows the housekeeping convention.** The variable goes
   through `durationEnvRaw`: unset, empty or malformed is the 5m default, and
   an explicit `0`/`0s` disables the loop (logged once at boot). The chart
   carries a default, so **no warehouse-infra value is required**.

### Why a heartbeat and not a relaxed consumer window

- The freshness window is NIP's safety rule: a transfer must never be approved
  from a capability fact that may be hours old because this producer or the
  broker is down. A staleness check that stops expiring is no check. The
  heartbeat keeps the check meaningful: a fact is fresh **because this service
  said so recently**, and if the producer, the relay or Kafka stops, facts
  still age out in at most the window and transfers fail closed again.
- Widening the window (or exempting capability from it) is a change in
  another context's invariant, made to cover a producer quirk, and it would
  trade the live failure for a silent one.
- Making NIP accept an equal revision with a newer time changes its Conformist
  read model for every producer; the owner chose the change on the Open Host
  Service side, which this ADR is.

## Consequences

**Positive**

- A steady cluster keeps every declared site's fact fresh; transfers can be
  approved, and still fail closed if this service stops publishing.
- No consumer change, no new event type, no payload change, no infra value.
- Replica-safe and PgBouncer-safe by reusing the row version and the outbox.

**Negative / accepted**

- `capability_revision` is no longer a count of capability changes: it grows by
  one per site per interval (about 288 a day at 5m), well within `int64`. The
  contract (AsyncAPI) says so; GET and MCP still return the current revision.
- Extra outbox rows: one per site per interval on the integration topic and
  one on the analytics topic (the projector ignores the event). Published rows
  are swept by ADR 0026.
- A heartbeat and a PUT of the same site can collide: one of them gets a
  `409 concurrent-modification` (PUT, as ADR 0035 already documents) or is
  skipped until the next tick (heartbeat).
- The consumer cannot tell a heartbeat from a change on the wire.

## Verification

- Domain: `internal/domain/site/capability_heartbeat_test.go` (due boundary at
  exactly one interval, one nanosecond either side; zero/negative interval;
  revision 0; decommissioned; PUT stays a no-op after a heartbeat).
- Use case: `publish_site_capability_heartbeats_test.go` (current values, fresh
  time, revision + 1, once per interval at the boundary, lost version race is
  a skip, one failing site does not stop the rest).
- Loop: `internal/adapters/inbound/heartbeat/heartbeat_test.go`.
- Config: `cmd/facility/capability_heartbeat_config_test.go`; chart:
  `charts/facility-layout/tests/render_test.sh`.
- Integration (testcontainers Postgres,
  `site_capability_heartbeat_integration_test.go`): the heartbeat outbox row
  decodes through the hand-mirrored NIP DTO and a hand-mirrored copy of NIP's
  supersede rule moves `as_of`, while a same-revision copy does not; six
  concurrent replica passes leave one heartbeat per site; the SQL due
  boundary; the real loop republishes repeatedly.
- BDD: `features/site_capability_heartbeat.feature`.
- Contract: `apis/asyncapi.yaml` (`siteCapabilityChanged`).

## Related

- [ADR 0035](./0035-site-transfer-capability-command.md),
  [ADR 0026](./0026-housekeeping-sweeper.md),
  [ADR 0018](./0018-transactional-outbox.md),
  [ADR 0025](./0025-optimistic-concurrency-version-column.md),
  [ADR 0023](./0023-migrations-direct-postgres-connection.md).
- [Domain events](../ddd/domain-events.md),
  [Consuming this service](../ecosystem/consuming-this-service.md),
  [Configuration](../operations/configuration.md).
