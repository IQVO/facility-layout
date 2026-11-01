---
id: 0006-partial-success-bulk-import
title: 0006 — Bulk import reports partial success per row
sidebar_label: 0006 · Partial-success import
description: A 500-row layout import with 3 bad rows creates the other 497 and says exactly which 3 failed and why.
---

# 0006 — Bulk import reports partial success per row

**Status:** Accepted

## Context

A real warehouse has thousands of coded slots. Nobody is going to create them
one `POST /locations` at a time, so this service needs a bootstrap mechanism:
load a whole building's layout from a CSV/JSON export, reproducibly, in one
call.

The design question is what happens when some rows are bad — and in a real
export, **some rows are always bad**. Exports carry stale zones, lowercase
position codes, typo'd aisle numbers, and slot kinds that a placement rule
forbids in the zone the spreadsheet claims.

The forces:

- **Bad rows are the normal case, not an exception.** A mechanism whose
  happy path requires a perfect file will never see its happy path.
- **All-or-nothing turns a 3-row problem into a 500-row problem.** Rejecting
  the entire import means fixing three lines, re-uploading everything, and
  discovering the fourth bad line on the next attempt. Iteration cost scales
  with file size instead of error count.
- **A retry after an all-or-nothing failure is not obviously safe.** Did
  anything commit? Is a partial state sitting there? The operator has to
  reason about it.
- **Per-row validation must not be weakened.** Whatever is created must pass
  the same [chain-of-custody check and PlacementRule evaluation](../ddd/invariants.md)
  as a single `POST /locations`. Bulk is not a bypass.
- **"3 rows failed" is not actionable.** The operator needs the row index, the
  code, and the specific reason — ideally naming the rule.

## Decision

We will make `POST /location-imports` **atomic per row, with a partial-success
report** — never all-or-nothing.

- Every row is processed independently and validated **in full**, using the
  same domain path as single registration.
- A failing row does not abort the import and does not roll back the rows
  before it.
- The response is a report: `rowsSubmitted`, `slotsImported`, `rowsRejected`,
  and a `results` array with **one entry per submitted row** — successes
  included — carrying the caller's own array `index`, the resolved
  `locationCode`, a `succeeded` boolean, and the exact `error` when it failed.
- Structure is created on first sight: a row naming a site, zone or aisle
  that does not yet exist creates it, so an export can be handed over without
  a preparation pass.
- The endpoint answers **`200`, not `201`**. A partial-success report over
  many rows is not the creation of one addressable resource, and there is no
  single `Location` to return. `201` would be a lie whenever any row failed.
- An **empty array is rejected** with `400 empty-import` rather than answered
  with a zero-row report — an empty import is almost always a broken export
  pipeline, not an intentional no-op.
- Events follow the same logic: one `LocationSlotRegistered` per *successful*
  row, plus exactly one `FacilityLayoutImported` summary carrying the three
  counts.

Real output, from a three-row import with one good row, one placement-rule
violation and one malformed code:

```json
{
    "rowsSubmitted": 3,
    "slotsImported": 1,
    "rowsRejected": 2,
    "results": [
        { "index": 0, "locationCode": "WH1-STOR-AMB-A08-01-01-A", "succeeded": true },
        {
            "index": 1,
            "locationCode": "WH1-STOR-FRZ-A02-01-01-A",
            "succeeded": false,
            "error": "location type violates a placement rule for this zone: PalletRack is denied in zone WH1-STOR-FRZ by rule [RULE-FRZ-NO-SHELF: Deny PalletRack where temperatureClass=Frozen]"
        },
        {
            "index": 2,
            "locationCode": "",
            "succeeded": false,
            "error": "location code segment must contain only uppercase letters and digits: position segment \"b\""
        }
    ]
}
```

Row 2's `locationCode` is empty because the `LocationCode` value object
refused to construct at all — there is no code to echo back. That is
information, not an omission.

## Consequences

### Easier

- A 500-row export with 3 bad rows creates the other 497 and names the 3.
  Iteration cost scales with the number of errors, not the size of the file.
- The report is directly mappable back to the source spreadsheet by `index`.
- Errors are specific enough to act on without opening a log: the violated
  rule is named, the offending segment is quoted.
- Validation is not weakened anywhere. Every imported slot went through the
  identical domain path as a hand-registered one, so the "every Active slot
  is legal by construction" property from
  [ADR 0003](./0003-placement-rules-at-registration-time.md) still holds.
- Including successes in `results` means the report is a complete record of
  the call, not just a complaint list.

### Harder

- **The import is not transactional.** A failure partway through leaves the
  earlier rows committed. Callers must read the report; they cannot infer
  outcome from the status code alone. This is the deliberate trade, and it is
  why the response is `200` with a body rather than a bare `201`.
- **Re-running an import is not idempotent.** Rows that succeeded the first
  time come back as `duplicate-location-code` on the second run. Correct
  behaviour, but the operator has to distinguish "already there" from "newly
  failed" in the report — see also
  [ADR 0005](./0005-one-way-decommission.md), since previously-decommissioned
  codes report the same way.
- Structure created on first sight means a typo'd zone code silently creates
  a *new zone* rather than failing. The chain-of-custody check cannot catch
  this, because from the service's point of view the row is internally
  consistent. Mitigation is the report's `slotsImported` count and
  `GET /sites/{siteCode}/layout` for a post-import eyeball.
- The response grows linearly with the request. A 10,000-row import returns a
  10,000-entry report. Acceptable at bootstrap volumes; it would need
  paging or a filter if imports ever became a routine high-frequency
  operation.
- The rule set is loaded per row rather than cached per zone across the
  import — repeated work that is fine at these volumes and is the obvious
  first optimisation if it ever stops being fine.
