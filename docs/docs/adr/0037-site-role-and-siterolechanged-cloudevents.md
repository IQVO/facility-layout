---
id: 0037-site-role-and-siterolechanged-cloudevents
title: "37. Site role: an explicit network role on the Site aggregate"
sidebar_label: 37. Site role
sidebar_position: 37
description: "ADR 0037 — the Site aggregate gets an explicit siteRole (15 network roles plus UNSPECIFIED), set at registration and changed by PUT /sites/{siteCode}/role, published on SiteRegistered and the new SiteRoleChanged, backfilled once from the seeded names, and paired with a read-only role -> default transfer capability table. Extends ADR 0035; capability stays an explicit command."
---

# 37. Site role: an explicit network role on the Site aggregate

## Status

**Accepted** (2026-10-13, final-gaps wave decision E2). Extends
[ADR 0035](./0035-site-transfer-capability-command.md): the transfer
capability stays a per-site, explicit, revisioned command; this record adds
the fact that tells an operator (and a seed) WHICH capability a site should
have. Reuses the `Site` version guard of
[ADR 0025](./0025-optimistic-concurrency-version-column.md) as extended by
ADR 0035.

## Context

Inter-site stock transfers (network-inventory-planning) rebalance inventory
between nodes that hold inventory. ADR 0035 made the capability an explicit
opt-in per site, but nothing said which sites the ecosystem needs enabled.
The kind of node a building is (a Non-Sortable Fulfillment Center, a
Delivery Station, an Inbound Cross-Dock) existed only inside the seeded
display name, for example `ABE8 - Non-Sortable Fulfillment Center
(Breinigsville, PA)`. Deriving behaviour from a display name is wrong: a name
is free text, can be edited, and is not a contract.

The ecosystem decision (E2) fixes the capability each role needs:

| Role (`siteRole`) | Origin | Destination | Why |
|---|---|---|---|
| `ROBOTICS_SORTABLE_FC` | yes | yes | stores ambient inventory |
| `NON_SORTABLE_FC` | yes | yes | stores ambient inventory |
| `TRADITIONAL_SORTABLE_FC` | yes | yes | stores ambient inventory |
| `SUB_SAME_DAY_FC` | yes | yes | stores ambient inventory |
| `FRESH_GROCERY_FC` | no | no | perishable, short shelf life: no rebalancing between FCs |
| `XL_FC` | yes | yes | stores ambient inventory |
| `SOFT_LINES_SORTABLE_FC` | yes | yes | stores ambient inventory |
| `MICRO_FC` | no | yes | urban forward node replenished from FCs, never a source |
| `SIMULATED_FC` | yes | yes | the simulation FC behaves like an ambient FC |
| `INBOUND_CROSS_DOCK` | yes | no | pushes inbound stock into FCs |
| `NATIONAL_INBOUND_CROSS_DOCK` | yes | no | pushes inbound stock into FCs |
| `RETURNS_CENTER` | yes | no | resellable returns go back to FCs |
| `SORTATION_CENTER` | no | no | parcel network, no storage |
| `DELIVERY_STATION` | no | no | parcel network, no storage |
| `AIR_HUB_GATEWAY` | no | no | parcel network, no storage |

## Decision

1. **`siteRole` lives on the Site aggregate** as the `site.Role` value: the
   15 roles above as stable UPPER_SNAKE wire values, plus `UNSPECIFIED` for a
   site nobody classified. `site.ParseRole` is exact (case and spelling);
   anything else is `ErrInvalidSiteRole`, REST/MCP slug `invalid-site-role`
   (422).
2. **Set at registration.** `POST /sites` takes an optional `siteRole`;
   omitted means `UNSPECIFIED`. `SiteRegistered` carries it as an additive
   `siteRole` field (camelCase, like the rest of that payload). A site
   created by the bulk import (`POST /locations/import`) is `UNSPECIFIED`.
3. **Changed by command.** `PUT /sites/{siteCode}/role` with
   `{"siteRole": "<ROLE>"}`. `Site.ChangeRole`:
   - rejects `UNSPECIFIED`, an unknown or a missing role
     (`invalid-site-role`);
   - treats the current role as an idempotent no-op (200, no event, so no
     Idempotency-Key);
   - rejects a decommissioned site (`site-not-active`, as ADR 0035);
   - otherwise sets the role, and the use case saves the row and publishes
     `SiteRoleChanged` (`siteCode`, `siteRole`, `previousSiteRole`; camelCase)
     through the transactional outbox in one unit of work. Kafka key and
     CloudEvents subject are the site code, so one site's changes stay
     ordered. The `sites.version` guard serialises concurrent writers; the
     loser gets 409 `concurrent-modification` and its outbox row rolls back.
