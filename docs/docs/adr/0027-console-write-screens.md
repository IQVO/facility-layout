---
id: 0027-console-write-screens
title: 27. Write-capable console screens
sidebar_label: 27. Console write screens
sidebar_position: 27
description: "ADR-0011 adopted the console as a read-only viewer of facility data; the web app has since grown write-capable screens (ConfigureScreen, LayoutDesigner, RackPlanner). This records that extension and its contract."
---

# 27. Write-capable console screens

## Status

**Accepted.** Extends ADR-0011 (which adopted the console as a read-only
viewer); recorded retroactively 2026-10 because the screens shipped
without their own ADR.

## Context

ADR-0011's scope was deliberately read-only: the facility screen
rendered `GET /sites` and `GET /sites/{siteCode}/layout` and changed
nothing. Real operations quickly outgrew that — an operator who can SEE
a wrong zone code wants to fix it in the same place, not drop to curl.

The web app (`web/`) grew three write-capable screens beyond the
read-only map ADR-0011 described:

- **ConfigureScreen** — register sites, zones, aisles, location types,
  placement rules.
- **LayoutDesigner** — set slot and aisle geometry (ADR-0017's PUT
  endpoints), register cross-aisles and fixed structures.
- **RackPlanner** — register slots and view the zone grid + SVG floor
  plan with per-role glyphs (ADR-0016/0017).

## Decision

The console is a first-class WRITE client of this service's REST API,
within these rules:

- Every write goes through the same public REST API any other client
  uses — no privileged endpoint, no server-side session, no bespoke
  write path. The SPA is static files behind nginx; all state lives in
  the API.
- Every resource-creation POST from the console sends an
  `Idempotency-Key` (a fresh `crypto.randomUUID()` per logical submit),
  per ADR-0019 — the console is exactly the "lost-response client
  retry" client that middleware exists for, and CORS allows the header.
- REST remains unauthenticated (ADR-0015) — the console inherits that
  decision rather than reintroducing auth just for the UI.
- The screens keep ADR-0011's "no console-bff fan-out" property: the
  browser talks to this service directly, CORS-scoped.

## Consequences

- One surface to harden: a console bug is an API bug; the API's
  validation, idempotency and OCC guards (ADR-0025) are the only
  protection, which is the point.
- The web build (`npm run build` in `web/`) is a CI gate; a broken
  screen now blocks merges the way a broken handler would.
- ADR-0011's "purely a new consumer of an existing, unchanged contract"
  framing no longer covers the whole console; this record is the
  addendum.

## Related

- ADR-0011 — the read-only adoption this extends.
- ADR-0015 — unauthenticated REST/MCP.
- ADR-0019 — Idempotency-Key middleware (and the CORS header allow-list
  fix that lets the console send it).
