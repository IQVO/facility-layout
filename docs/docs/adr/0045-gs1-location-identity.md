---
id: 0045-gs1-location-identity
title: "45. GS1 location identity (gln) and yard spot kind (spotKind)"
sidebar_label: 45. GS1 location identity
sidebar_position: 45
description: "ADR 0045 — Site, Zone and LocationSlot gain an OPTIONAL GS1 Global Location Number (gln, 13 digits, mod-10 check digit, unique across every location) and a yard slot gains an OPTIONAL spotKind (PARKING|HOLDING|REEFER_PLUG|HAZMAT|STAGING_LANE); set at registration, carried additively on SiteRegistered, ZoneRegistered and LocationSlotRegistered, no new event type."
---

# 45. GS1 location identity (`gln`) and yard spot kind (`spotKind`)

## Status

**Accepted** (2026-11-10). Additive: no existing event, route, column or
payload changes meaning, and every new field is optional. Reuses the Published
Language rules of [ADR 0013](./0013-first-published-language-consumer.md) and
the CloudEvents envelope of [ADR 0024](./0024-cloudevents-mandatory-envelope.md).
It follows the additive-field pattern of
[ADR 0040](./0040-slot-handling-unit.md) and
[ADR 0037](./0037-site-role-and-siterolechanged-cloudevents.md).

## Context

Trading partners and the GS1 standards speak **location identity** as a Global
Location Number (GLN). This context is the system of record for where things
physically are, but a Site, a Zone or a slot (a dock door, a yard spot) could
only be named by this service's own codes, so every partner integration needed a
translation table. Separately, the yard keeps its holding spots in its own
policy and has no notion of what a spot is for; the location model already has a
`Yard` role ([ADR 0016](./0016-functional-location-roles.md)) but no kind of yard
spot.

## Decision

1. **Model.** `Site`, `Zone` and `LocationSlot` carry an optional `gln`
   (`shared.GLN`). The zero value means "none recorded" and is exactly today's
   behaviour; every location registered before this ADR has none. A
   `LocationSlot` also carries an optional `spotKind` (`shared.SpotKind`):
   `PARKING`, `HOLDING`, `REEFER_PLUG`, `HAZMAT`, `STAGING_LANE`. `HAZMAT` is new
   relative to the four kinds of the earlier model.
2. **The GS1 rule.** A GLN is exactly 13 ASCII digits whose last digit is the GS1
   mod-10 check digit of the first twelve: from the right, over those twelve
   digits, multiply alternately by 3 and 1 (the rightmost by 3), add them up, and
   the check digit is the amount that rounds the sum up to the next multiple of
   ten (`(10 - sum mod 10) mod 10`). Example: `4006381333931` (weighted sum 89,
   check digit 1). The optional GLN extension component is not modelled. The rule
   lives in the domain (`shared.ParseGLN`); the database only guards the shape
   (13 digits).
3. **Uniqueness.** A GLN identifies exactly one location. It is unique across
   sites, zones and slots of the service, which is stricter than "per site tree"
   and matches what a GLN means: the same number on two locations is a data error
   whichever tree they sit in. One aggregate cannot see its siblings and the
   three kinds live in three tables, so the claim goes through a small port,
   `ports.GLNRegistry`, backed by one table `location_glns (gln PRIMARY KEY,
   owner UNIQUE)`. The claim is made inside the same unit of work as the
   aggregate save and the outbox insert, so a refused registration leaves
   nothing behind and two concurrent claims of one number cannot both commit
   (the second waits on the primary key, then sees the conflict). A location
   without a GLN never touches the registry.
4. **Spot kind is Yard-only.** The repo tells a yard slot from a dock slot by the
   `LocationRole` of its LocationType (a Zone has no kind). `spotKind` is legal
   only when that role is `Yard`; on any other role (a dock door, a storage bin)
   the registration is refused. The rule is the aggregate's
   (`LocationSlot.AssignSpotKind`) and a database CHECK repeats it
   (`spot_kind IS NULL OR role = 'Yard'`).
