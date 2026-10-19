#!/usr/bin/env bash
# Contract-test the REST API with Schemathesis (property-based testing
# against apis/openapi.yaml): builds the service, boots it against the
# Postgres named by DATABASE_URL and the Kafka broker named by KAFKA_BROKERS
# on a loopback port, waits for /readyz, generates valid AND invalid requests
# for every operation, and asserts the responses conform to the spec (status
# codes, content types, response schemas; positive data accepted, negative
# data rejected).
#
# Mirrors the `contract` job in .github/workflows/ci.yml — same pinned
# Schemathesis version, same flags — so a local pass means a CI pass.
#
# This service has no in-memory mode (ADR 0039): cmd/facility requires
# DATABASE_URL (it migrates and serves from Postgres) AND KAFKA_BROKERS (the
# outbox relay and /readyz). CI provides a postgres:16 and a Kafka service
# container; for a local run, start disposable ones first (the broker must
# advertise the address you pass in KAFKA_BROKERS):
#
#   docker run -d --rm --name fl-contract-pg -p 18098:5432 \
#     -e POSTGRES_USER=facility -e POSTGRES_PASSWORD=facility \
#     -e POSTGRES_DB=facility postgres:16
#   docker run -d --rm --name fl-contract-kafka -p 19098:9092 \
#     -e KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://localhost:29092,PLAINTEXT_HOST://localhost:19098 \
#     confluentinc/confluent-local:7.6.1
#   export DATABASE_URL='postgres://facility:facility@localhost:18098/facility?sslmode=disable'
#   export KAFKA_BROKERS='localhost:19098'
#
# (credentials/URL shape mirror docker-compose.yml and the CI `contract`
# job's postgres service).
#
# Requires `st` on PATH:
#   python3 -m pip install --user 'schemathesis==4.28.0'
set -euo pipefail

SCHEMATHESIS_VERSION="4.28.0"
PORT="${CONTRACT_PORT:-18081}"
BASE_URL="http://127.0.0.1:${PORT}"
MAX_EXAMPLES="${CONTRACT_MAX_EXAMPLES:-100}"

if ! command -v st >/dev/null 2>&1; then
  echo "schemathesis (st) is not installed (or not on PATH)."
  echo "Install the exact version CI pins:"
  echo "  python3 -m pip install --user 'schemathesis==${SCHEMATHESIS_VERSION}'"
  exit 1
fi

if [ -z "${DATABASE_URL:-}" ]; then
  echo "DATABASE_URL is not set — this service has no in-memory mode"
  echo "(cmd/facility requires Postgres and applies migrations on boot)."
  echo "Start a disposable one and export DATABASE_URL, e.g.:"
  echo "  docker run -d --rm --name fl-contract-pg -p 18098:5432 \\"
  echo "    -e POSTGRES_USER=facility -e POSTGRES_PASSWORD=facility \\"
  echo "    -e POSTGRES_DB=facility postgres:16"
  echo "  export DATABASE_URL='postgres://facility:facility@localhost:18098/facility?sslmode=disable'"
  exit 1
fi

if [ -z "${KAFKA_BROKERS:-}" ]; then
  echo "KAFKA_BROKERS is not set — this service has no log sink and no optional relay"
  echo "(cmd/facility requires a real Kafka broker; /readyz depends on it)."
  echo "Start a disposable one and export KAFKA_BROKERS, e.g.:"
  echo "  docker run -d --rm --name fl-contract-kafka -p 19098:9092 \\"
  echo "    -e KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://localhost:29092,PLAINTEXT_HOST://localhost:19098 \\"
  echo "    confluentinc/confluent-local:7.6.1"
  echo "  export KAFKA_BROKERS='localhost:19098'"
  exit 1
fi

cd "$(dirname "$0")/.."
BIN="$(mktemp -d)/facility"
go build -o "$BIN" ./cmd/facility

# The old in-memory boot started EMPTY (no seed data), and so does this one:
# the migrations create the schema and insert no rows, so Schemathesis still
# starts from an empty facility map and creates what it needs itself.
LOG_LEVEL="${LOG_LEVEL:-warn}" HTTP_ADDR="127.0.0.1:${PORT}" "$BIN" &
SERVER_PID=$!
trap 'kill "$SERVER_PID" 2>/dev/null || true' EXIT

