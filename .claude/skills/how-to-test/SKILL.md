---
name: how-to-test
description: Write or review tests and diagnose a failing coverage, mutation, bdd or integration CI job: the four test layers, the 90% gate, gremlins threshold semantics, the testcontainers rule. Use when adding tests, killing a surviving mutant, or fixing a red check.
---

# How to test

Use when writing or reviewing tests in this repo, or diagnosing a failing
`test`/`mutation-fast`/`bdd`/`integration` CI job
(`.github/workflows/ci.yml`). This fleet's quality bar is layered —
passing `go test` is necessary but is the WEAKEST signal; mutation
testing exists specifically because green tests can assert nothing.

## The four layers, in order of what they actually prove

1. **Unit tests** (`go test ./...`) — prove the code runs without
   panicking and returns SOMETHING. Table-driven, in-memory adapters
   only (`internal/adapters/outbound/memory/`), never a real network/DB
   call.
2. **Coverage** (`make coverage`, 90% gate on
   `./internal/domain/...,./internal/application/...`) — proves lines
   executed. Proves nothing about whether the test asserted the right
   thing.
3. **Mutation testing** (`make mutation`, gremlins, scoped to
   `internal/domain` — see `MUTATION.md`) — proves the tests actually
   ASSERT, not merely execute. A mutant is a deliberately broken version
   of the code (`<` -> `<=`, `+` -> `-`, etc.); if the test suite still
   passes against the mutant, it "survived" (LIVED) — meaning no test
   would catch that exact bug in production.
4. **BDD / behaviour** (`make bdd`, godog) — proves the use case works
   end-to-end through the real HTTP surface, not through a mocked port.
   See `features/structure.feature` for the shape.

## Mutation testing runs the WHOLE domain layer, not one package — and here's the actual math

`.gremlins.yaml`'s comment and `MUTATION.md` both record why:
narrowing the scope to make a mutation run cheap enough to block on was
measured here and rejected.

| scope | mutants | efficacy | runtime |
|-------|---------|----------|---------|
| `./internal/domain/slot` | 5 | 100.00% | 30s |
| `./internal/domain/placement` | 27 | 96.30% | 32s |
| **`./internal/domain` (chosen)** | **99** | **89.90%** | **~60s** |

`slot` is the richest package by behaviour but only yields 5 mutants —
useless as a regression detector. The whole layer gives 3.7x the
mutants of the best single package for ~2x the runtime, comfortably
inside `mutation-fast`'s `timeout-minutes: 20`. Both single packages
score ABOVE the gate, which is the tell: narrowing would set the
threshold against a subset that hides real survivors.

`.gremlins.yaml` fails on `<=` the threshold, not `>=` — read the
comment at the top of `.gremlins.yaml` for the exact current values
(efficacy >= 89, mutant-coverage >= 95) and when they were last
re-baselined against a measured run (89.90% efficacy / 100.00% mutator
coverage at baseline).

## Three real pitfalls, with this repo's own lived history

### 1. Zero/origin-value fixtures hide arithmetic mutants

`internal/domain/shared/geometry.go`'s `Point3D`/`Segment` are the real
example. `Point3D.IsZero()` deliberately does NOT mean "coordinates are
literally 0,0,0" — it means "no geometry was ever set" — precisely
because a fixture built at the literal origin makes `a - b` and `a + b`
degenerate. `NewSegment` even rejects identical start/end points for the
same reason (a zero-length centreline carries no travel-distance
information). When writing a fixture for `Segment.LengthM()` or
`DistanceToPoint`, every operand and every per-axis delta must be
distinct and non-zero, and the test must assert the exact expected
value — see `geometry_test.go`'s use of `t=0`/`t=1` boundary cases
specifically to prove the clamping logic at
`shared/geometry.go:173`/`175` rather than relying on origin-based
fixtures that would silently make `<` and `<=` produce the same answer.

### 2. Boundary guards need the boundary value itself

A guard like `if zM < 0 { return ErrInvalidZ }` (`geometry.go:29`,
`NewPoint3D`) needs an explicit test AT the boundary — `zM == 0` must
succeed, not error — or a `CONDITIONALS_BOUNDARY` mutant rewriting `<`
to `<=` survives silently. `MUTATION.md`'s "pre-existing benign
ASCII-boundary pattern" (`aisle/aisle.go:55`, `shared/location_code.go:75`,
`site/site.go:58`, `slot/functional.go:153`, `zone/zone.go:110`) is the
accepted counter-example: every uppercase-alphanumeric validator is
written as `(r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')`, and
killing those specific boundary mutants would mean testing the ASCII
table (accepting a segment of solely `A`/`Z`/`0`/`9`), not a domain
rule — accepted as noise, not a gap.

