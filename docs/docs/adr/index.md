---
id: index
title: Architecture Decision Records
sidebar_label: About ADRs
description: Why these records exist, the template they follow, and how to propose a new one.
---

# Architecture Decision Records

An **Architecture Decision Record** captures one architecturally significant
decision: what was decided, what was going on at the time that made the
decision necessary, and what the team now has to live with as a result.

The records here are not aspirational design documents. Each one reconstructs
a decision that is **actually visible in this repository** — in the code, in
`CLAUDE.md`, in `TASKS.md`, or in the shape of the API. If you can't point at
the consequence in the tree, it isn't an ADR.

## Why keep them

The alternative is that the reasoning lives in somebody's head, or in a
review thread nobody can find. Six months later the code says *what* the
system does and nothing says *why*, so the next person either preserves a
constraint that stopped mattering or removes one that still does.

For this service specifically the stakes are higher than usual: it is a
[Generic Subdomain](../ddd/subdomain-classification.md) that four other
contexts are meant to conform to. Decisions about its coded address format
and its enforcement points are expensive to reverse once anyone depends on
them, so the reasoning behind them is worth writing down.

## The template

These follow **Michael Nygard's** lightweight format — the de facto standard
— one markdown file per decision, numbered `0001-`, `0002-`, and so on:

| Section | Contains |
|---|---|
| **Title** | A short noun phrase naming the decision |
| **Status** | Proposed / Accepted / Deprecated / Superseded by ADR-NNNN |
| **Context** | The forces at play — technical, organisational, domain. Written in value-neutral language: what was true, not what we wanted. |
| **Decision** | The response to those forces, stated actively: *"We will…"* |
| **Consequences** | What becomes easier **and** what becomes harder. Both. A consequences section with no downsides is a sales pitch. |

Records are **immutable once accepted**. A decision that changes gets a new
record that supersedes the old one; the old one stays, with its status
updated. The history of what was believed and when is the point.

## The records

| # | Title | Status |
|---|---|---|
| [0001](./0001-hexagonal-ports-and-adapters.md) | Hexagonal (ports and adapters) architecture | Accepted |
| [0002](./0002-hierarchical-location-code.md) | Industry-standard hierarchical location code over a flat identifier | Accepted |
| [0003](./0003-placement-rules-at-registration-time.md) | PlacementRules enforced once at registration time | Accepted |
| [0004](./0004-rfc-7807-from-day-one.md) | RFC 7807 problem details from the first commit | Accepted |
| [0005](./0005-one-way-decommission.md) | One-way decommission, no reactivation in v1 | Accepted |
| [0006](./0006-partial-success-bulk-import.md) | Bulk import reports partial success per row | Accepted |
| [0007](./0007-mcp-inbound-adapter.md) | Model Context Protocol as an inbound adapter, not a new service | Accepted |
| [0008](./0008-location-classification-read-endpoint.md) | Location classification read endpoint | Accepted |
| [0009](./0009-kafka-integration-publisher.md) | Kafka integration publisher for the Published Language | Accepted (envelope superseded by ADR-0024) |
| [0010](./0010-analytical-data-product.md) | Per-service analytical data product (report) via a separate analytics topic | Accepted (envelope superseded by ADR-0024) |
| [0011](./0011-micro-frontend-console-adoption.md) | Adoption of the fleet-wide micro-frontend console architecture (warehouse-ops-agent ADR-0002) | Accepted |
| [0012](./0012-standard-metrics-convention.md) | Standard metrics convention across the fleet | Accepted |
| [0013](./0013-first-published-language-consumer.md) | inventory-storage is the first real consumer of the Published Language | Accepted |
| [0014](./0014-rest-identity-adoption.md) | Adopt the fleet REST identity standard (static bearer keys, read/read-write scopes; warehouse-ops-agent ADR 0005) | Accepted |
| [0015](./0015-remove-rest-mcp-auth.md) | Remove the static-bearer REST/MCP auth layer (reverts 0014 and the auth portion of 0007) | Accepted |
| [0016](./0016-functional-location-roles.md) | Functional location roles beyond storage (Dock, Yard, WorkCenter, Drop, QC, Consolidation, Shipping) | Proposed |
| [0017](./0017-geometry-and-travel-graph.md) | Physical geometry and a travel-distance read model | Proposed |
| [0018](./0018-transactional-outbox.md) | Transactional outbox + relay for atomic event publishing | Accepted |
| [0019](./0019-idempotency-key-middleware.md) | Transactional Idempotency-Key middleware for resource-creation POSTs | Accepted |
| [0020](./0020-resilience-kafka-dlq-graceful-shutdown.md) | Kafka consumer dead-letter queue and graceful shutdown hardening | Accepted |
| [0021](./0021-kafka-writer-balancer-hash.md) | Kafka writer Balancer switched from LeastBytes to Hash | Accepted |
| [0022](./0022-horizontal-autoscaling-and-pgxpool-tuning.md) | Per-workload HorizontalPodAutoscaler and pgxpool MaxConns/statement_timeout tuning | Accepted |
| [0023](./0023-migrations-direct-postgres-connection.md) | Run golang-migrate against a direct Postgres connection, not PgBouncer | Accepted |
| [0024](./0024-cloudevents-mandatory-envelope.md) | CloudEvents 1.0 as the mandatory event envelope | Accepted |

## The Kafka record that was deferred until the adapter existed

Earlier revisions of this index noted that, unlike the sibling services, this
repository had **no** Kafka/CloudEvents ADR — because it published no
integration events. It had an in-process log publisher, a buffered test
publisher, and a Postgres `events` outbox that nothing drained. Writing that
ADR then would have documented a decision nobody had made.

That moment has now arrived: [ADR 0009](./0009-kafka-integration-publisher.md)
adds the broker adapter that publishes this service's full Published Language to
`warehouse.facility.events`, selected by `EVENT_PUBLISHER=kafka`. The
CloudEvents `type` naming convention (see
[Domain events](../ddd/domain-events.md)) is now backed by an actual publisher.
See [Context map](../ecosystem/context-map.md).

## Proposing a new one

1. Copy the most recent record and renumber it. Numbers are never reused,
   even if a record is later withdrawn.
2. Write the **Context** first, and write it neutrally. If the context
   section already argues for the outcome, the decision was not really open.
3. Write **Consequences** honestly, including the ones you dislike. That
   section is what makes the record useful to whoever has to revisit it.
4. Open it as `Proposed`. Flip it to `Accepted` when it is agreed, or leave
   it `Proposed` and supersede it later if the discussion goes elsewhere.
5. Add it to the table above and to `sidebars.ts`.