# Wait for /readyz: it is 200 only while Postgres answers a ping AND the
# broker answers a metadata request (up to ~60s: migrations + broker).
for _ in $(seq 1 300); do
  if curl -sf "${BASE_URL}/readyz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done
curl -sf "${BASE_URL}/readyz" >/dev/null # fail loudly if it never became ready

# Excluded operations — each carries a conditional constraint OpenAPI 3.0.3
# cannot express, so Schemathesis would generate schema-valid requests the
# API must reject. Every exclusion names where the conditional IS tested:
#
# registerLocationType: defaultCapacity is required iff the role's
#   RequiresCapacity() is true (Storage/Staging/Drop/Consolidation; role
#   defaults to Storage) and must be 0/0 for Dock/Yard/WorkCenter/QC/
#   Shipping. Tested by TestLocationRoleRequiresCapacity
#   (internal/domain/placement/role_test.go) and TestLocationTypesEndpoints
#   (internal/adapters/inbound/http/endpoints_test.go).
#
# registerLocationSlot: dockFlow is required iff the type's role is Dock,
#   activities iff it is WorkCenter, and placement-rule evaluation depends
#   on the live rule set — all cross-field/state constraints. Tested by
#   internal/domain/slot/location_slot_test.go plus
#   features/placement_rules.feature and features/error_contract.feature.
#
# importFacilityLayout: row-level violations are deliberately answered with
#   200 + a per-row report (partial-success contract), not 4xx, and rows
#   carry the same dockFlow/activities conditionals. Tested by
#   features/import.feature ("Importing a mixed layout reports partial
#   success per row") and internal/application/usecases/import_test.go.
#
# registerCrossAisle: fromAisle/toAisle must resolve to aisles OF the path's
#   zoneId — a cross-parameter constraint. Tested by
#   TestRegisterCrossAisleHTTP "422s on an aisle mismatch"
#   (internal/adapters/inbound/http/travel_test.go).
#
# registerZone (ADR-0017): bayPitchM/levelPitchM are both-or-neither — a
#   one-sided pitch is a 422 invalid-pitch — and JSON null means "not
#   sent". That ternary cross-field rule (absent/null/both) cannot be
#   encoded in OpenAPI 3.0.3: required-based oneOf branches misclassify
#   explicit nulls both ways. Tested by "422s on a one-sided pitch" and
#   "the effective pitches survive a read-back"
#   (internal/adapters/inbound/http/travel_read_test.go) and
#   internal/domain/zone pitch tests. The Zone response contract is still
#   exercised by listZones, getZone and getZoneGrid.
#
# setAisleGeometry: the centreline's start and end must differ (a zero-length
#   centreline carries no travel-distance information) — cross-field
#   equality, inexpressible in a schema. Tested by TestNewSegment
#   (internal/domain/shared/geometry_test.go) and TestSetAisleGeometry
#   "422, never 500, for coincident centreline endpoints"
#   (internal/adapters/inbound/http/geometry_test.go).
#
# allow_header_conformance is excluded as a CHECK (not an operation): chi
# answers OPTIONS /locations/import with `Allow: POST, GET` because the
# templated GET /locations/{locationCode} route also matches that concrete
# path (and that GET IS documented — under the templated path). The OpenAPI
# path model cannot express this shadowing, so the check false-positives
# here and only here.
# Idempotency-Key (ADR 0019, enforced in every deployment now): the hooks
# file gives every request that carries the header a fresh UUID, like a real
# client sending one per logical submit (see scripts/contract_hooks.py).
export SCHEMATHESIS_HOOKS="scripts/contract_hooks.py"
st run apis/openapi.yaml \
  --url "${BASE_URL}" \
  --max-examples "${MAX_EXAMPLES}" \
  --workers 4 \
  --exclude-checks allow_header_conformance \
  --exclude-operation-id registerLocationType \
  --exclude-operation-id registerLocationSlot \
  --exclude-operation-id importFacilityLayout \
  --exclude-operation-id registerCrossAisle \
  --exclude-operation-id setAisleGeometry \
  --exclude-operation-id registerZone
