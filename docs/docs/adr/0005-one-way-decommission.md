---
id: 0005-one-way-decommission
title: 0005 — One-way decommission, no reactivation in v1
sidebar_label: 0005 · One-way decommission
description: Retiring a structure is terminal; re-registering a retired code is a duplicate, not a resurrection.
---

# 0005 — One-way decommission, no reactivation in v1

**Status:** Accepted — concurrency guarantee (a decommission can no longer
be silently overwritten by a concurrent write; see the version guard on
`SlotRepo.Save`) added by [ADR-0025](./0025-optimistic-concurrency-version-column.md).

## Context

Physical structure is removed from a warehouse: a rack is dismantled, an
aisle is closed, a whole zone is converted from ambient to chilled. The
service needs a way to say "this location is no longer part of the map."

Every structural aggregate — Site, Zone, Aisle, LocationSlot — shares the
same `Status` value object with three states: `Active`, `UnderMaintenance`,
`Decommissioned`. The open question was whether `Decommissioned` is
reversible.

The forces:

- **A LocationCode is an identity, and identity reuse is dangerous.** If
  `WH1-STOR-AMB-A07-03-02-B` is retired and later a physically different slot
  is given the same code, every downstream record referring to that code
  silently changes meaning. Consumers holding historical references cannot
  tell the two apart.
- **The obvious "convenient" behaviour is the worst one.** Letting
  `POST /locations` on a decommissioned code quietly reactivate the slot
  makes a *creation* request perform a *state transition* — invisible in the
  API, and impossible for a caller to have intended unambiguously.
- **Reactivation is genuinely rare.** Warehouses do reopen aisles, but on the
  timescale of a construction project, not a shift.
- **Reactivation semantics are not obvious.** If a slot is reactivated, does
  it re-validate against the *current* PlacementRules or the ones in force
  when it was created? Does it keep its old capacity override? Do downstream
  contexts get a `LocationSlotRegistered` again, or a new
  `LocationSlotReactivated`? Every answer is defensible and none is free.
- **A reversible state machine is harder for Conformist consumers.** Every
  consumer would need to handle a `Decommissioned → Active` transition,
  forever, for an event that almost never happens.

`CLAUDE.md` posed this explicitly and asked for a decision: *"A
`Decommissioned` slot cannot be re-activated by re-registering the same code
— it must go through an explicit reactivation use case (or, for v1, simply
stays a one-way transition — decide and document which, keep it simple:
one-way decommission is fine for v1)."*

## Decision

We will make decommission **one-way** for every structural aggregate in v1.

- `Decommission()` on Site, Zone, Aisle and LocationSlot is terminal.
  Calling it twice returns `ErrAlreadyDecommissioned` → `409 Conflict`.
- **There is no reactivation use case**, and no endpoint that performs one.
- **Re-registering a decommissioned LocationCode is rejected as a
  duplicate** — `ErrDuplicateLocationCode` → `409 Conflict` — not silently
  treated as a resurrection. The code is taken. It stays taken.
- Registering new structure against a decommissioned parent is rejected:
  `ErrSiteNotActive` / `ErrZoneNotActive` / `ErrAisleNotActive`, each
  `409 Conflict`. A decommissioned zone cannot grow a new aisle.

`UnderMaintenance` is retained as a **legal persisted state** with a
deliberately narrow role: a structure that exists but is temporarily out of
service, typically loaded from an external facility-management system. The
read models render it, and a slot in it can still be decommissioned, but v1
exposes **no use case that sets it**. It is a state the system can represent
and report, not one it can currently reach through its own API.

If reactivation is ever needed, it will be an explicit, separately-modelled
use case with its own event — not a relaxation of this rule.

## Consequences

### Easier

- **A LocationCode means exactly one physical slot, forever.** Historical
  references in other contexts can never silently change meaning.
- The state machine is trivial to reason about, test and document. There is
  one terminal state and no cycles.
- Conformist consumers never have to handle a `Decommissioned → Active`
  transition. `LocationSlotDecommissioned` means "stop offering this," full
  stop, with no possibility of a later contradiction.
- The dangerous accidental behaviour is impossible: `POST /locations` on a
  retired code gets a clear `409`, so a caller who genuinely wants the slot
  back has to notice and ask a human.
- No decision debt about re-validation semantics, event choice or capacity
  retention — none of those questions arise.

### Harder

- **A mistaken decommission is not undoable through the API.** Recovery means
  either a database intervention or registering the slot under a different
  code. This is the real cost, accepted because the alternative — a
  reactivation path that is used once a year and tested never — is worse.
- **A code is permanently consumed.** If a rack really is rebuilt in the same
  physical position, it needs a new code, and the physical label has to
  change with it. This is a deliberate trade: physical/digital identity
  agreement matters more than code tidiness.
- `UnderMaintenance` is currently write-only from the outside — reachable by
  data load but not by any endpoint. That is an asymmetry a reader will
  notice, and it is documented rather than hidden.
- Bulk re-import of a building that includes previously-decommissioned codes
  will report those rows as duplicates. Correct, but it means a re-import is
  not idempotent against a map that has had retirements, and the operator has
  to read the report rather than assume success.
