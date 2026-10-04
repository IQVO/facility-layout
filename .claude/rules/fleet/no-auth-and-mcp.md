---
paths:
  - "internal/adapters/inbound/**"
---

# Fleet rule: no auth layer; MCP is additive

- Static-bearer auth was rolled out fleet-wide and then deliberately REVERTED on 2026-09-11: every REST and MCP endpoint is unauthenticated until a fresh auth-model decision is recorded. Do not add bearer, JWT or API-key middleware to an inbound adapter, even "helpfully"; `TestNoAuthMiddlewareReintroduced` fails the build if it appears. Re-adopting auth is a user decision plus an ADR first.
- MCP servers are additive inbound adapters (official MCP Go SDK, Streamable HTTP only, a separate cmd/mcp binary). Nothing in the domain or application depends on them, and nothing else imports the MCP adapter (`TestMCPAdapterDependencyRule` enforces both directions).
