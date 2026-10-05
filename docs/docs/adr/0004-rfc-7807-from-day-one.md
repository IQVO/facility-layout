---
id: 0004-rfc-7807-from-day-one
title: 0004 — RFC 7807 problem details from the first commit
sidebar_label: 0004 · RFC 7807 from day one
description: Skipping the bespoke error shape the four sibling services had to migrate away from.
---

# 0004 — RFC 7807 problem details from the first commit

**Status:** Accepted

## Context

The four sibling services in `warehouse-systems` all started with a bespoke
error body — roughly `{"error": "something went wrong"}` — and later migrated
to **RFC 7807 Problem Details** (`application/problem+json`) as part of a
REST-hardening task. That migration was a breaking change to every error
response, done after the APIs already had consumers and OpenAPI specifications
describing the old shape.

The forces when this service was being built:

- **The migration cost is known, and it was not small.** Four repositories had
  just paid it. Nothing about this service would have made paying it a fifth
  time cheaper.
- **This service is designed to have several consumers.** As a Generic
  Subdomain that `inventory-storage`, `wes-work-planning` and
  `fulfillment-execution` are meant to conform to, a breaking change to its
  error contract is a breaking change for everyone at once.
- **This domain has genuinely rich failure modes.** A slot registration can
  fail because the site doesn't exist, the zone is decommissioned, the code is
  malformed, the code is taken, the capacity is zero, or a named placement
  rule refused it. Those are six materially different situations that a caller
  may want to handle differently, and `{"error": "..."}` flattens all of them
  into string matching.
- **The 409/422 distinction only pays off if it is machine-readable.** A
  conflict a caller should retry later and a request a caller must change are
  different things; if the response body cannot express which is which, the
  status code split is decoration.
- **There is a standard, and the fleet had already converged on it.**

`CLAUDE.md` for this repository states the decision directly: *"do NOT build
the old bespoke `{"error":...}` shape and migrate later like the other four
repos did historically — go straight to RFC 7807."*

## Decision

We will return **RFC 7807 `application/problem+json` on every error response,
from the first commit**. There will be no bespoke predecessor shape and no
migration.

```json
{
  "type": "https://errors.facility-layout.warehouse-systems.dev/placement-rule-violated",
  "title": "Location type is not legal in this zone",
  "status": 422,
  "detail": "location type violates a placement rule for this zone: PalletRack is denied in zone WH1-STOR-FRZ by rule [RULE-FRZ-NO-SHELF: Deny PalletRack where temperatureClass=Frozen]",
  "instance": "/locations"
}
```

- `type` is a stable URI identifying the error **category**, namespaced under
  `https://errors.facility-layout.warehouse-systems.dev/`. It is an
  identifier; it does not have to resolve to a page.
- `title` is a fixed human string per category and never varies for a given
  `type`.
- `detail` is the dynamic, request-specific message — this is where the
  violated rule, the offending segment or the conflicting code gets named.
- `instance` is the request path.

The mapping lives in exactly one place,
`internal/adapters/inbound/http/errors.go`. It was originally written as
two parallel switch statements — `statusFor(err)` and `problemFor(err)` —
keyed off **typed domain errors** with `errors.Is`; it has since been
consolidated into **one `errorCategories` table** whose rows pair each
typed error with its status and problem identity, so a new error needs
one row, not two coordinated switch arms. (That consolidation is the
improvement the "two switches" consequence below anticipated; the ADR
records the current table shape.) The domain never learns that HTTP
exists.

The status-code semantics are fixed alongside it:

| Code | Meaning |
|---|---|
| `400` | Malformed input — bad JSON, a code that is not seven `[A-Z0-9]` segments, a missing identifier |
| `404` | The named resource does not exist |
| `409` | State conflict — code taken, parent not Active, already decommissioned |
| `422` | Semantically invalid — non-positive capacity, unknown enum, **PlacementRule violation** |

This is the one place where this repository deliberately does **not**
replicate the other four services' history: it starts where they ended up.

## Consequences

### Easier

- No breaking migration, ever. The first consumer to integrate gets the final
  error contract.
- Callers can branch on `type` — a stable slug — instead of matching on
  English prose that changes when someone improves an error message.
- The 409/422 split is genuinely usable: a caller can tell "retry later,
  something else must change" from "your request is wrong."
- The `detail` field carries the domain's own precise message, which is what
  makes bulk-import reports and placement-rule rejections actionable rather
  than merely negative.
- The problem type catalogue doubles as documentation of every distinct
  failure mode — 37 of them, listed in [Conventions](../api-reference/conventions.md).
- `apis/openapi.yaml` has one shared `Problem` schema component referenced by
  every error response, rather than a per-endpoint invention.

### Harder

- Every new typed domain error needs a row in the `errorCategories`
  table (`internal/adapters/inbound/http/errors.go`). An error with no
  row silently falls through to `500 internal-error`, which is caught by
  tests rather than by the compiler. *(Originally two coordinated switch
  statements — `statusFor` and `problemFor` — where forgetting one of the
  two arms had the same effect; the single table removed the chance of
  the pair drifting apart.)*
- The catalogue is large and grows with the domain. That is honest — the
  domain really does have that many distinct failure modes — but it is more
  to maintain than a single error string.
- `application/problem+json` occasionally surprises tooling that assumes
  `application/json` for everything, and clients written casually against the
  happy path may not parse it.
- Choosing the right granularity for `type` requires judgement. Too coarse
  and callers are back to string-matching `detail`; too fine and the
  catalogue becomes unusable. The rule adopted here — one `type` per
  *category* of cause, with the specifics in `detail` — is a convention that
  has to be applied consistently by hand.