4. **A role never flips capability.** Neither registration nor a role change
   touches the transfer capability or publishes `SiteCapabilityChanged`.
   Capability stays the explicit ADR 0035 command.
5. **One source of truth for the defaults.** The table above lives in the
   domain (`site.TransferDefaults()`) and is exposed read-only as
   `GET /site-roles/transfer-defaults` (one row per assignable role, stable
   order; `UNSPECIFIED` has no row, so an unclassified site's capability is
   left as it is). The warehouse-infra seed reads it and PUTs the capability
   once per Active site (decision E2); operators read the same endpoint
   instead of a copied table.
6. **Exposed on every site read.** `GET /sites`, `GET /sites/{siteCode}`,
   the site header of `GET /sites/{siteCode}/layout`, and the MCP
   `list_sites` / `get_site_layout` tools carry `siteRole`.
7. **One-time backfill (migration 0009).** The migration adds
   `sites.site_role TEXT NOT NULL DEFAULT 'UNSPECIFIED'` with a CHECK of the
   16 values, then classifies the rows that already exist from the seeded
   name pattern `<CODE> - <Role words> (<city>)`. The role words are the name
   with its leading `<CODE> - ` removed; a role matches only when its exact
   phrase is followed by ` (`:

   | Role words | `siteRole` |
   |---|---|
   | `Robotics Sortable Fulfillment Center (…` | `ROBOTICS_SORTABLE_FC` |
   | `Non-Sortable Fulfillment Center (…` | `NON_SORTABLE_FC` |
   | `Traditional Sortable Fulfillment Center (…` | `TRADITIONAL_SORTABLE_FC` |
   | `Sub-Same-Day Fulfillment Center (…` | `SUB_SAME_DAY_FC` |
   | `Fresh Grocery Fulfillment Center (…` | `FRESH_GROCERY_FC` |
   | `XL (Extra-Large) Fulfillment Center (…` | `XL_FC` |
   | `Soft-Lines Sortable Fulfillment Center (…` | `SOFT_LINES_SORTABLE_FC` |
   | `Micro-Fulfillment Center (…` (incl. `(store-attached)`) | `MICRO_FC` |
   | `Simulated FC (…`, or the whole name `Simulated FC` (site `SIM1`) | `SIMULATED_FC` |
   | `Inbound Cross-Dock (…` | `INBOUND_CROSS_DOCK` |
   | `National Inbound Cross-Dock (…` | `NATIONAL_INBOUND_CROSS_DOCK` |
   | `Reverse Logistics / Returns Center (…` | `RETURNS_CENTER` |
   | `Sortation Center (…` | `SORTATION_CENTER` |
   | `Delivery Station (…` | `DELIVERY_STATION` |
   | `Air Hub Gateway (…` | `AIR_HUB_GATEWAY` |
   | anything else | `UNSPECIFIED` |

   The backfill does not bump `sites.version` and publishes no
   `SiteRoleChanged`: it records what the names already said, once. From then
   on the name is display text only and the role changes only by command.

## CloudEvents type catalogue entry

This record adds one type to this service's catalogue (the table in
[ADR 0024](./0024-cloudevents-mandatory-envelope.md), whose body stays as
written):

| CloudEvents `type` | Kafka key | `subject` |
|---|---|---|
| `com.warehouse.wms.facility-layout.site.SiteRoleChanged` | `siteCode` | `siteCode` |

`dataschema` is `urn:warehouse:facility-layout:events:SiteRoleChanged:v1`;
the payload is camelCase (`siteCode`, `siteRole`, `previousSiteRole`).
`SiteRegistered` keeps its type and `v1` dataschema: `siteRole` is an
additive field.

## Consequences

- The capability decision becomes reviewable data: a seed or an operator
  reads `GET /site-roles/transfer-defaults` and `GET /sites` and applies the
  difference with the ADR 0035 command; no repo hard-codes the table.
- A new role needs a code change (domain table, OpenAPI/AsyncAPI enums) and a
  new migration extending the CHECK. That is deliberate: a role is a
  contract value.
- Consumers that cached sites before migration 0009 never saw a
  `SiteRoleChanged` for the backfill; they read the role from `GET /sites`.
  No consumer reads the role today.
- A site registered without a role stays `UNSPECIFIED` until an operator
  classifies it; the seed leaves its capability untouched.
- Contract changes are additive: `siteRole` on `Site`, `RegisterSiteRequest`
  and `SiteRegistered`; new `SiteRoleChanged` message, `PUT
  /sites/{siteCode}/role` and `GET /site-roles/transfer-defaults`.
