---
id: 0025-optimistic-concurrency-version-column
title: 25. Optimistic concurrency (version column) on LocationSlot
sidebar_label: 25. Optimistic concurrency
sidebar_position: 25
description: "ADR 0025 — a version column on location_slots closes the blind-overwrite lost-update race between SetLocationGeometry and DecommissionLocationSlot (worst case: a geometry write resurrecting status=Active over a decommission). Sites, zones and aisles are deliberately not versioned."
---

# 25. Optimistic concurrency (version column) on LocationSlot

## Status

**Accepted.** Implemented in the same change that introduced this record.
Mirrors the fleet's reference implementations — workforce-management
ADR-0021 (`associate_shift`, `labor_assignment`) and order-management
ADR-0024 — adapted to this service's aggregates, with the same honest
per-aggregate assessment of which rows actually race.

Supersedes the "optimistic concurrency" hand-wave in
[ADR-0019](./0019-idempotency-key-middleware.md)'s decommission exclusion
rationale (which pointed at a mechanism that did not exist here) and backs
[ADR-0005](./0005-one-way-decommission.md)'s one-way decommission with an
actual guard: a decommission can no longer be silently undone by a
concurrent geometry write.

## Context

Before this change, `SlotRepo.Save` was a blind full-column upsert:

```go
// SlotRepo.Save, before
INSERT INTO location_slots (...)
VALUES (...)
ON CONFLICT (code) DO UPDATE SET
    status = EXCLUDED.status,
    x_m = EXCLUDED.x_m, ...
```

Every mutating use case follows a read-modify-write shape:
`FindByCode`, mutate the in-memory aggregate, `Save`. Two callers that
both load the same row, each apply their own mutation, and Save one after
the other will have the second Save silently overwrite EVERY column the
first Save touched — not just the field the second caller intended to
change. Neither caller is told anything went wrong.

The audit's concrete instance: `SetLocationGeometry` and
`DecommissionLocationSlot` both read-modify-write the same slot row with
no ordering guarantee between them. A geometry write in flight when a
decommission commits writes `status=Active` (from its stale copy) back
over the decommission — the slot silently returns to service, and
ADR-0005's "one-way decommission" is violated not by a decision but by a
race.

### Where this is a genuine risk in this domain

The mutable aggregates were assessed against how this service's own use
cases actually call them, not in the abstract:

- **`LocationSlot`** (`location_slots` table). `SetLocationGeometry`,
  `SetPickSequence` (via the same use case) and
  `DecommissionLocationSlot` all read-modify-write the SAME row for the
  SAME code, and each writes a DIFFERENT subset of columns (geometry
  columns vs `status`). These are independent HTTP calls with no ordering
  guarantee — an operator's console geometry edit racing a maintenance
  decommission is exactly the audit's scenario, not a contrived one.
  **Protected.**
- **`Site`** (`sites`). `RegisterSite` is the only write path; there is no
  site update use case. **Read-mostly, not protected.**
- **`Zone`** (`zones`). `RegisterZone` creates; the only subsequent write
  is `SetPitch` (ADR-0017), a targeted two-column UPDATE
  (`bay_pitch_m`/`level_pitch_m`) with no other writer racing it — no
  stale-field hazard exists for columns nobody else writes. **Not
  protected.**
- **`Aisle`** (`aisles`). `SetAisleGeometry` is the aisle's only mutator
  after registration; a single-writer column set cannot clobber itself.
  **Not protected.**

## Decision

**Add a `version` column to `location_slots` only.** `sites`, `zones`,
`aisles`, and every other table are untouched.

1. **Domain**: `LocationSlot` gains an unexported `version int` field and
   a `Version() int` accessor — inert infrastructure metadata, exactly
   like the aggregate's own code, never read by domain business logic.
   `NewLocationSlot` (a fresh aggregate) starts at `version: 1`;
   `RehydrateLocationSlot` gains a `version` parameter, populated by the
   adapter from the row it read.

2. **Migration** (`migrations/0007_slot_version.up.sql` / `.down.sql`):

   ```sql
   ALTER TABLE location_slots ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
   ```

   Existing rows default to 1, which is exactly what a fresh aggregate's
   first Save would have written.

