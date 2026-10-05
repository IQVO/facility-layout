---
id: 0001-hexagonal-ports-and-adapters
title: 0001 — Hexagonal (ports and adapters) architecture
sidebar_label: 0001 · Hexagonal architecture
description: Ports and adapters with a strict inward dependency rule, enforced by an executable arch-go fitness test.
---

# 0001 — Hexagonal (ports and adapters) architecture

**Status:** Accepted

## Context

`facility-layout` is the fifth service in the `warehouse-systems` platform.
The four that came before it — `inventory-storage`, `wes-work-planning`,
`workforce-management`, `fulfillment-execution` — all use hexagonal
architecture with an identical package layout and an identical dependency
rule, described in each repository's `CLAUDE.md` as NON-NEGOTIABLE.

The forces at play:

- **The domain rules are the asset.** This context's value is a set of
  invariants: seven-segment code validation, the Site → Zone → Aisle chain of
  custody, PlacementRule evaluation. Those must be testable without a
  database, an HTTP server, or a broker.
- **Two persistence modes are needed from day one.** The service must run
  against Postgres and also entirely in memory — for local exploration, unit
  tests, and the godog acceptance suite that drives the real HTTP API through
  `httptest.NewServer`.
- **Fleet consistency has real value.** Five services with the same shape
  means an engineer who knows one knows all five, and the CI configuration,
  linters and architecture tests transfer directly.
- **A layering rule that is only a convention decays.** The other four
  repositories learned this and added `arch-go` fitness tests later
  (their Task 14). Nothing about this service makes it immune.

The alternative considered was a conventional layered/MVC structure with
handlers calling services calling repositories, which is simpler to start but
puts framework and SQL types within reach of the business rules.

## Decision

We will use **hexagonal / ports-and-adapters** architecture with a strict
inward dependency rule:

> The domain depends on nothing. The application depends on the domain.
> Adapters depend on the application and the domain. Nothing points inward
> from the outside except through a port.

Concretely:

- `internal/domain/**` imports only the standard library and itself. No
  framework type, no SQL type, no `net/http` type.
- `internal/application/ports` contains **only** driven (outbound)
  interfaces — the repositories, `EventPublisher` and `Clock` — and they
  traffic in domain types, not primitives.
- `internal/application/usecases` holds one struct per use case.
- `internal/adapters/{inbound/http, inbound/kafka, inbound/mcp,
  outbound/postgres, outbound/memory, outbound/events, outbound/kafka,
  outbound/analyticsstore, outbound/telemetry,
  outbound/bootretry}` implement the ports.
- `cmd/facility/main.go` is the composition root of the API deployable;
  `cmd/mcp`, `cmd/facility-projector` and `cmd/facility-reports` are
  composition roots of the three later deployables (MCP inbound adapter
  ADR-0007; analytics writer/reader ADR-0010). *(Originally this ADR
  described a single `cmd/facility`; the port count and adapter list
  above reflect the current tree rather than the 2026-08 shape.)*

Aggregates do not reach outside themselves. Where a domain rule needs data an
aggregate cannot own — most visibly `slot.NewLocationSlot`, which must
evaluate every applicable `PlacementRule` — the use case loads that data and
**passes it in**:

```go
func NewLocationSlot(
	code shared.LocationCode,
	locationType placement.LocationType,
	capacityOverride shared.Capacity,
	attrs placement.ZoneAttributes,
	rules placement.RuleSet,
) (*LocationSlot, error)
```

We will enforce the rule as an **executable fitness test**, not a convention:
`internal/architecture/architecture_test.go` uses
[`arch-go`](https://github.com/arch-go/arch-go) and runs as its own blocking
`arch-test` CI job. Unlike the sibling repositories, this one has the test
from the first commit rather than retrofitted.

## Consequences

### Easier

- Every invariant is unit-testable with zero test doubles. The domain has no
  dependencies to fake.
- Swapping Postgres for in-memory is one branch in `main.go`. The HTTP layer
  and the entire test suite are byte-identical in both modes, which is what
  makes the BDD suite able to drive the real router end-to-end with no
  infrastructure.
- The SVG renderer stayed genuinely contained. It lives in
  `internal/adapters/inbound/http/svg.go`, consumes the same read model the
  JSON endpoint does, and no domain or application code knows it exists.
- A layering violation is a red build, not a review comment somebody might
  miss.
- An engineer moving between the five services finds the same tree.

### Harder

- More indirection than the problem strictly demands. This service is not
  complex; a flat structure would be shorter.
- Mapping code at every boundary: HTTP DTOs ↔ domain types, domain types ↔
  SQL rows. That mapping is explicit, hand-written and must be maintained.
- Port signatures taking domain types (`SlotRepo.FindByCode` takes a
  `shared.LocationCode`, not a `string`) make adapters slightly more verbose
  in exchange for making it impossible to hand the application an
  unvalidated code.
- `Clock` as a port means no code outside `main.go` may call `time.Now()`.
  This is easy to forget and is caught by review rather than by the
  compiler.
- Six repository interfaces means six in-memory implementations to keep
  thread-safe and in step with the Postgres ones.