### 3. Tie-break / near-equivalent mutants: know when NOT to chase them

`internal/domain/travel/graph.go` is this repo's own real, currently
live example — not hypothetical. `MUTATION.md`'s triage names four
survivors here directly:

- `travel/graph.go:139` (`gaps > 0` in `bayDistance`) — guards the
  single-bay-aisle edge case; the boundary itself (`gaps == 0` vs
  `gaps == -1`) is unreachable since `len(a.Bays) >= 1`, making the
  mutant equivalent. Killed the reachable case instead with
  `TestGraphSingleBayAisleIgnoresCentreline`.
- `travel/graph.go:211` — Dijkstra's relaxation,
  `if candidate < dist[edge.To]`. A `<`→`<=` mutant here is
  undetectable by ANY test whose edge weights are all distinct — it
  only diverges on an exact tie.
- `travel/graph.go:233` — the path-reversal loop bound after Dijkstra
  finds a route.
- `travel/graph.go:250` (`priorityQueue.Less`, both
  `CONDITIONALS_BOUNDARY` and `CONDITIONALS_NEGATION`) — the min-heap
  comparison backing the same algorithm.

`graph_test.go`'s `TestGraphFourWaypointRoute` (and every other fixture
in that file) deliberately uses distinct edge weights throughout.
`MUTATION.md`'s own conclusion on these four: "Do NOT force an
artificial tied-weight fixture just to kill this; that pins an
arbitrary, currently-unspecified tie-break order as if it were a real
invariant, which is worse than an accepted near-equivalent survivor."
Document any new instance of this class in `MUTATION.md`'s triage
section and move on — don't burn a review cycle trying to kill a mutant
that only proves an implementation detail the domain never promised.

## Diagnosing a `mutation-fast` CI failure: diff against develop, don't chase every LIVED line

```bash
gremlins unleash ./internal/domain          # on your branch
git stash && git checkout origin/develop -- . && gremlins unleash ./internal/domain   # baseline
```

Only entries NEW on your branch are your regression. This repo already
carries a documented baseline of 19 accepted survivors in `MUTATION.md`
("Killed: 217, Lived: 19, ... Test efficacy: 91.95%, Mutator coverage:
96.33%" at the last recorded baseline run) — ten pre-existing
ASCII-boundary mutants, one pre-existing error-message-wording
near-equivalent, and the ADR-0017 geometry/travel-graph near-equivalents
described above (the four `travel/graph.go` tie-break/unreachable-boundary
lines plus `shared/geometry.go`'s clamping and final-`Sqrt` lines).
Confirming the survivor SET is unchanged from `origin/develop`, not just
that the percentage cleared the gate, is the real proof a change didn't
just get lucky on the threshold.

## Kafka/Postgres integration tests: testcontainers, never a skip-gate

A `-tags=integration` test touching Kafka or Postgres MUST start its own
container via `testcontainers-go`. Never gate on
`os.Getenv("KAFKA_BROKERS")` + `t.Skip(...)`, and never hardcode
`localhost:9092`. This fleet's CI `integration` job provisions Postgres
ONLY (see the `integration` job in `.github/workflows/ci.yml` — a
`postgres:16` service container, nothing for Kafka), so a skip-gated
Kafka test silently skips there and proves nothing. This repo's own
`internal/architecture/fitness_test.go`'s
`TestKafkaIntegrationTestsUseTestcontainers` enforces this statically by
scanning every `*_integration_test.go` for the banned shapes — see
how-to-add-an-integration-event.md for the full mechanics. This repo's
existing integration tests (`postgres_integration_test.go`,
`travel_integration_test.go`, `telemetry_integration_test.go`,
`repos_integration_test.go`, `geometry_integration_test.go`) are all
Postgres-only today, so that fitness test currently has nothing to flag
— it exists to catch the first Kafka-touching one.

## Verify before opening the PR

```bash
make check-all   # check + coverage + arch-test + bdd (the full local gate)
```

`make check-all` does NOT run `make mutation` or `make vuln` locally —
CI's `mutation-fast` and `vuln` jobs run on every push/PR regardless, so
a PR can pass your local `check-all` and still go red in CI on either.
Run both explicitly before pushing if you touched `internal/domain/` or
`go.mod`/`go.sum`:

```bash
make mutation    # gremlins unleash ./internal/domain
make vuln        # govulncheck ./...
```
