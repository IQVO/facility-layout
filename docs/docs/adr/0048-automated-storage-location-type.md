---
id: 0048-automated-storage-location-type
title: "48. The AutomatedStorage location type: one AS/RS is one logical location"
sidebar_label: 48. AutomatedStorage location type
sidebar_position: 48
description: "ADR 0048 — well-known LocationTypes AutomatedStorage (role Storage), AsrsPort and HandoffPoint (role Drop) with a canonical-role check; at most one active AutomatedStorage slot per aisle and AsrsPort slots only in such an aisle; the WorkCenter activity Pick for goods-to-person stations. No new role, field or event; every consumer proven tolerant."
---

# 48. The `AutomatedStorage` location type

## Status

**Accepted** (2026-11-20). Additive: no existing route, event, field or column
changes meaning; one enum value (`Pick`) is added to the WorkCenter activities.
Builds on [ADR 0016](./0016-functional-location-roles.md) (location roles) and
follows the additive pattern of [ADR 0040](./0040-slot-handling-unit.md) and
[ADR 0045](./0045-gs1-location-identity.md). Records the `facility-layout` row of the
WCS design (warehouse-docs `docs/wcs/ecosystem-impact.md`, seam decision 2).

## Context

The WCS stores containers in an AS/RS that moves them between rack positions on
its own. Seam decision 2: "`facility-layout` gains a `LocationType`
`AutomatedStorage` with the role `Storage`. Each AS/RS is **one logical location**
with an ordinary `LocationCode`. … Rack positions stay inside `asrs-control`."
The ecosystem-impact row adds: "registers AS/RS ports, stations and dock handoffs as
locations", and the AMR map (amr-fleet-control, context-map relationship 3) is built
from this context's travel graph and its "waypoints, edges, stations, dock doors,
handoff points".

LocationType names are free strings (`RegisterLocationType` accepts any name); the
role is a closed enum. Nothing stops `AutomatedStorage` being registered as a dock
today, and nothing says which ports belong to which AS/RS.

## Decision

1. **Well-known types with canonical roles.** `AutomatedStorage` → `Storage`;
   `AsrsPort` → `Drop`; `HandoffPoint` → `Drop` (Drop: "used to hold both inbound
   and outbound LPNs while in between warehouse processes"). Registering one of
   these names with another role is `422 location-type-role-mismatch`. Every other
   name stays free, exactly as today. No new LocationRole, handling unit or event.
2. **One AS/RS is one Aisle with one `AutomatedStorage` slot.** At most one active
   `AutomatedStorage` slot per aisle (`409 automated-storage-already-in-aisle`,
   backed by a Postgres partial unique index for concurrent registrations). An
   `AsrsPort` slot is accepted only in an aisle that holds an active
   `AutomatedStorage` slot (`422 asrs-port-without-automated-storage`).
   Decommissioning the AS/RS slot does not cascade to its ports. A keyed
   `POST /location-imports` that loses the aisle race on the index answers `500
   internal-error`: the whole import rolls back, its key is released, and a retry
   re-executes (and reports the row rejected).
3. **Convention** (not enforced beyond decision 2): the AS/RS zone is `<site>-AUTO-ASRS`
   (area `AUTO`, zone code `ASRS`), the aisle code is asrs-control's `asrsId`, the
   storage slot is `<site>-AUTO-ASRS-<asrsId>-00-00-S`, a port is
   `<site>-AUTO-ASRS-<asrsId>-<asrs aisle, 2 digits>-00-<portId>` (e.g.
   `WH1-AUTO-ASRS-ASRS1-01-00-IN1`), so material-flow-control and amr-fleet-control
   read `(asrsId, portId)` from a port's code: aisle segment and position segment.
   The port's geometry (`PUT /locations/{code}/geometry`) is where robots drop and
   pick. A handoff point is a `HandoffPoint` slot in the zone it serves (a dock's
   outbound lane, a forward area).
4. **Stations are WorkCenter locations.** The activity `Pick` is added for
   goods-to-person stations; a decant station declares `Receive` and
   `Deconsolidate` (existing). fulfillment-execution already requires a station's
   location to be a WorkCenter.

## Consumers (proved before this change; re-verified in the PR)

| Consumer of `LocationSlotRegistered` / `GET .../locations` | Reads | Why it is tolerant |
| --- | --- | --- |
| inventory-storage facility cache | `locationCode`, `zoneId`, `handlingUnit`, and since its ADR 0054 `locationType` | free string; `AutomatedStorage` makes the bin container-only there |
| inbound-receiving dock-door copy | `role`, `dockFlow` | acts on role `Dock` only |
| dock-scheduling, yard-management | `role` | act on `Dock` / `Yard` only |
| outbound-shipping | `role` | acts on `Staging` only |
| order-sourcing, task-interleaving | codes, zones, geometry | no role or type switch |
| pick-face-replenishment | `role` (`IsStorage`) | reserve sources need a zone in `RESERVE_ZONE_CODES` (default `RSV`); the AS/RS zone `ASRS` is not one |
| slotting-optimization | `role` | forward candidates need a zone in `FORWARD_ZONE_CODES` (default `FWD`); `ASRS` is not one |
| warehouse-planning storage tally | `role`, `locationType`, `activities` | Storage → a bucket per type (a new `AutomatedStorage` bucket); WorkCenter → a bucket per upper-cased activity (a new `PICK` bucket); `Drop` → skipped with a WARN, as today |
| fulfillment-execution station registration | `role` | WorkCenter required, unchanged |
| e2e-tests `seed-inventory`, `retail-day` | `role=Storage` listing | changed first to skip `AutomatedStorage` (e2e-tests PR, WCS plan 05a Task 1) |

## Consequences

- An AS/RS appears in every Storage-role reading as ONE slot with the AS/RS's
  capacity envelope; its rack positions never exist here.
- material-flow-control and amr-fleet-control can locate an AS/RS's ports without
  a new attribute; if they ever need one (port kind, port order), it is a later
  additive field.
- The cluster's AS/RS, ports, stations and handoffs are data registered by WCS plan
  06, not by this change.

## Alternatives considered

1. **A new LocationRole `AutomatedStorage`.** Rejected: seam decision 2 fixes the
   role as `Storage`, and a new role value breaks every consumer that switches on
   role (warehouse-planning WARN-skips unknown roles).
2. **Rack positions as slots.** Rejected by seam decision 2.
3. **An `asrsId` attribute on port slots.** Rejected for now: the aisle already
   expresses membership; an attribute is a REST + event + DB + MCP + A2A change for
   a fact the code carries.
