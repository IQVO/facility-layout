---
id: domain-vision
title: Domain vision
sidebar_label: Domain vision
description: Why a separate service owns the warehouse map, and why placement legality is decided once rather than everywhere.
---

# Domain vision

> The system of record for **where things physically are in the building**:
> the site's structural hierarchy and the coded storage slots inside it. It
> owns whether a coded location **exists, is active, and is legal for a given
> kind of storage unit** — the warehouse map that other contexts read but
> never write.

Everything on this page follows from that sentence. This service is
deliberately narrow, and the boundary is the interesting part.

## What it owns, and what it refuses to own

| Concern | Owner |
|---|---|
| Does location `WH1-STOR-AMB-A07-03-02-B` exist? | **facility-layout** |
| Is it Active, or decommissioned, or under maintenance? | **facility-layout** |
| Is a `PalletRack` legal in this zone? | **facility-layout** |
| Which aisle comes next in walk order? | **facility-layout** |
| How many units of SKU X are in that location? | `inventory-storage` |
| Is that stock reserved, usable, or unlocated? | `inventory-storage` |
| Is SKU X hazmat or temperature-sensitive, and how big and heavy is one unit? | `product-master` |
| Who is picking from it right now? | `fulfillment-execution` |
| Should we release more work into that zone? | `wes-work-planning` |

The line is *structure versus contents*. Facility Layout knows the shelf
exists and what shape it is. It has no opinion whatsoever about what is
sitting on it. The e-commerce-fulfillment reference model puts "bin-accurate
location" inside the WMS-tier **Inventory & Slotting** core subdomain, and
notes that context exposes it as an Open Host Service. This service is the
generalized, multi-consumer version of exactly that concern, factored out so
that both the WMS tier and the WES tier can depend on it without depending on
each other.

## Why it is a separate service

The platform's DDD reference makes the argument for us, about a different
concern with the same shape:

> **Extract generic logic instead of duplicating it.** Cartonization is a
> good example: rather than implementing box-selection logic separately in
> both WMS (for planning/estimates) and WES (at point of pack), model it as
> its own Generic Subdomain both contexts call into.

Physical location is the same case:

- `inventory-storage` (WMS tier) needs location validity to accept a stow. A
  chaotic-storage stow is only valid if the scanned location is real and
  active — placing an item without a valid scanned location is precisely how
  inventory becomes "lost."
- `wes-work-planning` and `fulfillment-execution` (WES tier) need zone and
  aisle adjacency for travel-path and congestion reasoning. The WES
  ubiquitous language already contains `Zone`, `Travel Path` and
  `Congestion` — but nothing in the platform was the *source of truth* for
  what a Zone actually is, or which aisle is next in walk order.

Neither tier owns physical location. Both consume it. Duplicating the map in
both would guarantee they drift, and re-slotting a building would then be a
two-service migration with a window where they disagree. So the map is
extracted into one service, and everyone else references it.

## Why it is an Open Host Service with no inbound dependencies

This service has **no** dependency on any other warehouse-systems service,
and never will. It does not read `inventory-storage`'s stock, it does not
know what a Task is, and it does not call anybody. Everything it publishes is
its **Published Language**: twelve past-tense
[domain events](../ddd/domain-events.md) plus a stable REST surface.

That asymmetry is the point. A context that everyone depends on must be
cheap to depend on: no coupling back, no ordering constraints at startup, no
"the map is down so nobody can plan" cascade beyond a plain read failure. The
services that use it are downstream **Conformists** to whatever shape this
service publishes — they translate its vocabulary into their own models
rather than negotiating a shared one. See
[Context map](../ecosystem/context-map.md) for exactly who consumes what today
(`inventory-storage` and `warehouse-planning` over Kafka,
`wes-work-planning` and `fulfillment-execution` over REST,
`warehouse-ops-agent` over MCP).

## The location code is the product

Most of this service's value is one design decision: the coded address is
**hierarchical, human-parsable, and typed**, not an opaque identifier.

```
WH1-STOR-AMB-A07-03-02-B
```

Because the code carries its own hierarchy, a bare location string is
self-describing: you can read the zone out of it, you can sort by walk order,
you can group a whole aisle without a join, and an operator on the floor can
say it out loud over a radio. A flat UUID would need a lookup for every one
of those. See [The location code](./location-code.md) and
[ADR 0002](../adr/0002-hierarchical-location-code.md).

The trade-off is deliberate and recorded: a hierarchical code means a
re-organisation of the building changes codes. That is accepted, because
warehouse re-slotting is a rare, planned, physically-signposted event, while
reading and reasoning about locations happens millions of times a day.

## PlacementRules: the enforcement point

The rule "no ambient product in the frozen zone" has to live *somewhere*. The
tempting answers are all bad:

- **In each caller.** Then every service that creates or validates a location
  reimplements it, and they disagree the first time the rule changes.
- **In the read path.** Then every read pays for it, and an already-illegal
  slot can sit in the database for months before anybody notices.
- **In a nightly job.** Then the invalid state exists, and something has to
  clean it up.

This service puts it at **registration time**, in the domain, once. A
`PlacementRule` is `(LocationType, Effect, ZonePredicate)`. When a
`LocationSlot` is constructed, the use case loads the applicable rule set from
the repository and hands it to the aggregate — an aggregate never reaches out
to a repository itself. The rule set evaluates:

1. Any matching **`Deny`** rule naming this LocationType rejects it. Deny
   always wins.
2. If **any** matching `Allow` rule exists for the zone at all, the zone
   becomes an allow-list: the LocationType must be named by one of them.
3. Otherwise the zone is unconstrained and the placement is permitted.

The rejection error always names the exact rule that was violated:

```
location type violates a placement rule for this zone: PalletRack is denied in
zone WH1-STOR-FRZ by rule [RULE-FRZ-NO-SHELF: Deny PalletRack where
temperatureClass=Frozen]
```

Because the check happens once at the boundary, **every slot in the database
is legal by construction**, and downstream contexts can treat any Active slot
they read as already-validated. That is the whole reason this is a service
and not a shared library. See
[ADR 0003](../adr/0003-placement-rules-at-registration-time.md).

## Chain of custody, not bare insert

The other invariant that shapes the whole application layer: registering a
slot resolves the Site → Zone → Aisle chain the code implies, and rejects the
registration if any link is missing or not `Active`. There are no orphan
slots. A code like `WH1-STOR-AMB-A07-03-02-B` cannot exist unless site `WH1`,
zone `WH1-STOR-AMB` and aisle `WH1-STOR-AMB-A07` all exist and are all
Active.

This is why `RegisterLocationSlot` and `ImportFacilityLayout` are the only two
use cases with real orchestration, and why everything else is thin. See
[Invariants](../ddd/invariants.md).

## Drawing the warehouse is a capability, not a reporting afterthought

A warehouse map that cannot be drawn is a database table. The two read models
are first-class deliverables of this context:

- `GET /sites/{siteCode}/layout` returns the whole building nested
  zones → aisles → slots, **pre-ordered** — zones by id, aisles by
  `sequenceHint` walk order (not registration order), slots by
  bay → level → position. A client renders it top-down with no joins and no
  sorting.
- `GET /zones/{zoneId}/grid` returns one zone as an explicit 2D matrix whose
  `rows[i].cells` array is index-aligned with `columns`, with literal `null`
  where the rack has a gap. Iterate rows × columns and paint.

Both are **projections** assembled across the aggregates by querying the
repositories. They are not separately stored state, so they cannot go stale
relative to the aggregates they are built from. See
[Drawing the warehouse](../api-reference/drawing-the-warehouse.md) for the
actual JSON and SVG.
