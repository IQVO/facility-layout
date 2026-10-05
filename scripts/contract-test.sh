#!/usr/bin/env bash
# Contract-test the REST API with Schemathesis (property-based testing
# against apis/openapi.yaml): builds the service, boots it with its
# in-memory adapters on a loopback port, waits for /healthz, generates
# valid AND invalid requests for every operation, and asserts the
# responses conform to the spec (status codes, content types, response
# schemas; positive data accepted, negative data rejected).
#
# Mirrors the `contract` job in .github/workflows/ci.yml — same pinned
# Schemathesis version, same flags — so a local pass means a CI pass.
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

cd "$(dirname "$0")/.."
BIN="$(mktemp -d)/facility"
go build -o "$BIN" ./cmd/facility

HTTP_ADDR="127.0.0.1:${PORT}" "$BIN" &
SERVER_PID=$!
trap 'kill "$SERVER_PID" 2>/dev/null || true' EXIT

# Wait for the server to report healthy (up to ~10s).
for _ in $(seq 1 50); do
  if curl -sf "${BASE_URL}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done
curl -sf "${BASE_URL}/healthz" >/dev/null # fail loudly if it never came up

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
