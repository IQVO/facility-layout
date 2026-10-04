---
name: code-review
description: Fast pre-commit semantic review of the uncommitted changes or a given git range: domain logic in the wrong layer, missing failure-path tests, adapter concerns in domain types, impure ports. Advisory counterpart to make check. Invoke explicitly: /code-review [range].
disable-model-invocation: true
argument-hint: "[git range]"
---

Perform a pre-commit semantic code review of the current uncommitted
changes (or, if `$ARGUMENTS` names a branch/commit range, review that
diff instead — e.g. `/code-review origin/develop..HEAD`).

This is a fast, local, advisory pass — the counterpart to `make check`'s
mechanical checks (fmt/vet/lint/test), not a replacement for them. Run
`make check` FIRST; don't spend this review's attention on anything a
linter already catches.

## What to look for, in priority order

1. **Domain logic leaking into the wrong layer.** Business rules belong
   in `internal/domain/`, not in an HTTP handler, a Kafka consumer, or a
   repository adapter. If a handler in `internal/adapters/inbound/http/`
   does anything beyond decode -> call use case -> encode, flag it — that
   logic likely belongs in the use case or the aggregate itself.
2. **A new use case with no failing-path test.** Check
   `internal/application/usecases/` — every new/changed `Execute` method
   needs a test for its domain-rule failure path, not just the happy
   path. This is the single most common gap `make coverage`'s 90% gate
   still lets through (100% line coverage on the happy path alone often
   clears 90%).
3. **A domain type carrying an adapter concern.** JSON tags, SQL column
   names, or HTTP status codes appearing on a type under
   `internal/domain/` is a boundary violation `internal/architecture/`'s
   fitness tests won't catch (they check import direction, not tag
   presence) — flag it by eye.
4. **A driven port (`internal/application/ports/`) that isn't a pure
   interface**, or a use case constructing a concrete adapter directly
   instead of depending on a port. Ports contain interfaces only.
5. **Error handling that swallows or over-wraps.** Check that domain/
   application errors map cleanly to RFC 7807 problem details at the
   HTTP boundary (`internal/adapters/inbound/http/errors.go`'s existing
   mapping table) rather than being stringified/re-wrapped repeatedly on
   the way out.
6. **A new Kafka `GroupID` assigned an inline string literal** rather
   than a named const/var/function call — this fleet has a real incident
   (wes-work-planning#67) from exactly this pattern; the
   `TestKafkaConsumerGroupNeverHardcodedInline` fitness test catches it
   in CI if present, but flag it here too since not every repo has that
   test yet.
7. **A newly reintroduced auth/bearer/JWT check.** Every REST/MCP
   endpoint in this fleet is deliberately unauthenticated (2026-09-11
   fleet-wide revert) — a reintroduced auth check is very likely
   accidental (an agent "helpfully" adding back something that looks
   missing) and should be flagged even if the code itself looks correct.
8. **Anything that would surprise the sibling-context boundary.** If this
   repo's `AGENTS.md`/`CLAUDE.md` documents a stricter rule (e.g. "no
   outbound calls to sibling contexts"), check the diff doesn't
   reintroduce exactly that.

## Output format

For each finding: file:line, a one-sentence description of the issue, and
a one-sentence suggested fix. Group findings by severity
(blocking/should-fix/nit). If nothing needs fixing, say so plainly — don't
manufacture findings to justify the review.

This command never modifies files or runs `git commit`/`git push`. It
only reports.
