---
id: 0021-kafka-writer-balancer-hash
slug: /adr/0021-kafka-writer-balancer-hash
title: 21. Kafka writer Balancer switched from LeastBytes to Hash
sidebar_label: 21. Kafka Balancer: LeastBytes -> Hash
sidebar_position: 21
description: "ADR 0021 -- every kafkago.Writer in internal/adapters/outbound/kafka (Publisher, AnalyticsPublisher, RelaySink) used &kafkago.LeastBytes{}, which ignores Message.Key entirely for partition routing. This service's Message.Key was already set correctly for every event (aggregateKey), but LeastBytes silently defeated it, so per-aggregate event ordering was not actually guaranteed once warehouse-infra PR #42 took every business topic from 1 to 8 partitions. Fix: switch every writer's Balancer to &kafkago.Hash{} (FNV-1a over Key), verified with a real-broker Testcontainers test on an 8-partition topic. Sourced from order-management PR #111 / ADR 0027, which found and fixed the identical bug."
---

# 21. Kafka writer Balancer switched from LeastBytes to Hash

## Status

Accepted — implemented in the same change that introduces this record.

## Context

A fleet-wide code audit following order-management's, inventory-storage's,
and workforce-management's Kafka partition-key fixes (PRs #111, #102,
#106, all merged into `develop`) found this repo carrying the same latent
bug those three had just fixed: `kafka-go`'s `LeastBytes` balancer
completely ignores `Message.Key` when choosing a partition.
`LeastBytes.Balance` picks whichever partition currently has the least
cumulative bytes written, reading `len(msg.Key) + len(msg.Value)` only to
update that running total — it never hashes or otherwise routes on the
key's *content*.

Unlike order-management's original finding, this repo's own
`aggregateKey` helper (`internal/adapters/outbound/kafka/publisher.go`)
was already correct: every event `Publisher`, `AnalyticsPublisher`, and
the outbox relay's `RelaySink` forward already carries a non-nil,
per-aggregate `Message.Key` (verified directly against
`origin/develop:internal/adapters/outbound/kafka/publisher.go` before
writing this record, not assumed). But all three of this package's
`*kafkago.Writer` literals were configured with `Balancer:
&kafkago.LeastBytes{}` — so the correct key was being computed and set,
and then silently discarded for partition-placement purposes by the
writer itself.

This was invisible for the same reason order-management's was: it was
accidentally safe when this service's topics (`warehouse.facility.events`,
`warehouse.facility.analytics`) ran at 1 partition — every message
necessarily lands on the only partition regardless of the balancer. It
stopped being safe the moment `warehouse-infra` PR #42 (Phase 3 of the
fleet's Kafka scaleup, already merged) took every business topic,
including both of this service's, to 8 partitions. At 8 partitions, a
correct `Key` under `LeastBytes` gives no per-aggregate partition
affinity at all: two events for the same `ZoneID` (or `SiteCode`,
`AisleID`, etc. — whatever `aggregateKey` returns) can land on different
partitions, purely as a function of how many bytes each partition had
already accumulated when each message was written. A downstream
Conformist consumer (inventory-storage, wes-work-planning,
workforce-management, fulfillment-execution — this service is an Open
Host Service, per ADR 0009) reading with more than one consumer instance
could then observe a later event for one aggregate before an earlier one
for that same aggregate, with nothing on the publish side having raced.

A fake-writer unit test (this package's existing `publisher_test.go`)
cannot catch this: it only asserts `msg.Key == expectedKey`, which
proves the key is *set*, not that it is *used* for routing. Those are
independently-breakable properties with this library — see
order-management's `ADR 0027` and its integration test for the same
finding. Confirmed here concretely: a real-Kafka Testcontainers test
publishing 3 events for one aggregate on an 8-partition topic found only
1 of 3 landing on the same partition under the old `LeastBytes` balancer
(the other 2 scattered elsewhere), and 3 of 3 after switching to
`&kafkago.Hash{}` — see
`internal/adapters/outbound/kafka/publisher_partition_integration_test.go`.

## Decision

1. Every `*kafkago.Writer` this package constructs —
   `Publisher.NewPublisher`'s writer (`publisher.go`),
   `AnalyticsPublisher.NewAnalyticsPublisher`'s writer
   (`analytics_publisher.go`), and `RelaySink.NewRelaySink`'s writer
   (`relay_sink.go`) — has its `Balancer` switched from
   `&kafkago.LeastBytes{}` to `&kafkago.Hash{}` (FNV-1a over
   `Message.Key`, `kafka-go`'s key-based balancer — the balancer choice
   order-management's ADR 0027 independently arrived at for the identical
   bug).
2. `Message.Key`/`Encoded.Key` construction (`aggregateKey`,
   `Publisher.Encode`, `AnalyticsPublisher.Encode`) is **unchanged** —
   this repo's key-setting logic was already correct per the audit that
   found this bug, and this ADR does not touch it.
3. No custom `Balancer` implementation is introduced. `kafka-go`'s stock
   `Hash` balancer plus the already-correct, non-nil `Key` is sufficient:
   this service needs "same aggregate id always lands on the same
   partition," not a specific partition number.

## Consequences

- Every event this package publishes for the same aggregate (same
  `ZoneID`, `SiteCode`, `AisleID`, `LocationType`, `RuleID`,
  `LocationCode`, etc., per `aggregateKey`) now deterministically lands
  on the same partition of both `warehouse.facility.events` and
  `warehouse.facility.analytics`, at any partition count, closing the gap
  that the 1→8 partition scaleup (warehouse-infra PR #42) opened. This
  matches order-management's PR #111 / ADR 0027 fix for the identical
  defect.
- No wire-format change: `Message.Key` was already being set correctly
  and is unchanged by this ADR; only the writer's `Balancer` field
  changed. No consumer of either topic needs any change.
- Verified with a real-broker Testcontainers integration test
  (`publisher_partition_integration_test.go`,
  `TestPublisherKeysMessagesForSameAggregateOntoTheSamePartition`) against
  an 8-partition topic, not a fake-writer unit test — mirroring
  order-management PR #111's test shape, per the same reasoning: a
  fake-writer test proves the key is set, not that the balancer routes on
  it.
- Source of this finding: order-management PR #111 (the reference fix,
  ADR 0027) and the original partition-scaleup audit, warehouse-infra PR
  #42, which is what made this defect observable across the fleet in the
  first place.
