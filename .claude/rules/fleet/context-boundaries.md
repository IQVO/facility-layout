---
paths:
  - "internal/adapters/outbound/**"
  - "internal/application/ports/**"
---

# Fleet rule: bounded-context boundaries

- process-path-management and labor-performance ban ALL REST and MCP calls to sibling contexts. Cross-context data arrives through events or local declarative data, never a live lookup; check each repo's own CLAUDE.md before adding any outbound adapter.
- Contexts integrate through published events and ports. Never import another context's Go packages; duplicate the small contract instead and keep the strings identical on both sides.
- Frontend micro-frontend remotes live in the repo of the owning service (its web/ directory) and are lazy-loaded by warehouse-console; the console reads the API origin from /config.json at runtime.
- Local edge: Nginx on :80 serves frontend assets and Kong on :8000 serves the APIs; Kong is never the asset edge.
