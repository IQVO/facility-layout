---
paths:
  - ".github/**"
  - "Makefile"
  - ".gremlins.yaml"
  - ".golangci.yml"
  - "lefthook.yml"
  - "**/*_test.go"
  - "features/**"
---

# Testing discipline, Definition of Done, tech standards

## Tech & standards (IDENTICAL stack and quality bar to the other fleet services)

- Go 1.26, modules. Module path: `github.com/claudioed/facility-layout`.
- chi (`github.com/go-chi/chi/v5`), pgx/v5 + pgxpool, golang-migrate SQL
  migrations.
- Config via env (`DATABASE_URL`, `HTTP_ADDR` defaulting to `:8080`,
  `ANALYTICS_DATABASE_URL`, `EVENT_PUBLISHER`). `docker-compose.yml` for
  Postgres 16, matching the other repos' exact shape (service name
  `postgres`, healthcheck, named volume).
- Typed domain errors mapped to HTTP status in the adapter; RFC 7807
  `application/problem+json` for every error response from day one
  (ADR-0004) — never the old bespoke `{"error":...}` shape.
- Table-driven tests: domain + application (in-memory adapter); one
  httptest per endpoint; build-tagged Postgres integration test (skipped
  without `DATABASE_URL`).
- gofmt/go vet clean; every package has a doc comment.
- `.golangci.yml` — copied VERBATIM from `../inventory-storage/.golangci.yml`
  (same linters: errcheck, govet, staticcheck, unused, ineffassign,
  bodyclose, misspell, unconvert, gocritic; same test-file errcheck
  exclusion; gofmt+goimports formatters).

## Definition of done

- `go build ./...`, `go vet ./...`, `go test ./...` (and `-race`) all green.
- gofmt clean; `golangci-lint run ./...` zero issues (config copied from
  inventory-storage, see above).
- Unit test coverage >= 90% combined across `internal/domain/...` and
  `internal/application/...` (identical gate to the other fleet services).
- README.md: run steps (compose/migrate/go run), every endpoint with curl
  examples (including a full worked example: register a site, a zone, an
  aisle, a location type, a placement rule, a slot, then GET the layout
  and the grid and show the actual JSON), a layering note.
- These invariants each have a failing-path test: slot registration
  rejected when the Site/Zone/Aisle chain doesn't resolve or isn't Active;
  slot registration rejected when it violates a PlacementRule; duplicate
  LocationCode rejected; Zone/Aisle registration rejected against an
  unknown or Decommissioned parent.

## Local quality gate (run before every commit)

- **After making changes, run `make check`.** That is the fast
  self-correction loop: `fmt-check`, `vet`, `build`, `lint`, `test` — the
  same sensors CI runs, in well under a minute, with no database needed.
  Fix whatever it reports and re-run until it is green *before* you commit.
- **Before pushing, run `make check-all`** — `check` plus the 90%
  `coverage` gate, the arch-go `arch-test` fitness tests, and the `bdd`
  acceptance suite.
- `make vuln` runs `govulncheck ./...` (the supply-chain sensor, blocking
  in CI as the `vuln` job). Run it after touching `go.mod`/`go.sum`.
- `make mutation` runs gremlins over `./internal/domain` with the
  thresholds in `.gremlins.yaml` — the same command the blocking
  `mutation-fast` CI job runs. It takes a couple of minutes, so it is not
  part of `check`; run it when you change domain behaviour or domain
  tests.
- `make integration` and the slow scheduled `mutation` job are
  deliberately NOT in either bundle: the first needs `DATABASE_URL` and a
  live Postgres, the second takes too long for an edit loop.
- The lefthook git hooks enforce this automatically once someone has run
  `lefthook install` locally (pre-commit: `fmt-check` + `vet` + `lint`;
  pre-push: `make check`) — but run `make check` proactively rather than
  relying on the hook to catch you.

**Why this exists:** it keeps quality *left* (harness engineering) — every
sensor that used to fire only in CI, post-push, now fires locally so an
agent or a human can self-correct before the problem ever reaches a
reviewer or the pipeline.
