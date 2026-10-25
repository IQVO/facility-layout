---
id: 0033-slug-prefixed-mcp-tool-errors
title: "33. MCP tool errors follow the fleet convention: `<slug>: detail`"
sidebar_label: 33. Slug-prefixed MCP tool errors
sidebar_position: 33
description: "Every facility-layout MCP tool error now reads '<slug>: detail', where slug is the stable problem slug the REST adapter reports for the same condition; anything unmapped is 'internal-error' with its detail withheld. A caller can tell a rejection of its input from an internal failure without parsing prose."
---

# 33. MCP tool errors follow the fleet convention: `<slug>: detail`

## Status

**Accepted.** Refines [ADR 0007](./0007-mcp-inbound-adapter.md) (the MCP
adapter) and the charter's §8.3 ("domain errors MUST surface as clean
structured tool errors, mapped from RFC 7807"). Aligns with
[ADR 0004](./0004-rfc-7807-from-day-one.md), whose problem slugs it reuses.
Additive: success payloads, tool names, schemas and annotations are
unchanged.

## Context

`addTool` (`internal/adapters/inbound/mcp/tools.go`) returned the raw handler
error, so the text a caller saw was whatever the use case or repository
happened to say: `site not found`, `siteCode is required`, or, for an
infrastructure failure, a driver message. A caller could not tell a rejection
of its own input from an internal failure without matching prose, and an
unexpected error could carry internals (a DSN fragment, SQL) to a model.

The REST adapter already has the stable vocabulary: every typed error maps to
a problem-type slug (`site-not-found`, `malformed-location-code`, …) and
anything else is `internal-error`
(`internal/adapters/inbound/http/errors.go`). `warehouse-planning` solved the
same problem for its tools with `<slug>: detail` text built by `toolError`
and `slugFor`/`mapError` in its own `internal/adapters/inbound/mcp/errors.go`.
`warehouse-ops-agent`'s `mcpclient` is the known consumer of this server's
tools; it surfaces an `isError` result as an upstream failure and today
cannot discriminate.

## Decision

1. A tool error is the text `"<slug>: <detail>"` on an `isError` result. The
   slug is the **same slug the REST adapter uses** for the same condition.
2. `internal/adapters/inbound/mcp/errors.go` holds the table (`errorCatalog`,
   `slugFor`) and `mapError`. `addTool` applies `mapError` to every handler
   error, so every tool — including the conditionally registered report tool —
   follows the convention without per-handler code.
   - a typed domain error (matched with `errors.Is`, so wrapping is fine) is
     prefixed with its slug and keeps its message;
   - an error already built by `toolError` passes through unchanged;
   - anything else is logged and reported as
     `internal-error: an unexpected internal error occurred` — the detail is
     withheld from the caller and from the tool's trace span.
3. The tools' own argument checks use REST slugs too: a missing `siteCode` is
   `invalid-site-code`, a missing `zoneId` is `invalid-zone-code`, a missing or
   blank `from`/`to` of `estimate_travel_distance` is `missing-location-code`,
   a missing report window is `invalid-report-query`.
4. The MCP table and the REST table stay **one-for-one**. They are two
   adapters and do not import each other at runtime; the REST package exports
   `ErrorSlugs()` and `TestSlugTableMatchesRESTOneForOne` fails when a typed
   error is added to one table and not the other (the two sentinels REST raises
   while decoding a request body are the only deliberate omissions).
5. Each tool description gains one sentence documenting the shape.

## Consequences

- A caller can classify by slug: `*-not-found`, `unknown-*`, `malformed-*`,
  `invalid-*` and `missing-*` are rejections of its input; `internal-error` is
  ours. The slug is the discriminator; callers must never guess from the prose.
- Backward compatible for success. For errors the text gains a stable prefix
  and keeps the former wording after it (the existing eval scenarios that match
  fragments such as `site not found` still pass). A client that treats any
  `isError` as an upstream failure keeps working; one that wants to classify can
  now do so. Text without a slug (from an older server) is simply unclassified.
- Two tables must be maintained; the parity test is what makes that cheap.
  Adding a typed error to REST now also means adding it to the MCP table, and
  the build says so.
- The unmapped path hides detail on purpose; the real error is in the logs.
  An infrastructure failure of the reports REST client therefore reads
  `internal-error` instead of `reports client: unexpected status 502`.
- Ecosystem check (`origin/develop` of every other fleet repo): the only code
  consuming this server's tools is `warehouse-ops-agent`
  (`internal/adapters/outbound/mcpclient/facility_layout.go` over
  `session.go`'s `callTool`); it renders the `isError` text verbatim, so the
  prefix is carried through unchanged. The other repos only list the tool
  *names* in `fleet_tool_snapshot.golden` (names and annotations, no
  descriptions or error text). Classifying by slug on that side is a change
  in `warehouse-ops-agent`, not here.

## Related

- [ADR 0004](./0004-rfc-7807-from-day-one.md) — the problem slugs.
- [ADR 0007](./0007-mcp-inbound-adapter.md) — the MCP adapter;
  [MCP governance charter](../mcp/governance-charter.md) §8.3.
- [Sequence diagrams §6](../ddd/sequence-diagrams.md#6-read-models-over-rest-and-mcp).