3. **Repo `Save`**: a single version-guarded `INSERT ... ON CONFLICT
   DO UPDATE ... WHERE location_slots.version = $loaded`, checking
   `RowsAffected()` — the same single-statement form workforce-management
   ADR-0021 verified against a real Postgres (a fresh INSERT always
   reports 1; a WHERE-mismatched DO UPDATE correctly reports 0):

   ```go
   tag, err := querierFrom(ctx, r.pool).Exec(ctx, `
       INSERT INTO location_slots (..., version) VALUES (..., 1)
       ON CONFLICT (code) DO UPDATE SET
           ..., version = location_slots.version + 1
       WHERE location_slots.version = $n
   `, ..., s.Version())
   if tag.RowsAffected() == 0 {
       return ports.ErrConcurrentModification
   }
   ```

4. **`ports.ErrConcurrentModification`** (new sentinel in
   `internal/application/ports`, alongside the repository interfaces) is
   returned by `SlotRepo.Save` on a version mismatch against an existing
   row.

5. **HTTP mapping**: the `errorCategories` table in
   `internal/adapters/inbound/http/errors.go` maps it to `409 Conflict`
   with its own `concurrent-modification` RFC 7807 category, distinct
   from every other 409 (`duplicate-*`, `*-not-active`,
   `already-decommissioned`) so a caller can tell "re-fetch and retry"
   apart from "this request was rejected on its merits".

6. **Use cases are unchanged.** Every mutating use case already does
   `FindByCode` → mutate → `Save` inside `atomically`; the version flows
   through transparently because `RehydrateLocationSlot` populates it
   from what was read and `Save` reads it back off the aggregate. The
   creation use cases (`RegisterLocationSlot`, `ImportFacilityLayout`)
   are unaffected: they Save freshly-constructed aggregates (version 1)
   against codes the duplicate check has already proved absent.

## Consequences

**Positive**

- The lost-update race the audit named — a concurrent
  `SetLocationGeometry` writing `status=Active` back over a decommission
  — is closed, and proven closed by a real two-goroutine concurrent test
  against testcontainers Postgres, not just a sequential simulation.
- The guard is per-aggregate honest: no version column was bolted onto
  read-mostly aggregates where it would only add friction.
- Single-writer flows are completely unaffected; the entire pre-existing
  test suite passes unmodified.

**Negative / accepted**

- Callers receiving `409 concurrent-modification` must re-fetch and
  retry; this repo implements no automatic retry (the fleet's pattern).
  Retries of the SAME request are the idempotency-key middleware's job
  (ADR-0019) — a different concern from lost updates between DIFFERENT
  requests.
- One more column and one more migration to carry.
- If a future use case makes zones or aisles genuinely multi-writer
  (e.g. an aisle-direction update racing a geometry update), the same
  pattern must be extended there — this ADR's assessment is a snapshot,
  not a permanent guarantee.

## Verification

- Domain: `TestRehydrateLocationSlot` asserts the loaded version is
  preserved.
- Integration (`-tags=integration`, testcontainers Postgres,
  `internal/adapters/outbound/postgres/version_integration_test.go`):
  - `TestSlotRepo_Save_StaleVersionFails` — two sequential loads, first
    Save (decommission) succeeds, stale second Save (geometry) returns
    `ErrConcurrentModification` and its change is verified absent on
    reload: the decommission survives.
  - `TestSlotRepo_Save_CurrentVersionSucceedsAndIncrements` — a Save at
    the current version succeeds; version advances by exactly one.
  - `TestSlotRepo_ConcurrentSaves_ExactlyOneWinner` — two goroutines
    load, mutate independently, and Save concurrently: exactly one
    succeeds, version advances by exactly one, exactly one mutation is
    visible on reload.

## Related

- [ADR-0005](./0005-one-way-decommission.md) — the one-way decommission
  whose invariant this guard enforces under concurrency.
- [ADR-0019](./0019-idempotency-key-middleware.md) — same-request
  retries (idempotency), complementary to this ADR's cross-request lost
  updates.
- workforce-management ADR-0021 — the fleet reference this
  implementation follows.
