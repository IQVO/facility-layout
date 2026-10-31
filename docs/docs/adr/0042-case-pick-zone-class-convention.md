---
id: 0042-case-pick-zone-class-convention
title: "42. The case-pick zone is a convention on the free-form zone class"
sidebar_label: 42. Case-pick zone class (CPK)
sidebar_position: 42
description: "ADR 0042 — a case-pick zone is the zone class CPK, a fleet convention on the existing free-form zoneCode, not a new type: no schema change, no event change, and facility-layout validates nothing."
---

# 42. The case-pick zone is a convention on the free-form zone class

## Status

**Accepted** (2026-10-25). Documentation only; no behaviour changes. Relates
to [ADR 0002](./0002-hierarchical-location-code.md) (the zone segment of the
location code) and [ADR 0040](./0040-slot-handling-unit.md) (a slot's
`handlingUnit`, a different, per-slot signal).

## Context

slotting-optimization (its ADR 0012, case-pick faces) plans SKUs that are
picked in whole cases onto faces whose zone is in its own
`CASE_PICK_ZONE_CODES` list; its default is empty, meaning no case-pick faces.
The fleet convention for naming such a zone is now the class `CPK`, next to
`FWD` (each-pick forward), `BLK` (bulk) and `RSV` (reserve).

A zone's behavioural class is the free-form `zoneCode` string
(`^[A-Z0-9]+$`; the API documents `AMB, CHL, FRZ, HAZ, FWD, RSV` only as
examples). It is carried as `zoneCode` on `ZoneRegistered` and is the zone
segment of every `LocationCode`.

## Decision

1. **`CPK` is a convention, not a type.** A case-pick zone is a zone whose
   `zoneCode` is `CPK` (an area may also use another code and be listed in the
   consumer's configuration). There is no new enum, field, aggregate state or
   endpoint.
2. **No schema change and no event change.** `RegisterZoneRequest`,
   `ZoneRegistered` and the location-code format are untouched.
   slotting-optimization reads the class from the `zoneCode` it already
   receives with the layout facts it consumes; what to do with it stays in its
   own configuration.
3. **Nothing validates it.** facility-layout accepts any `[A-Z0-9]+` code and
   does not check it against a list, does not require `CPK` zones to hold
   case-sized slots, and does not reject a `CPK` zone with each-pick slots.
   Whether a zone is a case-pick zone is decided by the consumer that plans
   on it.
4. **Documented, not enforced.** `apis/openapi.yaml` (both zone descriptions
   and a `registerZone` example), the ubiquitous language, the DDD aggregate
   page and the zone lists now name `CPK`.

## Consequences

**Positive**

- Nothing to migrate or deploy: no contract version, no consumer change.
- Matches how every other class (`AMB`, `FWD`, `RSV`) already works.

**Negative / accepted**

- A typo (`CKP`) is a valid zone and silently not case-pick. Detection is the
  consumer's job (an unmatched list entry or an unexpected each-pick plan).
- The convention lives in prose; it can drift across repositories. A future
  decision may add validation or a typed attribute if drift becomes real.

## Alternatives considered

- **A new `ZoneKind` enum or `pickUnit` field on Zone.** An API, aggregate and
  event change (and a Published Language version) for a naming convention.
  Rejected for now.
- **Use the per-slot `handlingUnit` (ADR 0040).** It describes one slot, not
  a zone, and is not what slotting's zone list reads.
