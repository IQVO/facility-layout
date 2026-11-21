---
paths:
  - "internal/adapters/inbound/mcp/**"
  - "internal/adapters/inbound/a2a/**"
  - "internal/adapters/outbound/mcpclient/**"
---

# Fleet rule: every agent tool declares its authority level

Adopted with the Agentic Standard v2 decision (architecture section of the docs site). Enforcement is phased: the declaration and the catalogue come first, the fitness tests arrive with the harness template, so a repo that has not yet been rolled is not red.

- Every MCP tool and every A2A skill carries exactly one authority level. L1 inform: reads facts, changes nothing. L2 recommend: returns a proposal or ranking, changes nothing. L3 act on approval: changes state, and a human confirmation sits between the proposal and the call. L4 act within an envelope: changes state without per-call approval, and ONLY where the owning context's domain enforces the envelope as an invariant. There is no L5; governed autonomy is not adopted.
- Declare it where the tool is registered: MCP tool metadata key com.warehouse/authority (reverse-DNS prefix, then a slash, then the name) with the value `L1`, `L2`, `L3` or `L4`; A2A skill tag `authority:L1` (and so on). The fleet card and tool catalogue is generated from these, so a card change also changes the catalogue (see the A2A rule).
- A read tool is L1 or L2. A write tool is L3 unless an ADR in the owning context grants L4. The level documents the gating the CALLER must apply; an unauthenticated endpoint cannot verify a human, so the confirmation lives in the client (the ops agent's confirmation token), never in the context.
- Never widen what a context exposes: a read-only MCP charter stays read-only until that context's own ADR amends it. An agent surface never offers a write the REST and domain layers do not already guard with invariants.
- An agent-initiated state change is attributable. When a write arrives through MCP or A2A the adapter stamps `initiated_by` into the domain event's data (fields `via` as `mcp` or `a2a`, optional `proposal_id`, `approved_by`, `agent_id` supplied by the caller). REST writes carry no `initiated_by`. This is a data field on the existing event, never a CloudEvents extension attribute.
- Promotion is earned: a behaviour moves from L2 to L3 only after replay and shadow evaluation pass; L3 to L4 needs an ADR in the owning context.
- Unauthenticated like every fleet endpoint: this rule adds no bearer, JWT or API-key check.
