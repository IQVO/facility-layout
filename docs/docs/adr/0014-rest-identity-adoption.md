---
id: 0014-rest-identity-adoption
title: 14. Adopt the fleet REST identity standard (static bearer keys, read/read-write scopes)
sidebar_label: 14. REST identity adoption
sidebar_position: 14
description: "facility-layout adopts warehouse-ops-agent ADR 0005: every REST route except /healthz is protected by the same static bearer key + scope middleware the MCP adapter already carried, lifted into internal/adapters/inbound/auth so both surfaces share one implementation, rolled out through AUTH_MODE=log before enforce."
---

# 14. Adopt the fleet REST identity standard

## Status

**Superseded by ADR-0015** (2026-09-11: the fleet-wide static-bearer auth
layer this ADR adopted was reverted; REST and MCP are unauthenticated
pending a fresh auth-model decision — see
[ADR-0015](0015-remove-rest-mcp-auth.md)). Originally accepted 2026-09-07;
the decision itself was
[warehouse-ops-agent ADR 0005 — Fleet REST identity: static bearer keys
with read/read-write scopes, no IdP](https://github.com/claudioed/warehouse-ops-agent/blob/develop/docs/docs/adr/0005-rest-identity-static-bearer-scopes.md),
which is likewise superseded.

## Decision

facility-layout applies ADR 0005 as written. The `Scope` / `Authenticator` /
`StaticKeyAuth` seam that ADR-0007 gave the MCP adapter moves to
`internal/adapters/inbound/auth` (the fleet template, copied verbatim) and
gains a chi `Middleware`; the MCP adapter now imports it instead of keeping
its own copy, so this repository has exactly one identity implementation
for both surfaces. `NewRouter` mounts the middleware on every route group
through a chi `Group`, leaving `/healthz` outside it; `NewReportsRouter`
mounts the same middleware with the required scope pinned to **read**,
because the reader is read-only by construction (ADR-0010). Safe methods
need the read scope, everything else read-write; rejections are RFC 7807
problems under this service's existing type base (`…/unauthenticated`,
`…/insufficient-scope`) with a `WWW-Authenticate: Bearer` challenge on
401. Keys come from `API_READ_KEY` / `API_READWRITE_KEY` (falling back to
`MCP_READ_KEY` / `MCP_READWRITE_KEY` so one Secret can serve both
deployables); `AUTH_MODE` selects `enforce` | `log` | `off`, defaulting to
`enforce` when a key is configured and `off` — with a WARN — when none is,
so local runs and the existing handler tests are unchanged. The chart
exposes `auth.mode` / `auth.readKey` / `auth.readWriteKey` /
`auth.existingSecret`, renders `AUTH_MODE` into the ConfigMap and the key
refs into both the main and the reports Deployment. warehouse-infra rolls
this out cluster-wide in two phases (`log`, soak for `auth: would-reject`,
then `enforce`), exactly as ADR 0005 prescribes. The OAuth 2.1 seam ADR-0007
promised is preserved: swapping `StaticKeyAuth` for a resource-server
`Authenticator` changes only the composition roots.
