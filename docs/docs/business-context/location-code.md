---
id: location-code
title: The location code
sidebar_label: The location code
description: Site-Area-Zone-Aisle-Bay-Level-Position — the industry-standard coded address, why it is a value object, and how it is validated.
---

# The location code

The coded address of a slot is the single most consequential design decision
in this service. It is not a made-up scheme: it is the widely-used WMS
industry pattern — **Site → Area → Zone → Aisle → Bay → Level → Position** —
hyphen-joined and human-parsable.

```
WH1-STOR-AMB-A07-03-02-B
 |    |    |   |   |  |  `-- Position: left-to-right slot on the level
 |    |    |   |   |  `----- Level:    vertical level / shelf
 |    |    |   |   `-------- Bay:      bay / section along the aisle
 |    |    |   `------------ Aisle:    physical corridor
 |    |    `---------------- Zone:     behavioral class (AMB/CHL/FRZ/HAZ/FWD/RSV/CPK)
 |    `--------------------- Area:     coarse functional area (STOR/RCV/PACK/STAGE)
 `-------------------------- Site:     the physical facility
```

Segments read left to right, coarsest to finest.

| Segment | Meaning | Example |
|---|---|---|
| Site | the physical facility/building | `WH1` |
| Area | coarse functional area | `STOR` (storage), `RCV` (receiving), `PACK`, `STAGE` |
| Zone | behavioral class *within* an area — drives rules | `AMB` (ambient), `CHL` (chilled), `FRZ` (frozen), `HAZ` (hazmat), `FWD` (forward-pick), `RSV` (reserve), `CPK` (case-pick faces; a convention on this free-form class, [ADR 0042](../adr/0042-case-pick-zone-class-convention.md)) |
| Aisle | physical corridor | `A07` |
| Bay | a bay/section along the aisle | `03` |
| Level | vertical level/shelf | `02` |
| Position | left-to-right slot on that level | `B` |

## It is a value object, not a string

`LocationCode` is built from seven typed segments and always round-trips
through `String()` / `ParseLocationCode()`. Construction is rejected if any
segment is empty or contains a character other than `[A-Z0-9]`.

```go
// LocationCode is the industry-standard, human-parsable coded address of a
// physical slot, built from seven typed segments and rendered
// hyphen-joined, coarsest to finest.
type LocationCode struct {
	site     string
	area     string
	zone     string
	aisle    string
	bay      string
	level    string
	position string
}

func NewLocationCode(site, area, zone, aisle, bay, level, position string) (LocationCode, error)
func ParseLocationCode(value string) (LocationCode, error)
```

The fields are unexported. There is no way to hold a `LocationCode` that is
not valid, and no way to build one by string concatenation somewhere in an
adapter. Every entry point — the HTTP handler, the bulk importer, the
Postgres rehydration path — goes through the same constructor.

### Validation rules

| Rule | Error |
|---|---|
| Exactly seven hyphen-joined segments | `location code must have exactly 7 hyphen-joined segments (site-area-zone-aisle-bay-level-position)` |
| No segment may be empty | `location code segment must not be empty` |
| Every character must be `[A-Z0-9]` | `location code segment must contain only uppercase letters and digits` |

Errors name the offending segment, which matters a great deal during a bulk
import of a real building's export:

```
location code segment must contain only uppercase letters and digits: position segment "b"
```

Lowercase is rejected rather than normalised. Silently upcasing would mean
two spellings of the same physical slot could both be "accepted" by different
callers, and the code is an *identity* — identity comparison must be exact.

## Derived identifiers

Because the hierarchy is inside the code, the parent identifiers fall out of
it for free, with no lookup:

| Derived | From segments | Example |
|---|---|---|
| `Site()` | Site | `WH1` |
| `ZoneID()` | Site-Area-Zone | `WH1-STOR-AMB` |
| `AisleID()` | Site-Area-Zone-Aisle | `WH1-STOR-AMB-A07` |

This is what makes the chain-of-custody check on
[slot registration](../ddd/invariants.md) cheap: the code itself tells the use
case which Site, Zone and Aisle must exist and be `Active`. Nothing has to be
passed alongside it, and nothing can disagree with it.

It is also why the two read models can be assembled without a join table:
`GET /zones/{zoneId}/grid` groups by `(aisle, bay)` and `level` straight out
of the code's own segments.

## Why not a UUID or a free-text bin string

The alternatives were considered and rejected. The full reasoning, with
consequences, is recorded in
[ADR 0002 — hierarchical location code over a flat identifier](../adr/0002-hierarchical-location-code.md).
In brief:

| Option | Why not |
|---|---|
| **Flat UUID** | Requires a lookup for every question that the hierarchy answers for free (which zone? which aisle? what walk order?). Not speakable over a radio, not readable on a label, not sortable into a pick path. |
| **Free-text bin string** | No validation surface, no derived hierarchy, and inevitable drift between the label on the rack and the string in the database. |
| **Composite key of separate columns, no code** | Works in SQL, but every API, every log line and every operator conversation then needs seven fields instead of one token. |

The one real cost — codes change when the building is re-organised — is
accepted, because re-slotting is a rare, planned, physically-signposted
event, while reading and reasoning about locations happens constantly.

## Persistence

The database stores **both** representations: the full code string as the
primary identity, and each of the seven segments as its own column. That is
deliberate — it lets `GetZoneGrid` filter and order by aisle, bay and level
directly in SQL instead of parsing strings, while keeping the canonical
identity a single value.
