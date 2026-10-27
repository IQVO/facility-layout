---
paths:
  - "README.md"
---

# Fleet rule: every README follows the fleet README skeleton

- Every repository's README follows one section skeleton, in a fixed order. The
  reference implementation is facility-layout's README; inventory-storage and
  order-management are compliant seconds. The full skeleton and the per-class
  variants are documented on the warehouse-docs fleet-health pages (README
  standard) and enforced by the fleet docs-coverage audit's readme-sections check.
- The order: title plus ONE identity paragraph (the bounded context, what it owns,
  what it explicitly does not own, strategic classification) — then a Disclaimer
  section — then Documentation (own docs site, else the warehouse-docs context
  pages, with a page-map table) — Architecture (hexagonal layering tree, the
  fitness test that enforces it) — Domain events / Integration (topic names,
  CloudEvents type convention with real examples, publishes and consumes per
  sibling) — Running it with a Configuration env table per binary — Observability
  (OTel signals, what gets exported) — API (routes, errors, idempotency, spec
  link) — the conditional sections (analytics data product, Helm chart with the
  MCP deployment subsection, operator micro-frontend) — Local development /
  quality gate — Tests (real suite names and the CI job names ci.yml actually
  runs) — Architecture Decision Records (index plus count) — License.
- Sections are REQUIRED BY REPO FACTS: a section is mandatory exactly when the
  repo has the fact it documents (a runnable service, an OpenAPI or AsyncAPI
  contract, a chart, an mcp binary, a web remote, analytics migrations, ADRs, a
  LICENSE file). Document what exists; never claim what does not; never leave a
  fact the repo has undocumented.
- The Disclaimer section sits immediately after the identity paragraph and uses
  this text verbatim, and nothing else: "This project is a domain-driven design
  study. It is not associated with, endorsed by or affiliated with any company,
  and it uses no company's code, data, logos or trademarks. All names, figures
  and examples are illustrative or synthetic." The older study-project blockquote
  is replaced by this section; there is no third wording.
- Counts (events, ADRs, CI jobs, ports) are recomputed from the repository when a
  README is edited, never copied from a sibling. A README that names CI jobs
  lists the jobs the workflow actually runs.
- Changing the skeleton is a fleet decision: edit the canonical rule in
  warehouse-docs first, then fan the copy out to every repository in one
  coordinated set of PRs.
