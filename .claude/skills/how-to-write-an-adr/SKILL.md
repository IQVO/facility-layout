---
name: how-to-write-an-adr
description: Write an Architecture Decision Record in this repo's numbering and format, including companion ADRs for cross-repo changes. Use when a design decision should be recorded or a change contradicts an existing ADR.
---

# How to write an ADR

Use when a change is architecturally significant — a new bounded-context
integration, a reversal of a prior decision, a cross-repo contract
change, or anything a future reader would otherwise have to
reverse-engineer from the diff. Not every change needs one: a bug fix or
a routine feature addition inside an already-decided architecture
doesn't.

## Numbering and location

`docs/docs/adr/NNNN-kebab-case-title.md`, four-digit zero-padded,
sequential — check the highest existing number
(`git ls-tree --name-only origin/develop -- docs/docs/adr/` and pick the
next integer, never reuse or guess). As of this guide the highest is
ADR-0017; the next new ADR is 0018. `docs/docs/adr/index.md` lists them
for readers; you don't need to touch it when adding a new ADR (it's not
a manually maintained index — check its own content before assuming
otherwise).

## Frontmatter (Docusaurus needs all five fields)

```yaml
---
id: NNNN-kebab-case-title
slug: /adr/NNNN-kebab-case-title
title: "NN. Title (a short noun phrase, matching the heading)"
sidebar_label: "NN. Short label for the nav sidebar"
sidebar_position: NN
description: "One or two sentences — this shows up in search and link
  previews, so make it stand alone without the rest of the doc."
---
```

`id`/`slug` are the full kebab-case filename (minus `.md`); `title`/
`sidebar_label` repeat the number as plain text (`"17. ..."`, not
`#17`); `sidebar_position` is the bare integer. See ADR-0017's actual
frontmatter for a real example:

```yaml
id: 0017-geometry-and-travel-graph
title: 17. Physical geometry and a travel-distance read model
sidebar_label: 17. Geometry & travel graph
sidebar_position: 17
```

Getting these inconsistent is the most common cause of a broken sidebar
entry or 404 after merge — verify by running the docs build (see below)
before opening the PR.

## Format: Michael Nygard's template

```markdown
# NNNN. Title (a short noun phrase)

## Status
Accepted | Proposed | Deprecated | Superseded by ADR-XXXX

## Context
The forces at play — technical, business, constraints — that make this
decision necessary. Write in the past tense, as if explaining to someone
who wasn't there. State the alternatives seriously considered, not just
the one chosen; a reader six months from now needs to know a simpler
option was weighed and rejected, not assume nobody thought of it.

## Decision
What was actually decided, stated as an active, present-tense
declaration ("we will...", not "we might..."). Be specific about the
mechanism, not just the intent.

## Consequences
What becomes easier, what becomes harder, and what future work this
creates or forecloses. Be honest about the downsides.
```

ADR-0017 is a strong model for the `## Context` section specifically: it
doesn't just assert the gap ("there's no distance model"), it checks
three real WMS products (SAP EWM's geo-coordinates + Activity Area,
Oracle WMS Cloud's Length/Width/Height + Pick Sequence, Blue Yonder's
travel-path-as-input framing) against actual vendor documentation before
choosing a design — this repo's own `AGENTS.md` names this
competitor-research-driven pattern as the house style, not a one-off.

The `## Decision` section is the part worth the most editing effort: it
should let a reader implement the same decision from scratch without
follow-up questions. ADR-0017's Decision section is a model — it states
the exact mechanism (a pure-domain `internal/domain/travel` package, no
repository/adapter imports, that falls back to a zone's `bayPitchM` when
real aisle centreline geometry is missing and explicitly flags the
result `estimated: true` rather than reporting a false-precision
number), not just the intent ("add travel distance").

## Superseding an earlier ADR

Don't edit the old ADR's Decision section. Add a `## Status` line noting
`Superseded by ADR-XXXX` on the OLD one (a one-line patch), and open the
new ADR referencing it. This repo has a real example one level
different from a pure supersession: ADR-0014 (REST identity adoption)
was fully REVERTED, not superseded — check
`docs/docs/adr/0015-remove-rest-mcp-auth.md` for the exact wording
pattern to use when a decision is undone entirely rather than replaced
by a newer mechanism; the distinction matters because a "Superseded by"
note implies the old mechanism was replaced by something equivalent,
while ADR-0015's revert means there is currently NO auth layer at all
(this repo's `internal/architecture/fitness_test.go`'s
`TestNoAuthMiddlewareReintroduced` statically enforces that the revert
stays reverted — see how-to-test.md).

## Cross-repo decisions: use a companion ADR, not one repo's private opinion

When a decision genuinely spans two bounded-context repos, write ONE ADR
per repo, each referencing the other explicitly as "the companion ADR"
with a one-line description of the split of responsibility. This repo's
own ADR-0016/ADR-0017 pair is the concrete, currently-real example (not
a hypothetical — both are in this repo, both `## Status: Proposed`):

- **ADR-0016** (`0016-functional-location-roles.md`) adds `LocationRole`
  (`Storage | Dock | Yard | WorkCenter | Drop | Staging | QC |
  Consolidation | Shipping`) to `LocationType`, so a coded location can
  be a dock door or work center, not just storage.
- **ADR-0017** (`0017-geometry-and-travel-graph.md`) adds optional
  geometry (`Point3D`, `Dimensions`, aisle centrelines, cross-aisles) and
  the pure-domain travel graph.

Each ADR's `## Related` section names the other explicitly with the
actual division of responsibility, not just a cross-link:

> ADR-0016 (its own Related section): "ADR-0017 — Geometry and the
> travel graph (the companion ADR; functional locations are where
> geometry and travel distance become useful to a consumer)."
>
> ADR-0017 (its own Related section): "ADR-0016 — Functional location
> roles beyond storage (the companion ADR; geometry and travel distance
> are what make a `Dock`, `WorkCenter`, or `Drop` location's position on
> the map actually useful)."

Neither ADR restates the other's decision — each states only its own
mechanism plus a one-line pointer to what the other one is responsible
for. Don't write the decision once in one repo/ADR and expect the
companion's readers to find it there; each bounded context's (and in
this single-repo case, each ADR's) readers should get the full picture
from their own document plus the pointer.

## After writing: regenerate and verify the docs build

```bash
cd docs
npm ci
npm run build   # onBrokenLinks / onBrokenAnchors are both 'throw' — this
                 # WILL fail if the frontmatter/slug is wrong or a
                 # cross-reference link is broken
```

A broken ADR link or malformed frontmatter fails the build with a clear
Docusaurus error, not a silent 404 — always run this locally before
opening the PR. This repo's `docs-api-drift` CI job
(`.github/workflows/ci.yml`) already gates the OpenAPI-generated REST
reference against drift; it does not run a full `docusaurus build`, so a
broken ADR cross-reference is caught locally or not at all until someone
runs `npm run build` by hand — don't skip this step assuming CI has it
covered.
