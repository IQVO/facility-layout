---
id: 0011-micro-frontend-console-adoption
title: 11. Adoption of the fleet-wide micro-frontend console architecture
sidebar_label: 11. Micro-frontend console adoption
sidebar_position: 11
description: "facility-layout's adoption of the cross-fleet micro-frontend console architecture decided in warehouse-ops-agent ADR-0002: a facility-mfe Module Federation remote owned in this repo, additive CORS on the existing REST API, and an explicit non-membership in the BFF-backed Order Lifecycle screen."
---

# 11. Adoption of the fleet-wide micro-frontend console architecture

## Status

**Accepted.**

## Context

The warehouse-systems fleet decided, in
[`warehouse-ops-agent` ADR-0002](https://github.com/claudioed/warehouse-ops-agent/blob/docs/adr-mfe-architecture/docs/docs/adr/0002-micro-frontend-console-architecture.md)
("Micro-frontend console architecture over per-service REST, with a thin BFF
for cross-service reads"), to give every bounded context in the fleet —
`order-management`, `inventory-storage`, `wes-work-planning`,
`fulfillment-execution`, `workforce-management`, and `facility-layout` — its
own Module Federation **remote**, owned and released inside that context's own
repo, composed at runtime by a separate `warehouse-console` shell and styled
through a shared `@warehouse/ui-kit` design system. That record is the fleet's
single architecturally-significant decision; this record is **not** a second
decision — it is the adoption record for how `facility-layout`, specifically,
took on that already-decided shape, plus what this repo had to add or
deliberately leave out to do so correctly.

The forces specific to this service, as this repo's own generic-subdomain
strategic classification and existing ADRs already establish:

- **This is the fleet's Open Host Service with no inbound dependency, ever**
  (see [Context map](../ecosystem/context-map.md)). Any UI work here must
  preserve that: `facility-mfe` may call this service's own REST API, and
  nothing here may call into another context's remote, BFF, or database.
- **`facility-layout` is not one of the four services the console's Order
  Lifecycle screen fans out to.** ADR-0002's BFF joins
  `order-management` → `inventory-storage` → `wes-work-planning` →
  `fulfillment-execution` by an order reference (with the non-uniform
  `WorkUnit`-id hop documented there). `facility-layout` has no order
  reference and no aggregate in that chain — its role in the console is its
  **own** screen over its **own** data (sites, zones, aisles, slots), exactly
  like `fulfillment-mfe`'s queue-depth dashboard is its own screen, not a BFF
  participant.
- **No prior browser client, no prior CORS.** Like the three services ADR-0002
  did touch, this service's REST API had never needed to be called from a
  browser origin before; the same-origin assumption was implicit everywhere.
- **Unlike the three BFF-touched services, no new GET-by-reference endpoint
  was needed here.** ADR-0002 required `inventory-storage`,
  `wes-work-planning`, and `fulfillment-execution` to each add one minimal
  lookup-by-order-reference endpoint because the BFF needed to join on a key
  those services didn't yet expose a lookup for. `facility-layout`'s own
  screen reads `GET /sites` and `GET /sites/{siteCode}/layout` — both already
  existed for other consumers before this adoption; no domain, port, or REST
  contract change was required to support the new remote.

## Decision

**`facility-layout` adopts the fleet's micro-frontend console architecture
exactly as ADR-0002 specifies for a non-BFF context: a `web/` directory in
this repo houses `facility-mfe`, a Vite + React Module Federation remote that
talks only to this service's own REST API, and this service's HTTP adapter
gained additive CORS middleware. It does not participate in the BFF or the
Order Lifecycle screen, and no existing endpoint, domain type, or use case was
changed to support it.**

### `facility-mfe`: one remote, in this repo, per ADR-0002's pattern

`web/` (Vite + React + `@module-federation/vite`) exposes `./App` as remote
name `facility_mfe`, dev server on port `5186` per the fleet's port map
(`5181`=order, `5182`=inventory, `5183`=planning, `5184`=fulfillment,
`5185`=workforce, `5186`=facility). It shares `react`, `react-dom`,
`react-router-dom`, and `@warehouse/ui-kit` as Module Federation singletons,
matching every sibling remote, and is consumed by `warehouse-console` at
`/facility/*` — the same composition contract ADR-0002 defines for all six.

It is built, tested, and released inside `facility-layout`'s own CI, on this
repo's own schedule, per ADR-0002's "ownership stays aligned with the backend
split" principle: the team that owns this service's domain and REST API also
owns the screen that renders it.

`src/types.ts` hand-mirrors `internal/adapters/inbound/http/dto.go`'s wire
shapes (`Site`, `Zone`, `Aisle`, `LocationSlot`, `SiteLayout`,
`LocationClassification`) rather than generating from the OpenAPI spec —
consistent with this being a thin read-only screen, not a second contract
surface.

### `FacilityScreen`: this context's own operator workflow, not a BFF client

`FacilityScreen.tsx` renders `GET /sites` as a `DataTable` (site code, name,
status pill via `@warehouse/ui-kit`'s `StatusPill`), and — on selecting a
site — fetches `GET /sites/{siteCode}/layout` and renders the nested
zone → aisle → slot tree, each zone showing `TemperatureClass` and `Hazmat`
as status pills. Both endpoints predate this adoption and were not modified;
this is purely a new consumer of an existing, unchanged contract. There is no
Order Lifecycle involvement: this screen answers "what does this facility's
map look like", not "what happened to order X".

### CORS: additive middleware, same shape as the three BFF-touched services

`internal/adapters/inbound/http/server.go` gained a `go-chi/cors` global
middleware, `CORS_ALLOWED_ORIGINS` env var (default
`http://localhost:5173,http://localhost:5186` — the shell's dev origin plus
this remote's own dev port), `GET`/`POST`/`PUT`/`DELETE`, no credentials (the API is unauthenticated —
the static-bearer-key layer was later removed by ADR-0015 — so no cookies
or credentials are ever involved).
Added directly to the existing HTTP adapter, not via a gateway — identical to
ADR-0002's rationale for the other four services, applied here even though
this service sits outside the BFF's fan-out, because `facility-mfe` still
needs a browser-origin call path to this service's own API.

### What was deliberately NOT done

- **No `GET`-by-reference endpoint was added.** ADR-0002's three additive
  endpoints exist to let the console-bff join on an order reference this
  service has no concept of. `facility-layout`'s existing `GET /sites` and
  `GET /sites/{siteCode}/layout` already answered `facility-mfe`'s screen
  need.
- **No participation in `console-bff`'s fan-out**, and no inbound call from
  `warehouse-ops-agent` into this service. This preserves the "no inbound
  dependency, ever" property this repo's own context map treats as its most
  important non-edge.
- **No change to any domain, application, or existing adapter code.** The
  entire adoption is additive: a new `web/` directory and roughly 25 lines of
  CORS middleware in the existing HTTP adapter.

## Consequences

### Easier

- **The fleet's console now has a facility screen at all** — before this
  adoption, `facility-layout`'s data (sites, zones, aisles, slots) had no
  human-facing view anywhere in the estate; an operator had to call the REST
  API directly.
- **Ownership stays exactly where ADR-0002 intends.** This screen ships,
  breaks, or is fixed inside this repo's own PRs and CI, never requiring a
  coordinated change in `warehouse-console` or any sibling remote.
- **Zero domain or contract churn.** Because this service's role in the
  console is a self-contained screen rather than a BFF participant, adopting
  the architecture cost nothing beyond `web/` and CORS — no new use case, no
  new port, no new migration.
- **The Open Host Service / no-inbound-dependency property is unweakened.**
  Adopting the console did not create a new caller into this service from
  another bounded context's backend; `facility-mfe` calling this service's
  own API is the same shape as any other client of this Open Host Service.

### Harder

- **CORS is now permanent surface on a service that never needed it before**,
  same cost ADR-0002 names for the other three services: the allow-list must
  be kept current as a real deployed console origin is added, and a forgotten
  update is a silent browser-side failure, not a loud backend error.
- **`facility-mfe`'s wire types are hand-mirrored, not generated**, from
  `dto.go`. A future DTO field rename must be caught by whoever reviews that
  PR remembering to check `web/src/types.ts` — nothing currently enforces
  this in CI.
- **A seventh place `@warehouse/ui-kit` is consumed via `file:../../warehouse-ui-kit`**,
  the same pre-1.0, no-registry coupling ADR-0002 already flagged as a
  fleet-wide "harder" consequence: this repo's CI must check out `ui-kit` as
  a sibling directory and build it first, same as every other remote.

## References

- [ADR-0002 — Micro-frontend console architecture over per-service REST, with
  a thin BFF for cross-service reads](https://github.com/claudioed/warehouse-ops-agent/blob/docs/adr-mfe-architecture/docs/docs/adr/0002-micro-frontend-console-architecture.md)
  (`warehouse-ops-agent`) — the fleet-wide decision this record adopts.
- [Context map](../ecosystem/context-map.md) — this service's Open Host
  Service / no-inbound-dependency position, unchanged by this adoption.
- [ADR-0007 — MCP inbound adapter](./0007-mcp-inbound-adapter.md) — the other
  precedent for adding a new inbound surface additively, without touching
  domain or application code.
