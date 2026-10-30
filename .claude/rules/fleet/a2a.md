---
paths:
  - "internal/adapters/inbound/a2a/**"
  - "internal/adapters/outbound/a2a/**"
  - "cmd/a2a/**"
---

# Fleet rule: A2A carries delegation only

- Three transports, no overlap. Kafka + CloudEvents carries FACTS between contexts; MCP carries an agent's TOOLS; A2A carries the DELEGATION of a goal to another context's agent. A2A is never a data-sync channel: delegate out, take the facts back through CloudEvents. A skill that changes state runs the SAME use case and transactional outbox as REST and MCP, so the event still leaves on Kafka.
- An A2A server is an additive inbound adapter (`a2aproject/a2a-go/v2`, pinned at v2.7.0 fleet-wide), a separate cmd/a2a binary on port 8093, the Agent Card at `/.well-known/agent-card.json`, JSON-RPC at `/` and `/a2a`, and Kong route `/api/a2a/<context>` rewritten to `/`. The chart's `a2a` block is default off; warehouse-infra turns it on. `A2A_AGENT_URL` is mandatory and absolute. Nothing in the domain or application imports it, and nothing else imports it (`TestA2AAdapterDependencyRule` enforces both directions).
- Curated, goal-shaped skills: at most two per context, never one per endpoint. A write skill exists only when the context's MCP surface already offers that exact write; a read-only MCP charter means a read-only agent (no write use case in `Deps`, no publisher, no Kafka env). Never widen what a context exposes.
- Message-only and strict: reject unknown arguments and trailing data, and answer domain rejections in-band as RFC 7807-shaped problems with the same status classification as the REST adapter. Never leak infrastructure error text or a deprecated alias in an answer.
- Unauthenticated, like REST and MCP: the 2026-09-11 auth revert covers A2A too. The card declares no security schemes. Do not add bearer, JWT or API-key middleware; re-adopting auth is a user decision plus an ADR first. A human-gated write (the warehouse-ops-agent act slice) is gated by a confirmation token and a human step, not by authentication.
- A2A CLIENTS are rare and deliberate: fulfillment-execution delegates to wes-work-planning; the warehouse-ops-agent act slice delegates `release_next_work` (human-gated), and the same agent asks exception-management a read-only advisory question when proposing a release (it never gates or alters the decision). Every other context has no outbound A2A client (`TestNoOutboundA2AClient`); process-path-management and labor-performance additionally ban all calls to sibling contexts. A client takes its peer URL from env (`<PEER>_A2A_URL`, unset means the feature is off), uses the traced HTTP client, and maps peer failures to typed errors.
- Any change to a card (skills, arguments) also updates the fleet card index, the A2A agents page in the architecture section of the warehouse-docs site; its live-card drift check fails otherwise.
