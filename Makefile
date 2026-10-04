# facility-layout — local quality gate.
#
# These targets mirror the sensors in .github/workflows/ci.yml so the same
# feedback is available BEFORE a commit leaves the machine, not only after a
# push. `make check` is the fast self-correction loop; `make check-all` is the
# fuller pre-push gate.

GOLANGCI_LINT_VERSION := v2.13.1
GREMLINS_VERSION      := v0.6.0
COVERAGE_THRESHOLD    := 90
COVERPKG              := ./internal/domain/...,./internal/application/...

.DEFAULT_GOAL := help
.PHONY: help build vet fmt fmt-check lint test coverage integration bdd contract arch-test mutation vuln check check-all

help: ## Print the available targets
	@echo "facility-layout — make targets"
	@echo ""
	@echo "  help          print this list (default target)"
	@echo "  build         go build ./..."
	@echo "  vet           go vet ./..."
	@echo "  fmt           gofmt -w . (formats in place)"
	@echo "  fmt-check     fail if any file is not gofmt-clean"
	@echo "  lint          golangci-lint run ./... (pin: $(GOLANGCI_LINT_VERSION))"
	@echo "  test          go test ./... -race (unit + httptest + bdd; no DB needed)"
	@echo "  coverage      coverage run + $(COVERAGE_THRESHOLD)% gate over domain+application"
	@echo "  integration   go test -tags=integration ./... -race (NEEDS DATABASE_URL + Postgres)"
	@echo "  bdd           go test ./... -run TestFeatures -v (godog acceptance suite)"
	@echo "  contract      scripts/contract-test.sh (Schemathesis vs apis/openapi.yaml;"
	@echo "                boots the service in-memory; needs st: pip install"
	@echo "                'schemathesis==4.28.0')"
	@echo "  arch-test     go test ./internal/architecture/... -v (arch-go fitness tests)"
	@echo "  mutation      gremlins unleash ./internal/domain (same subset/threshold as CI mutation-fast)"
	@echo "  vuln          govulncheck ./... (supply-chain sensor)"
	@echo "  check         FAST pre-commit bundle: fmt-check vet build lint test"
	@echo "  check-all     check + coverage arch-test bdd (pre-push gate)"
	@echo ""
	@echo "  integration needs a running Postgres, e.g.:"
	@echo "    docker compose up -d postgres"
	@echo "    DATABASE_URL='postgres://facility:facility@localhost:5432/facility?sslmode=disable' make integration"

build: ## go build ./...
	go build ./...

vet: ## go vet ./...
	go vet ./...

fmt: ## gofmt -w .
	gofmt -w .

fmt-check: ## fail if gofmt -l . is non-empty
	@files=$$(gofmt -l .); \
	if [ -n "$$files" ]; then \
		echo "gofmt: the following files are not formatted:"; \
		echo "$$files"; \
		echo "run 'make fmt' to fix them"; \
		exit 1; \
	fi; \
	echo "gofmt: clean"

lint: ## golangci-lint run ./...
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "golangci-lint is not installed."; \
		echo "install the version CI pins with:"; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)"; \
		exit 1; \
	fi
	golangci-lint run ./...

test: ## go test ./... -race
	go test ./... -race

coverage: ## coverage run + gate, identical to the CI test job
	go test ./... -race -coverprofile=coverage.out -coverpkg=$(COVERPKG)
	@COVERAGE=$$(go tool cover -func=coverage.out | awk '/^total:/ {print $$3}' | tr -d '%'); \
	echo "Coverage: $${COVERAGE}% (gate: $(COVERAGE_THRESHOLD)%)"; \
	if awk -v c="$$COVERAGE" -v t="$(COVERAGE_THRESHOLD)" 'BEGIN { exit !(c < t) }'; then \
		echo "coverage $${COVERAGE}% is below the $(COVERAGE_THRESHOLD)% gate"; \
		exit 1; \
	fi

integration: ## build-tagged Postgres integration tests — needs DATABASE_URL + a running Postgres
	go test -tags=integration ./... -race -count=1

bdd: ## godog/Gherkin acceptance suite
	go test ./... -run TestFeatures -v

# Property-based contract tests against apis/openapi.yaml — mirrors the
# `contract` CI job. Not part of check/check-all (needs Python tooling
# installed); CI runs it as its own job on every push and PR.
contract: ## scripts/contract-test.sh (Schemathesis property-based contract tests)
	./scripts/contract-test.sh

arch-test: ## arch-go hexagonal fitness tests
	go test ./internal/architecture/... -v

mutation: ## gremlins on the domain layer, honouring .gremlins.yaml (same as the CI mutation-fast job)
	@if ! command -v gremlins >/dev/null 2>&1; then \
		echo "gremlins is not installed."; \
		echo "install the version CI pins with:"; \
		echo "  go install github.com/go-gremlins/gremlins/cmd/gremlins@$(GREMLINS_VERSION)"; \
		exit 1; \
	fi
	gremlins unleash ./internal/domain

vuln: ## govulncheck ./...
	@if ! command -v govulncheck >/dev/null 2>&1; then \
		echo "govulncheck is not installed."; \
		echo "install it with:"; \
		echo "  go install golang.org/x/vuln/cmd/govulncheck@latest"; \
		exit 1; \
	fi
	govulncheck ./...

check: fmt-check vet build lint test ## FAST pre-commit bundle
	@echo "make check: OK"

check-all: check coverage arch-test bdd ## fuller pre-push gate (still no DB, no mutation)
	@echo "make check-all: OK"

# --- agent harness (harness-template v3) -----------------------------------
.PHONY: check-fast guide-lint harness-test
# Fast local gate used by the agent Stop hook: format, vet, fitness tests, and the tests of
# the packages changed vs HEAD. The full gate stays `make check` / `make check-all`.
check-fast: fmt-check vet arch-test
	@pkgs="$$(python3 scripts/harness/hook.py changed-pkgs)"; \
	if [ -n "$$pkgs" ]; then go test $$pkgs; else echo "check-fast: no changed Go packages"; fi

guide-lint: ## lint agent guides: skills load, references resolve, context budget
	python3 scripts/harness/guide_lint.py

harness-test: ## unit-test the agent hooks (pre/post/stop)
	python3 scripts/harness/test_hook.py
