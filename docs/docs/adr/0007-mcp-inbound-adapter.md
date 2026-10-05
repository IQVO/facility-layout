---
id: 0007-mcp-inbound-adapter
title: 7. Model Context Protocol as an inbound adapter, not a new service
sidebar_label: 7. MCP inbound adapter
sidebar_position: 7
description: "Expose this bounded context to the AI ecosystem via an MCP server built as a second driving adapter over the existing use cases — Streamable HTTP, official Go SDK, static bearer-key auth, curated intent-level READ tools. This context is read-only (an Open Host Service), so no write tool is exposed yet, though the scope seam exists."
---

# 7. Model Context Protocol as an inbound adapter, not a new service

## Status

**Accepted — auth section superseded by ADR-0015.** The reference
implementation and pilot for this pattern across
the estate is `fulfillment-execution` (its ADR-0008); this record is
`facility-layout` adopting that same decision, adapted to a read-only Open Host
Service. The static-bearer-key authentication this ADR originally specified
for the MCP endpoint was removed fleet-wide by
[ADR-0015](0015-remove-rest-mcp-auth.md); the endpoint is unauthenticated
pending a fresh auth-model decision.

**Addendum (2026-09-07) — deployable.** Until this date `cmd/mcp` existed only
as code: the Dockerfile did not build it and the Helm chart had no MCP
workload, so no cluster ran it. The image now ships the binary as `/app/mcp`
and the chart gains an opt-in `mcp` block (`mcp.enabled`, default `false`)
rendering a `<release>-mcp` Deployment (`app.kubernetes.io/component: mcp`),
a ClusterIP Service on `8090` and a Secret carrying `MCP_READ_KEY` /
`MCP_READWRITE_KEY`. To make it probeable, `cmd/mcp` gained an
**unauthenticated `GET /healthz`** and now mounts the authenticated MCP
Streamable HTTP handler at **both `/` and `/mcp`** (the `/mcp` path is the
`*_MCP_ENDPOINT` convention `warehouse-ops-agent` already uses). Deployment to
the `warehouse` cluster is driven from `warehouse-infra`, which sets
`mcp.enabled=true` and generates the keys; this repo only makes it possible.

## Context

The platform is being connected to the AI ecosystem (Claude, Cursor, ChatGPT,
agent frameworks). The interoperability standard those clients speak is the
**Model Context Protocol (MCP)**: a client discovers a server's *tools*
(model-callable functions), *resources* (read-only context), and *prompts*
(reusable templates), then an LLM decides which to call.

The forces:

- **There is already a clean action surface.** Every capability of this service
  is an application-layer **use case** (`internal/application/usecases`), one
  struct per use case, reached through ports. The `chi` HTTP adapter is a thin
  driving adapter over exactly those use cases. An AI client needs the same
  reads the HTTP client already has.
- **This context is read-only to the rest of the system.** `facility-layout` is
  a Generic Subdomain and an **Open Host Service**: it owns the "warehouse
  map" (site → zone → aisle → slot) that `inventory-storage`,
  `wes-work-planning`, `workforce-management` and `fulfillment-execution`
  **consume but never mutate**. The map is written by operators building the
  facility, not by agents. So the useful MCP surface here is *reads* — an agent
  answering a placement or travel question navigates the map; it does not
  change it.
- **The domain must not learn about MCP.** ADR-0001's dependency rule is
  load-bearing: domain depends on nothing, application depends on domain,
  adapters depend inward. A protocol whose shape is set by an external LLM
  ecosystem is precisely the kind of concern that must stay in an adapter.
- **MCP has an idiomatic Go path now.** The official **MCP Go SDK**
  (`github.com/modelcontextprotocol/go-sdk`) is a Tier-1 SDK. Building the
  server in Go keeps it in the same language, module, and quality gate as the
  rest of the service — no Python sidecar, no second toolchain.
- **The spec is versioned aggressively.** Revisions in 2025-06, 2025-11, and
  2026-07 have already deprecated features (`roots`, `sampling`, `logging` —
  SEP-2577). Whatever is built will need to track a moving contract.
- **Tools are model-controlled.** Unlike an HTTP client driven by code we
  wrote, an LLM chooses *when* to call a tool and *with what arguments*. The
  spec's own guidance is emphatic: curate a small set of intent-level tools,
  treat tool invocation as requiring host consent, and guard state-changing
  tools most heavily. Here there are no state-changing tools to guard.
- **This is an internal, non-user-facing deployment.** The servers run inside
  the `warehouse` kind cluster for agent and developer use, not on the public
  internet for end users. The MCP authorization spec permits a static bearer
  token for exactly this case; full OAuth 2.1 is required only when a server
  faces real end users.

## Decision

**We will expose this bounded context to the AI ecosystem through an MCP server
built as a second driving adapter over the existing use cases — leaving the
domain and application layers untouched — and, because this context is
read-only to the rest of the system, we will register only READ tools, a
scoped resource, and a prompt, and no write tool.**

### The adapter, mirroring the HTTP one

A new `internal/adapters/inbound/mcp/` sits beside `internal/adapters/inbound/http/`:

```
internal/adapters/inbound/mcp/
  server.go      MCP Server wiring (Go SDK), capability registration
  tools.go       intent-level READ tool handlers -> call read use cases
  resources.go   read-model resources (scoped, not bulk)
  prompts.go     workflow prompts (operational SOPs)
  auth.go        bearer-key auth middleware (interface; OAuth-ready seam)
  mapping.go     tool I/O DTOs + compact projections of the read models
```

