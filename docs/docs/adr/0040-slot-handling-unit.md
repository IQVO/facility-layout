---
id: 0040-slot-handling-unit
title: "40. A slot declares the handling unit it accepts"
sidebar_label: 40. Slot handling unit
sidebar_position: 40
description: "ADR 0040 — LocationSlot gains an OPTIONAL handlingUnit (EACH|CASE|PALLET): settable at registration and changeable via PUT /locations/{code}/handling-unit, carried additively on LocationSlotRegistered and announced by the new LocationSlotHandlingUnitDeclared event, so inventory-storage can stow a sealed case."
---

# 40. A slot declares the handling unit it accepts

## Status

**Accepted** (2026-10-24). Additive: no existing event, route or column
changes meaning. Reuses the Published Language rules of
[ADR 0013](./0013-first-published-language-consumer.md) and the CloudEvents
envelope of [ADR 0024](./0024-cloudevents-mandatory-envelope.md).

## Context

`inventory-storage` refuses to stow a sealed case into a bin whose handling
unit is not `CASE` or `PALLET` (its ADR 0045 section 4,
`ErrBinHandlingUnitMismatch`, `409 bin-handling-unit-mismatch`). It learns a
bin's handling unit from `LocationSlotRegistered`, but this context had no
such attribute anywhere (model, REST, events), so no bin could ever take a
case. The mastercase rollout plan (section 4.2) assigns "bin attributes:
handlingUnit (EACH|CASE|PALLET)" to this context, the system of record for
what a coded location is legal for.

## Decision

1. **Model.** `LocationSlot` carries an optional `handlingUnit`
   (`shared.HandlingUnit`: `EACH`, `CASE`, `PALLET`). The zero value means
   "none recorded" and is exactly today's behaviour; every slot registered
   before this ADR has none. `DeclareHandlingUnit` enforces the invariants:
   a value outside the enum is `ErrUnknownHandlingUnit` (`422
   unknown-handling-unit`) and a Decommissioned slot refuses the change
   (`ErrSlotDecommissioned`, `409 already-decommissioned`, checked first).
   There is no way to clear a value; declare another one.
2. **REST.** `POST /locations` accepts an optional `handlingUnit`.
   `PUT /locations/{locationCode}/handling-unit` with
   `{"handlingUnit": "CASE"}` sets or changes it on an existing slot and
   returns the slot (`200`; `400`, `404`, `409`, `422` as above). Like the
   geometry PUT it is naturally idempotent and takes no `Idempotency-Key`
   ([ADR 0019](./0019-idempotency-key-middleware.md) covers creations only).
   `GET /locations/{locationCode}` and the other slot reads include
   `handlingUnit` when set and omit it otherwise.
3. **Events** (camelCase, matching `LocationSlotRegistered`, not the
   snake_case of `SiteCapabilityChanged`):
   - `LocationSlotRegistered` gains `handlingUnit` (string enum), **omitted
     when none was recorded**, so the payload of every existing slot is
     byte-identical to before.
   - New `com.warehouse.wms.facility-layout.locationslot.LocationSlotHandlingUnitDeclared`
     (`dataschema` `urn:warehouse:facility-layout:events:LocationSlotHandlingUnitDeclared:v1`)
     with `data`: `locationCode` (string, the same identifier as in
     `LocationSlotRegistered`), `handlingUnit` (string enum), `declaredAt`
     (RFC 3339 instant, equal to `occurredAt`), plus the usual
     `eventName`/`eventType`/`occurredAt`. Raised through the outbox on
     every declaration, keyed and `subject`-ed by `locationCode` so it lands
     on the slot's partition after its `LocationSlotRegistered`.
4. **Storage.** Migration `0011`: nullable `location_slots.handling_unit`
   with `CHECK (handling_unit IS NULL OR handling_unit IN
   ('EACH','CASE','PALLET'))`.

## Consequences

- Consumers that ignore unknown fields/types are unaffected. A consumer
  keeping a per-bin copy applies last-write-wins per `locationCode`:
  `handlingUnit` from `LocationSlotRegistered` when present, and every
  `LocationSlotHandlingUnitDeclared` thereafter. `inventory-storage` does
  this in its own change (it amends its ADR 0045 section 4).
- Slots that existed before this ADR stay without a handling unit until
  someone declares one, so a deployment must declare the case/pallet bins
  (one `PUT` each) before sealed cases can be stowed in them.
- Not in this change: bulk import rows do not carry `handlingUnit` yet, and
  the reserve-versus-forward-pick bin role of plan section 4.2 is a separate
  attribute left to its own decision.
