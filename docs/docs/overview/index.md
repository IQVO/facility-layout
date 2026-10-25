---
id: index
title: Facility Layout
sidebar_label: Introduction
description: The warehouse map — Site, Zone, Aisle and coded LocationSlots, and the rules for what may legally be stored where.
---

# Facility Layout

:::warning[Study project]
This documentation site is an educational Domain-Driven Design exercise. It
follows real industry-standard patterns and terminology, but it is **not a
production system** and is **not affiliated with, endorsed by, or
representative of any real-world company**.
:::

**Facility Layout** is the bounded context that owns *where things physically
are in the building*: a site's structural hierarchy — Site → Area → Zone →
Aisle → Bay → Level → Position — and the coded storage slots inside it.

It does **not** own occupancy or stock. That stays in `inventory-storage`'s
`Bin` and `StockUnit` aggregates. What this service owns is whether a coded
location **exists**, **is active**, and **is legal for a given kind of storage
unit** — the warehouse map that other contexts read but never write.

```
WH1-STOR-AMB-A07-03-02-B
 |    |    |   |   |  |  `-- Position: left-to-right slot on the level
 |    |    |   |   |  `----- Level:    vertical level / shelf
 |    |    |   |   `-------- Bay:      bay / section along the aisle
 |    |    |   `------------ Aisle:    physical corridor
 |    |    `---------------- Zone:     behavioral class (AMB/CHL/FRZ/HAZ/FWD/RSV)
 |    `--------------------- Area:     coarse functional area (STOR/RCV/PACK/STAGE)
 `-------------------------- Site:     the physical facility
```

## What this service is for

| Question a caller asks | Answered by |
|---|---|
| Does `WH1-STOR-AMB-A07-03-02-B` exist, and is it active? | `GET /locations/{locationCode}` |
| May a `PalletRack` slot legally exist in the frozen zone? | Enforced at registration by [PlacementRules](../business-context/domain-vision.md#placementrules-the-enforcement-point) |
| What does this building look like, top to bottom? | `GET /sites/{siteCode}/layout` |
| What does one zone look like as a paintable grid? | `GET /zones/{zoneId}/grid` |
| Which aisle comes next in walk order? | An Aisle's `SequenceHint` |
| How far apart are two slots on the map? | `GET /distance?from=&to=` (routed within a zone; straight-line estimate across zones when both slots have position geometry) |
| Which of a site's slots are dock doors, work centers, QC stations…? | `GET /sites/{siteCode}/locations?role=` |
| Is this slot in a hazmat or temperature-controlled zone? | `GET /locations/{locationCode}/classification` |

## Strategic position, in one paragraph

This is a **Generic Subdomain**, in the same bucket as Cartonization and WCS
in the platform's DDD reference. Physical-location truth is well understood
and is not a competitive differentiator, and it is needed by contexts on both
sides of the WMS/WES line: `inventory-storage` needs location validity to
accept a stow, and `wes-work-planning` / `fulfillment-execution` need
zone/aisle adjacency for travel-path and congestion reasoning. Neither owns
it; both consume it (as do `warehouse-planning`, for storage capacity, and
`warehouse-ops-agent`, over MCP). Following the reference's discipline —
*"extract generic logic instead of duplicating it"* — it is therefore its own
bounded context and its own service, not a package bolted onto
`inventory-storage`. See
[Subdomain classification](../ddd/subdomain-classification.md).

## The four ideas worth knowing

1. **The LocationCode is a value object, not a string.** Seven typed
   segments, `[A-Z0-9]` only, always round-tripping through
   `String()`/`ParseLocationCode()`. See
   [The location code](../business-context/location-code.md).
2. **Registering a slot is a chain-of-custody check, not an insert.** The
   Site → Zone → Aisle chain the code implies must resolve to existing,
   `Active` aggregates. No orphan slots, ever. See
   [Invariants](../ddd/invariants.md).
3. **PlacementRules are enforced once, at registration time.** The rule that
   keeps ambient shelving out of the frozen zone runs when the slot is
   created — not on every read, and not in every calling service. See
   [ADR 0003](../adr/0003-placement-rules-at-registration-time.md).
4. **"Drawing the warehouse" is a first-class capability.** Two read models
   return renderable structure, not raw rows a client has to join and sort.
   See [Drawing the warehouse](../api-reference/drawing-the-warehouse.md).

## Where to go next

- [Getting started](./getting-started.md) — run it locally in under a minute.
- [Domain vision](../business-context/domain-vision.md) — why this context
  exists the way it does.
- [Ubiquitous language](../business-context/ubiquitous-language.md) — the
  exact vocabulary this service speaks.
- [API Reference](../api-reference/index.md) — every endpoint, generated from
  the real `apis/openapi.yaml`.
- [Context map](../ecosystem/context-map.md) — how this fits the other
  warehouse-systems services, and which of them consume it today.
- [Architecture Decision Records](../adr/index.md) — the decisions, and why.
