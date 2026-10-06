---
id: bulk-import
title: Bulk import
sidebar_label: Bulk import
description: POST /locations/import — bootstrapping a real building's layout, with atomic per-row validation and a partial-success report.
---

# Bulk import

`POST /locations/import` is the bootstrap mechanism: it loads a real
building's layout from a CSV/JSON export in one call, reproducibly, instead
of somebody typing in slots one at a time.

The whole point of the endpoint is that a warehouse gets loaded **once**,
from a file, and the report tells you exactly what did and did not land.

## Request shape

A JSON array of rows. Each row fully specifies its own site, area, zone,
aisle, bay, level, position and location type — the structure it needs is
created on first sight, so the array can be handed straight over from an
export without a preparation pass.

| Field | Required | Notes |
|---|---|---|
| `siteCode` | yes | Created if not already present |
| `siteName` | on first sight of a site | Used only when the site is created |
| `areaCode`, `zoneCode` | yes | Together form the zone id `Site-Area-Zone` |
| `temperatureClass` | on first sight of a zone | `Ambient` \| `Chilled` \| `Frozen` |
| `hazmat` | on first sight of a zone | boolean |
| `aisleCode` | yes | |
| `sequenceHint` | on first sight of an aisle | walk-order position |
| `direction` | on first sight of an aisle | `OneWay` \| `TwoWay` |
| `bay`, `level`, `position` | yes | the final three LocationCode segments |
| `locationType` | yes | must already be registered |

## Atomic per row, never all-or-nothing

Every row is processed independently and validated in full — the same
[chain-of-custody check and PlacementRule evaluation](../ddd/invariants.md)
that `POST /locations` performs. A failing row does not abort the import and
does not roll back the rows before it.

> A 500-row export with 3 bad rows still creates the other 497 and tells you
> exactly which 3 and why.

The reasoning is recorded in
[ADR 0006](../adr/0006-partial-success-bulk-import.md).

## A real worked import

Three rows: one good, one that violates a placement rule, one with a
malformed location code.

```bash
curl -X POST localhost:8080/locations/import -H 'Content-Type: application/json' -d '[
 {"siteCode":"WH1","siteName":"Fulfilment Centre One","areaCode":"STOR","zoneCode":"AMB","temperatureClass":"Ambient","hazmat":false,"aisleCode":"A08","sequenceHint":8,"direction":"TwoWay","bay":"01","level":"01","position":"A","locationType":"PalletRack"},
 {"siteCode":"WH1","areaCode":"STOR","zoneCode":"FRZ","temperatureClass":"Frozen","hazmat":false,"aisleCode":"A02","sequenceHint":2,"direction":"OneWay","bay":"01","level":"01","position":"A","locationType":"PalletRack"},
 {"siteCode":"WH1","areaCode":"STOR","zoneCode":"AMB","temperatureClass":"Ambient","hazmat":false,"aisleCode":"A08","sequenceHint":8,"direction":"TwoWay","bay":"01","level":"01","position":"b","locationType":"PalletRack"}
]'
```

Real response from a running instance:

```json
{
    "rowsSubmitted": 3,
    "slotsImported": 1,
    "rowsRejected": 2,
    "results": [
        {
            "index": 0,
            "locationCode": "WH1-STOR-AMB-A08-01-01-A",
            "succeeded": true
        },
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

Reading the report:

- **Row 0 succeeded.** Aisle `A08` did not exist before this call; the import
  created it (with `sequenceHint: 8` and `TwoWay`) and then the slot.
- **Row 1 was rejected by a domain rule.** This is the
  "ambient product in the frozen zone" guard doing its job — at registration
  time, naming the exact rule (`RULE-FRZ-NO-SHELF`) so an operator can go and
  look at it. The frozen zone and aisle `A02` still exist; only the illegal
  slot was refused.
- **Row 2 was rejected before the domain ever saw it.** The `LocationCode`
  value object refused the lowercase `"b"` position segment, which is why
  `locationCode` comes back empty — the code was never successfully
  constructed, so there is nothing to echo.

`index` is the caller's own array index, so a failing row can be mapped
straight back to a line in the source spreadsheet.

## Why `200` and not `201`

A bulk import is a **partial-success report over many rows**, not the
creation of one addressable resource. There is no single `Location` header
that could describe it, and `201` would be a lie whenever some rows failed.
`200` with a report body is the honest answer.

The only cases that produce an error status instead are the ones where the
request itself is unusable:

| Situation | Response |
|---|---|
| Body is not a JSON array | `400` `application/problem+json` |
| Array is empty | `400` `empty-import` — *"Facility layout import must contain at least one row"* |

An empty array is rejected rather than answered with a zero-row report,
because it is almost always a broken export pipeline rather than an
intentional no-op.

## Events

Per row, the import publishes the registration events for whatever it
creates: `SiteRegistered` / `ZoneRegistered` / `AisleRegistered` for a parent
seen for the first time, one `LocationSlotRegistered` per **successful**
slot, and `LocationGeometryUpdated` when the row carries geometry. After the
last row it publishes exactly one `FacilityLayoutImported` summary event
carrying `rowsSubmitted` / `slotsImported` / `rowsRejected`. The summary exists so a
consumer can tell "the building was loaded" apart from a burst of unrelated
single registrations. See [Domain events](../ddd/domain-events.md).
