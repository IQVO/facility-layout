---
id: ddd-artifacts
title: DDD artifact pack (ddd-crew)
sidebar_label: Artifact pack index
sidebar_position: 1
description: Index of the ddd-crew DDD artifacts and UML/ER/sequence diagrams for the Facility Layout bounded context, all derived from the code on develop.
---

# DDD artifact pack (ddd-crew)

This pack describes the **Facility Layout** bounded context with the
[ddd-crew](https://github.com/ddd-crew) modelling tools plus UML, ER and
sequence diagrams. Every diagram is Mermaid, every page lists the source
files it was derived from, and every page states what it leaves out.

| Artifact | Page | ddd-crew tool / notation it follows |
|---|---|---|
| Core Domain Chart | [Core Domain Chart](./core-domain-chart.md) | [Core Domain Charts](https://github.com/ddd-crew/core-domain-charts) |
| Bounded Context Canvas | [Bounded Context Canvas](./bounded-context-canvas.md) | [Bounded Context Canvas v5](https://github.com/ddd-crew/bounded-context-canvas) |
| Context Map | [Context map](../ecosystem/context-map.md) (kept under Ecosystem, not duplicated here) | [Context Mapping](https://github.com/ddd-crew/context-mapping) |
| Aggregate Design Canvas | [Aggregate Design Canvas](./aggregate-design-canvas.md) — narrative companions: [Aggregates](./aggregates.md), [Invariants](./invariants.md) | [Aggregate Design Canvas v1.1](https://github.com/ddd-crew/aggregate-design-canvas) |
| Domain Message Flow | [Domain Message Flow](./domain-message-flow.md) | [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling) |
| EventStorming (design level) | [EventStorming](./eventstorming.md) | [EventStorming glossary & cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet) |
| Ubiquitous Language | [Ubiquitous language](../business-context/ubiquitous-language.md) (kept under Business Context, not duplicated here) | Glossary mapped to code identifiers |
| UML class diagrams | [Class diagrams](./class-diagram.md) | UML class diagram + hexagonal ports/adapters view |
| ER diagram | [Entity-relationship](./entity-relationship.md) | Crow's-foot ER of the final migrated schema |
| UML sequence diagrams | [Sequence diagrams](./sequence-diagrams.md) | UML sequence diagrams per command use case |
| Domain events | [Domain events](./domain-events.md) | Published Language catalogue (CloudEvents 1.0) |

Related narrative pages: [Subdomain classification](./subdomain-classification.md)
and [Hexagonal architecture](./hexagonal-architecture.md).

## Sources of truth

The code on `develop` wins over every page in this pack. The pages were
derived from:

- `internal/domain/**` — aggregates, value objects, enums, domain errors and
  the twelve domain events (`internal/domain/shared/events.go`).
- `internal/application/usecases/**` and `internal/application/ports/ports.go`
  — the 31 use cases and the 12 outbound ports.
- `internal/adapters/**` — the REST router (`inbound/http/server.go`), MCP
  tools (`inbound/mcp/tools.go`), the CloudEvents helper
  (`kafka/cloudevents/cloudevents.go`), publishers, outbox and the analytics
  consumer.
- `migrations/*.up.sql` and `migrations/analytics/*.up.sql` — the schema.
- `apis/openapi.yaml` and `apis/asyncapi.yaml` — the published contracts.
- `docs/docs/adr/` — the decisions behind the shape (ADR 0001–0031).
- For downstream relationships, each consumer repository's own `develop`
  branch (paths cited on the [Context map](../ecosystem/context-map.md)).

When a page and the code disagree, the code is right and the page is a bug.
