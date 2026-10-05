---
id: 0013-first-published-language-consumer
title: 13. inventory-storage is the first real consumer of the Published Language
sidebar_label: 13. First real consumer
sidebar_position: 13
description: "ADR 0009's warehouse.facility.events topic now has a live downstream consumer — inventory-storage's location-classification cache. This records what that makes binding: the event payload fields it depends on, why they are now a compatibility contract, and the chart bug that had silently prevented any of it from being published."
---

# 13. inventory-storage is the first real consumer of the Published Language

## Status

**Accepted.** In effect in the `warehouse` kind cluster as of 2026-09-06.

## Context

ADR 0009 made this service publish its full Published Language to
`warehouse.facility.events`, and described downstream services as
"Conformists" to it. Until now that was **aspirational**: nothing in the
estate consumed the topic. The events were, in practice, write-only.

Two things changed that:

1. **inventory-storage now maintains a local read model of location
   classifications** fed by this topic (its ADR 0013), replacing the
   synchronous `GET /locations/{code}/classification` call it previously
   made on every stow. That HTTP endpoint (ADR 0008) is retained as its
   rollback, not removed.

2. **A chart bug meant this service was never actually publishing.**
   `values.yaml` documented `config.eventPublisher: "log" | "kafka"` and a
   `kafka.enabled`/`kafka.brokers` block, but the main deployment template
   rendered **neither** as an environment variable. Setting them did
   nothing: the container only ever saw `HTTP_ADDR`, `MIGRATIONS_PATH`,
   `DATABASE_URL` and the OTel vars, so `cmd/facility` always fell back to
   the Postgres outbox and the topic was never even created. Helm reported
   a successful upgrade throughout. This is worth recording as a lesson in
   its own right: **a successful `helm upgrade` is not evidence that a
   value reached the container.** Fixed separately; the projector template
   and every sibling service's chart had always rendered these correctly.

## Decision

Acknowledge inventory-storage as a **live downstream Conformist**, and
treat the following as a real compatibility contract rather than an
internal detail:

| Event | Fields the consumer depends on |
|---|---|
| `ZoneRegistered` | `zoneId`, `temperatureClass`, `hazmat` |
| `LocationSlotRegistered` | `locationCode`, `zoneId` |
| `LocationSlotDecommissioned` | `locationCode` |

Also binding, though less obvious:

- **The CloudEvents type suffix.** The consumer matches on the trailing
  event name (`...zone.ZoneRegistered` → `ZoneRegistered`), so renaming an
  event is breaking even if the namespace is untouched. *(Updated by
  [ADR-0024](./0024-cloudevents-mandatory-envelope.md): consumers now
  dispatch on the FULL CloudEvents `type` string, so the whole type —
  namespace and entity included — is the binding contract.)*
- **`LocationCode`'s segment structure.** The consumer derives a zone
  identity from the first three hyphen-separated segments when `zoneId` is
  absent, mirroring `LocationCode.ZoneID()`. Changing the code's shape
  changes that derivation.
- **Aggregate-scoped ordering only.** Slots and zones are separate
  aggregates published under different partition keys, so there is no
  ordering guarantee between them. The consumer copes by joining lazily —
  but this service must not start *assuming* cross-aggregate order either.

Every other event on the topic (`SiteRegistered`, `AisleRegistered`,
`LocationTypeRegistered`, `PlacementRuleDefined`,
`FacilityLayoutImported`) is currently ignored by this consumer. They stay
published: this is an Open Host Service, and the point is that the full
language is available whether or not today's single consumer uses it.

## Consequences

### Positive

- The Open Host Service claim is now demonstrably true, and verified
  end-to-end: with this service **scaled to zero replicas**,
  inventory-storage still classified stows correctly from its cache
  (hazmat SKU rejected from an ambient zone, accepted into a hazmat zone).
  A downstream context no longer depends on this one's availability for
  steady-state operation.
- Changes propagate to a running consumer within seconds, with no restart.
- A regression test exists at the estate level:
  `e2e-tests/features/facility_layout_propagation.feature` asserts the
  zone attributes actually reach inventory-storage's placement rules over
  Kafka — something neither repo's own tests can cover, since one owns the
  publisher and the other the consumer.

### Negative / accepted

- **Event payload changes are now breaking changes** for a named
  downstream. The fields above cannot be renamed or dropped without
  coordinating with inventory-storage.
- **A fresh consumer replays from the earliest offset**, so this topic
  must retain the full layout history for a new consumer's cache to be
  complete. Events that predate the publisher being switched on live only
  in this service's Postgres outbox and will never reach the topic — on a
  cluster with pre-existing layout data, the layout must be re-registered
  or re-imported after enabling the publisher. A partial cache is not a
  hard failure but a **fail-open** one (unknown locations classify as
  `Known=false`), which is quiet, so it must be watched for.
- ~~The no-outbox `Save`-then-`Publish` shape applies here too: a publish
  that fails after the repository commit leaves this service correct and
  the topic permanently missing that event. Fleet-wide gap, tracked
  separately, deliberately not patched here.~~ *(Closed by
  [ADR-0018](./0018-transactional-outbox.md): publishing now goes through
  the Postgres transactional outbox, so the store and the topic can no
  longer diverge.)*

## Related

- ADR 0008 — the synchronous classification endpoint this consumer
  replaces (kept as the rollback).
- ADR 0009 — the Published Language and the publisher itself.
- `inventory-storage` ADR 0013 — the consumer side: its cache, its
  readiness gate, and its `LOCATION_LOOKUP_MODE` rollback.
- `warehouse-infra`'s `deploy_facility_events_integration` — the Terraform
  flag that turns both sides on together.
