---
id: 0043-a2a-inbound-adapter
title: "43. A2A inbound adapter (third driving adapter)"
sidebar_label: 43. A2A inbound adapter
sidebar_position: 43
description: "ADR 0043 - facility-layout is an Agent2Agent (A2A) agent through a third driving adapter over the same read use cases as REST and MCP. Two curated read-only skills, site_layout and travel_distance, in a message-only agent served by cmd/a2a on port 8093. No write skill, because the MCP surface offers no write. A2A carries delegation only; facts still leave as CloudEvents."
---

# 43. A2A inbound adapter (third driving adapter)

## Status

Accepted (2026-10-29). Follows [ADR 0007](./0007-mcp-inbound-adapter.md) (the MCP
adapter this one is modelled on), [ADR 0015](./0015-remove-rest-mcp-auth.md)
(no authentication on REST or MCP), [ADR 0024](./0024-cloudevents-mandatory-envelope.md)
(CloudEvents on every Kafka message), [ADR 0033](./0033-slug-prefixed-mcp-tool-errors.md)
(REST problem slugs reused on a second surface) and
[ADR 0039](./0039-postgres-and-kafka-are-the-only-runtime.md) (Postgres and
Kafka are the only runtime). Mirrors the fleet's reference A2A rollouts
(wes-work-planning ADR-0046, workforce-management ADR-0039).

## Context

The fleet is adopting the Agent2Agent protocol so that a bounded context can
**delegate** a goal to another context's agent. facility-layout already exposes
its warehouse map over REST and MCP. It is a **read-only Open Host Service**:
the site, zone, aisle and slot structure is consumed, never mutated, by the
rest of the estate, and its MCP surface (ADR 0007) registers read tools only.

Three transports now carry three different things and must not overlap:

| Transport | Carries |
|---|---|
| Kafka + CloudEvents | **facts** between contexts (ADR 0024) |
| MCP | an agent's **tools** |
| A2A | **delegation** of a goal to another context's agent |

## Decision

Add `internal/adapters/inbound/a2a`, a third driving adapter over the **same**
read use cases (`GetSiteLayout`, `EstimateTravelDistance`) as the HTTP and MCP
adapters, built on the official `a2aproject/a2a-go/v2` SDK (pinned `v2.7.0`)
and served by a new `cmd/a2a` binary: JSON-RPC 2.0 over HTTP, port **8093**,
the Agent Card at `/.well-known/agent-card.json`, the endpoint mounted at `/`
and `/a2a`. `cmd/a2a` is cloned from `cmd/mcp` (same Postgres wiring, same boot
migrations) plus a mandatory `A2A_AGENT_URL`: an absolute http(s) URL advertised
on the card, refused at boot otherwise.

### Two curated read skills, not one per endpoint

| Skill | Kind | What it does |
|---|---|---|
| `site_layout` | read | one site's drawable map: zones, aisles in walk order and slot codes, fixed structures and the site header, for `siteCode`. |
| `travel_distance` | read | the shortest map distance in metres between two coded locations `from` and `to`, flagged `estimated` when any leg used the bay-pitch fallback. |

Both are derived from the existing MCP tools (`get_site_layout`,
`estimate_travel_distance`). Of the MCP surface's six reads they are the two a
delegating agent most plausibly asks for as a goal (*what does this site look
like* and *how far is it from here to there*); `list_sites`,
`list_functional_locations`, `get_zone_grid`, `get_zone_travel_graph` and the
report tool stay MCP-only tools, which is where an agent's inspection tools
belong.

### The write gate: read-only, because MCP is read-only

A skill may change state only if the context's MCP surface already offers that
write. It does not: ADR 0007 and `NewServer` register read tools only, and
`AGENTS.md` makes this an Open Host Service whose downstream conformists never
get write access. The agent therefore offers **no
write skill** and `cmd/a2a` wires no write use case, no event publisher and no
outbox. Two tests pin it: `Deps` holds exactly the two read use cases, and an
AST scan fails if the adapter or `cmd/a2a` takes any `usecases` symbol outside a
read-only allow-list. Widening this is a decision about the MCP surface first,
and a new ADR.

### Message-only, answered in-band, REST classification

Both skills are synchronous request/response reads, so the agent is
message-only (no Task lifecycle). Domain rejections and malformed requests are
**answered** as RFC 7807-shaped `problem`s inside the message envelope, never
raised as JSON-RPC transport errors, with the **same status and problem type as
the REST adapter**: `404` not found (`site-not-found`,
`location-slot-not-found`), `400` malformed input (`malformed-location-code`,
`invalid-site-code`, `missing-location-code`, plus the adapter's own
`malformed-skill-request`, `unknown-skill`, `invalid-argument`), `422`
semantically invalid (`no-route-between-zones`, `no-route`), `409` conflicts and
`500` `internal-error` for anything unmapped, whose cause is logged and never
returned. The adapter keeps its own copy of the REST error table (it depends on
application and domain only);
`TestErrorTableMatchesRESTOneForOne` compares slug **and status** against REST
through `http.ErrorSlugs()`, which gained a `Status` field for this. That is the
only change to the REST adapter, and it is additive.

### Strict input

Unknown argument names and trailing data are **rejected**, not ignored (a
misspelled `siteCode` must not silently turn into a different question). Go's
`encoding/json` matches names case-insensitively, so `sitecode` still reaches
`siteCode`. No shared domain error in this context names a deprecated key
alias, and a test pins that no answer part mentions one.

### Unauthenticated, like REST and MCP

The card declares no security schemes: this surface is unauthenticated by the
same fleet-wide decision as REST and MCP (ADR 0015). Schemes are added to the
card, not a redesign, if that decision is ever reversed.

### Facts still travel on Kafka

A2A never becomes a data-sync channel. Both skills answer from this context's
own state and publish nothing. The map's facts still leave only as
CloudEvents from the outbox relay in `cmd/facility`.

## Consequences

- A fourth deployable (`/app/a2a`) in the same image and an `a2a.*` chart block
  (`<release>-a2a`, default **off**), exposed through Kong at
  `/api/a2a/facility-layout` rewritten to `/`. Unlike the write-capable
  rollouts it needs no Kafka and no `EVENT_PUBLISHER`.
- Because it is read-only, a delegating agent cannot ask this context to change
  the map; it edits only through REST (ADR 0019 idempotency applies there).
- `site_layout` returns a whole site, the same payload as the MCP tool; the
  size grows with the site. A zone-scoped skill would be a later, deliberate
  addition.
- Port 8093 is the fleet's A2A port (8090 is the MCP server, 8091 the projector
  admin port, 8092 the reports API).
- Like `cmd/mcp`, `cmd/a2a` runs the OLTP migrations on start (idempotent,
  ADR 0023) and keeps one replica; it holds no session state.

## Alternatives considered

- **A write skill (register or decommission structure).** Rejected: the MCP
  surface offers none, so A2A would widen what the context exposes.
- **One skill per MCP tool (six).** Rejected: the card is a goal catalogue for
  other agents, not a mirror of the tool list.
- **Importing the MCP or REST error tables.** Rejected: adapters stay
  independent; a drift test replaces the import.