5. **REST** (OpenAPI first, `apis/openapi.yaml`). `POST /sites`,
   `POST /sites/{siteCode}/zones` and `POST /locations` accept an optional `gln`;
   `POST /locations` also accepts an optional `spotKind`. Every site, zone and
   slot representation (single reads, lists, `GET /sites/{siteCode}/layout`,
   `GET /sites/{siteCode}/locations?role=`) includes `gln` and `spotKind` when
   set and omits them otherwise, so a response for an old location is
   byte-identical to before. Problems (RFC 7807, [ADR 0004](./0004-rfc-7807-from-day-one.md)):

   | Slug | Status | When |
   |---|---|---|
   | `invalid-gln` | 422 | not 13 digits, or the check digit is wrong |
   | `gln-already-assigned` | 409 | another site, zone or slot already holds it |
   | `unknown-spot-kind` | 422 | not one of the five kinds |
   | `spot-kind-not-allowed` | 422 | `spotKind` on a slot whose role is not `Yard` |

   An old request without the fields behaves exactly as before.
6. **Events** (AsyncAPI first, `apis/asyncapi.yaml`). The existing
   `SiteRegistered`, `ZoneRegistered` and `LocationSlotRegistered` events gain
   `gln` (and `LocationSlotRegistered` gains `spotKind`). Both are **omitted when
   none was recorded**, so the payload of every existing location is
   byte-identical to before; the AsyncAPI schemas declare them nullable
   (`type: [string, 'null']`) and optional, never required, so a consumer may
   also tolerate an explicit `null`. No new event type and no `.v2`: an
   additive optional field is not a breaking change under
   [ADR 0024](./0024-cloudevents-mandatory-envelope.md). The AsyncAPI carries
   examples copied byte-for-byte from the wire goldens, and an architecture test
   (`TestIdentityExamplesMatchTheWireGoldens`) fails when they drift, so a
   consumer's contract test can use them. The analytics twin
   (`warehouse.facility.analytics`) encodes through the same `WireData` mapping:
   its goldens differ from the integration ones only in `dataschema`, and the
   projector reads only the fields it needs, so it ignores the new ones.
7. **Reads beyond REST.** The MCP read tools return the fields too
   (`list_sites`, `get_site_layout` for the site and each zone,
   `list_functional_locations` for each location), as does the A2A
   `site_layout` skill. Both omit them when none was recorded.
8. **Storage.** Migration `0013`: nullable `sites.gln`, `zones.gln`,
   `location_slots.gln` and `location_slots.spot_kind` with shape CHECKs, plus
   `location_glns`.
9. **Change after registration.** Both fields are set at registration. This
   version adds **no update route**: `gln` and `spotKind` are immutable once
   registered. The repo does have slot update routes (`PUT
   /locations/{code}/geometry`, `PUT /locations/{code}/handling-unit`), and the
   pattern for a change is a `PUT` plus a dedicated event
   (`LocationSlotHandlingUnitDeclared`). Following it would add routes and new
   event types for three aggregates; that is a decision of its own and is left to
   a follow-up. `ImportFacilityLayout` rows do not carry the fields yet either
   (same boundary as ADR 0040).

## Consequences

- Consumers that ignore unknown fields are unaffected. A consumer keeping a copy
  of a location applies the fields from the registered event when present.
- `yard-management` will consume `spotKind` from `LocationSlotRegistered` in a
  later item, replacing the spot kinds it holds in its own policy; nothing in
  this change touches that service.
- Locations that existed before this ADR have no GLN, and with no update route
  they cannot get one until the follow-up above ships (or they are registered
  again under a new code). A deployment that wants GLNs on existing sites must
  wait for that item.
- Uniqueness is global and enforced by a table the three registrations write to,
  so a decommissioned location keeps its GLN claimed (a retired location is never
  resurrected, [ADR 0005](./0005-one-way-decommission.md), and neither is its
  number).
- The check digit is verified by the service, not the database, so a row written
  by hand can hold a well-shaped but wrong GLN.
- The GLN extension component, GLN lookup by number (`GET` by GLN) and a GLN on
  an Aisle are out of scope.