It depends inward on `application` exactly as the HTTP adapter does. No MCP type
appears in `internal/domain/**` or `internal/application/**`. The tool handlers
call the **same** read use case structs the HTTP handlers call — never a
parallel code path, never the domain directly.

### A separate `cmd/mcp` binary

The MCP server ships as its own composition root, `cmd/mcp/main.go`, reusing the
same repositories and repo-selection (in-memory vs Postgres) as `cmd/facility`.
Two deployables from one module: the HTTP service and the MCP server. This
isolates blast radius, lets the two scale independently, and keeps
least-privilege clean.

### Streamable HTTP only

The single supported transport is **Streamable HTTP**. We do not ship stdio
builds; local desktop-client use goes through the same HTTP endpoint. One
transport is one thing to secure, trace, and test.

### Curated, intent-level READ tools — not one tool per endpoint

Tools are designed around decisions an agent makes, not around REST endpoints.
Mechanically wrapping every HTTP route would overwhelm the model — the
documented number-one MCP anti-pattern. The surface for this context:

- `list_sites` (read) — every site on the map as `{code, name}`, the entry
  point for discovery.
- `get_site_layout` (read) — one site's full drawable structure as a compact
  nested map (zones → aisles → slot codes, with each zone's temperature class
  and hazmat flag and each aisle's walk-order hint).
- `get_zone_grid` (read) — one zone's slots as a 2D grid (levels × (aisle, bay)
  columns) for detailed rack reasoning.

All three return **compact DTOs**, not the raw domain aggregates: a slot is
reduced to its location code, so the payload stays scoped to "the shape of the
map" rather than every slot's capacity envelope.

The single resource exposes the site layout as a **scoped** context contract
(`layout://facility/{siteCode}`), backed by the same `GetSiteLayout` read
model — never a database dump. The one prompt (`explore_layout`) encodes the
operational SOP for navigating site → zone → aisle → slot to answer a placement
or travel question.

### No write tool — but the scope seam is kept

Because the map is consumed and not mutated by the rest of the system, **no
write tool is registered.** There is nothing here for an autonomous agent to
change. The read/read-write `Scope` plumbing in `auth.go` is nonetheless kept
identical to the pilot: two key classes, the `scopeAllows` gate, and the
scope-parameterised tool wrapper all exist, and every registered tool requires
`ScopeRead`. This keeps the pattern uniform across the five contexts and means
that if a legitimate write use case ever appears here, exposing it is a single
`ScopeReadWrite` registration with no auth rework.

### Static bearer-key auth, behind an OAuth-ready seam

`auth.go` validates a per-client API key (from a Kubernetes Secret) on every
request; missing or invalid key returns `401` with a `WWW-Authenticate`
challenge; the key is never logged. The middleware is an **interface**, so an
OAuth 2.1 resource-server implementation can drop in later without touching any
tool handler.

### Reuse the existing observability

The adapter is instrumented with the same OpenTelemetry setup as the HTTP
boundary: a span per tool call (tool name, required scope, outcome). MCP calls
appear in traces next to HTTP requests.

## Consequences

### Easier

- **The domain and application layers do not change at all.** MCP is purely
  additive; the dependency rule (ADR-0001) is preserved and checked by the
  existing arch-go fitness tests, extended to cover the mcp adapter (it must not
  import `outbound/*`).
- **One read surface, two protocols.** HTTP and MCP call the same read use
  cases, so behaviour is identical regardless of caller.
- **Nothing here can be mutated by an agent.** Because the context is read-only
  and no write tool is registered, the entire class of "an autonomous agent
  changed the wrong thing" risk does not exist for this server. This is the
  safest possible MCP surface, and it is that way by the nature of the
  subdomain, not by accident.
- **It stays in Go, in one quality gate.** The MCP adapter is unit-tested to the
  same ≥90% bar, linted, and CI-gated like every other package.
- **The auth upgrade is contained,** and the write seam is already in place for
  the day a write use case is genuinely warranted.

### Harder

- **A second deployable to run and secure.** `cmd/mcp` is another binary, image,
  and ingress. The isolation is deliberate but it is real operational surface
  that did not exist before.
- **Auth is deliberately minimal.** A static bearer key is appropriate for an
  internal, non-user-facing server, but it does **not** cover user-facing,
  multi-tenant use. The server must stay in-cluster until the OAuth seam is
  taken. Recording that boundary is the point.
- **The MCP spec is a moving target.** Aggressive versioning and deprecations
  mean the SDK must be pinned and revisited; deprecated features
  (`roots`/`sampling`) must be avoided in favour of tool parameters.
- **Tool curation is an ongoing discipline, not a one-time choice.** Nothing in
  the compiler stops a future PR from adding a tool per endpoint, or from
  adding a write tool without re-examining whether this context should really
  accept agent writes at all. The MCP governance charter
  (`docs/mcp/governance-charter.md`) and its review gate exist to hold that
  line.
- **LLM-chosen arguments are untrusted input.** Every tool handler validates its
  inputs defensively (reject an empty or unknown site/zone, never default) —
  the caller is a model, not our own code — which is stricter than what the
  HTTP DTO layer assumes.
