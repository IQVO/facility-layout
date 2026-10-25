---
id: 0034-facility-layout-imported-stays-keyed-by-cloudevents-id
title: "34. FacilityLayoutImported stays keyed by its CloudEvents id"
sidebar_label: 34. Import event key kept
sidebar_position: 34
description: "Decided 2026-10-06: FacilityLayoutImported keeps its CloudEvents id as the Kafka key. No consumer needs import ordering; keying by site would need an additive siteCode on the event (a contract change) for no benefit."
---

# 34. FacilityLayoutImported stays keyed by its CloudEvents id

## Status

**Accepted** (decided 2026-10-06). Confirms decision 2 of
[ADR 0032](./0032-aggregate-partition-keys-for-geometry-events.md); changes
no code and no contract.

## Context

ADR 0032 keyed the four geometry-family events by aggregate identity and left
`FacilityLayoutImported` — a summary of one whole `ImportFacilityLayout` call,
with no aggregate and no site or zone in its payload — keyed by its CloudEvents
`id`. Its consequences section noted that site-scoped ordering of imports
would need an additive `siteCode` on the event, and called that a product
decision. The 2026-10-05 docs audit listed the key as an open question.

## Decision

We keep `FacilityLayoutImported` keyed by its CloudEvents `id`.

- No consumer needs ordering between import summaries. The only reader,
  facility-layout's own `AnalyticsConsumer`, applies an additive per-event-id
  counter (`IsProcessed`/`MarkProcessed`) that is order-independent;
  `inventory-storage` and `warehouse-planning` ignore the event.
- Keying by site would need an additive `siteCode` in the payload — a contract
  change — for no consumer benefit.
- `id`-keying spreads import batches over the partitions and keeps an outbox
  redelivery of the same event on the same partition (ADR 0032).

## Consequences

- Nothing changes on the wire. The question is closed: reopen it only when a
  consumer demonstrates a need for per-site import order, and then through an
  additive `siteCode` and a new ADR.
- No ordering is promised between an import summary and the per-slot events it
  accompanies (unchanged from ADR 0032).

## Related

- [ADR 0032](./0032-aggregate-partition-keys-for-geometry-events.md) — the
  key decision this confirms; [ADR 0024](./0024-cloudevents-mandatory-envelope.md)
  — the envelope and `id`.
- [Event storming](../ddd/eventstorming.md) (sticky H5),
  [Domain events](../ddd/domain-events.md).
