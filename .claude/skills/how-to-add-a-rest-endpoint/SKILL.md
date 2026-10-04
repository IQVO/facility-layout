---
name: how-to-add-a-rest-endpoint
description: Add or change a REST endpoint in this service in the fleet's hexagonal order (domain invariant, use case, port, HTTP adapter, apis/openapi.yaml, generated docs, godog scenario). Use when touching internal/adapters/inbound/http, apis/openapi.yaml, or exposing a use case over HTTP.
---

# How to add a REST endpoint

Use when asked to add a new REST use case/endpoint to this service. Follow
this order — domain first, adapter last — never the reverse; writing the
HTTP handler before the domain invariant it enforces produces handlers
that validate nothing and use cases that get bypassed.

This walks the exact path `POST /zones/{zoneId}/cross-aisles` took
(`internal/application/usecases/register_cross_aisle.go` +
`internal/adapters/inbound/http/server.go`'s `handleRegisterCrossAisle`,
ADR-0017) as the concrete worked example — read those two files alongside
this guide. It also cites the read-only sibling endpoint
`GET /zones/{zoneId}/travel-graph` (`GetZoneTravelGraph`) since a read
model use case is shaped slightly differently (no `Events`/`Clock` field,
returns a view rather than an aggregate).

## 1. Domain first: does an invariant already exist, or do you need one?

Check `internal/domain/<aggregate>/` for the rule this endpoint enforces.
A REST endpoint should almost never contain business logic itself — it
decodes a request, calls a use case, encodes the result.
`RegisterCrossAisle` needed a genuinely new domain rule — a cross-aisle
connecting an aisle to itself is nonsensical — so
`internal/domain/aisle/cross_aisle.go`'s `NewCrossAisle` constructor
rejects it with `aisle.ErrCrossAisleSameAisle`, table-driven-tested in
`cross_aisle_test.go` BEFORE the application/adapter layers were touched.
Note what the constructor deliberately does NOT check: whether the two
aisle codes actually belong to the named zone. `NewCrossAisle` has no
repository access, so that chain-of-custody check has to live one layer
up — see step 2.

## 2. Application: define the use case

Add a new file in `internal/application/usecases/` (one file per use
case, this repo's convention — not one giant `usecases.go`). Shape,
mirroring `RegisterCrossAisle`:

```go
package usecases

type RegisterCrossAisle struct {
    Zones       ports.ZoneRepo
    Aisles      ports.AisleRepo
    CrossAisles ports.CrossAisleRepo
    Events      ports.EventPublisher
    Clock       ports.Clock
}

func (uc *RegisterCrossAisle) Execute(ctx context.Context, zoneID, fromAisleCode, toAisleCode, atBay string) (*aisle.CrossAisle, error) {
    // 1. load the zone via the port — ErrZoneNotFound if it doesn't exist
    // 2. the chain-of-custody check the constructor cannot do itself:
    //    both aisle codes must resolve to Aisles that exist IN THIS ZONE
    //    (ErrCrossAisleAisleMismatch otherwise)
    // 3. check for an existing cross-aisle between the same two aisles at
    //    the same bay, in EITHER direction (ErrDuplicateCrossAisle)
    // 4. call the domain constructor to apply the shape invariant
    // 5. persist via the port
    // 6. publish the domain event via Events
    // 7. return the result
}
```

A read model use case looks different — `GetZoneTravelGraph` has no
`Events`/`Clock` field at all (nothing is stored, nothing is published):

```go
type GetZoneTravelGraph struct {
    Zones       ports.ZoneRepo
    Aisles      ports.AisleRepo
    Slots       ports.SlotRepo
    CrossAisles ports.CrossAisleRepo
}

func (uc *GetZoneTravelGraph) Execute(ctx context.Context, zoneID string) (*TravelGraphView, error) {
    // built fresh from the zone's aisles, slots, and cross-aisles on
    // every call — never persisted separately
}
```

Add the port to `internal/application/ports/` if it doesn't exist yet —
ports are interfaces ONLY (`TestApplicationPortsContainOnlyInterfaces`-style
fitness test in `internal/architecture/architecture_test.go`'s "ports
package only contains interfaces" case enforces this; a struct or function
in the ports package fails CI).

Write the use case's unit test against the in-memory adapter
(`internal/adapters/outbound/memory/`) — never a real Postgres/HTTP call
in a unit test. `internal/application/usecases/travel_test.go`'s
`TestRegisterCrossAisle` covers five cases this way: success, unknown
zone, an aisle that doesn't belong to the zone, a same-aisle connection,
and a duplicate regardless of orientation (`A07,A08` then `A08,A07` at
the same bay — the reverse direction is still a duplicate). Cover the
success path AND every domain-rule failure path, not just the happy one.

## 3. Adapter: wire the HTTP handler

In `internal/adapters/inbound/http/`:

1. `dto.go` — add the request/response DTO structs (JSON tags, this
   repo's naming convention: `registerCrossAisleRequest`/
   `crossAisleResponse`). DTOs live ONLY in the adapter layer — domain
   types never carry JSON tags.
2. `server.go` — add the route (`r.Post("/{zoneId}/cross-aisles",
   s.handleRegisterCrossAisle)` inside the zones sub-router) and the
   handler function:
   - decode + validate the request (`decodeJSON`)
   - call the use case's `Execute`
   - map use-case errors to HTTP status via `writeError` (check
     `errors.go`'s existing switch statements before adding a new error
     type — `usecases.ErrCrossAisleAisleMismatch` maps to a 422 alongside
     `usecases.ErrNoRouteBetweenZones`, while `usecases.ErrDuplicateCrossAisle`
     maps to a 409 alongside the other duplicate-registration errors; a
     new error case usually belongs next to an existing sibling with the
     same HTTP semantics, not in a new switch arm)
   - encode the domain result back to the response DTO and `writeJSON`
     (`toCrossAisleResponse`/`toTravelGraphResponse` in `mappers.go`)
3. Add the new use case field to the `Server` struct
   (`GetZoneTravelGraph *usecases.GetZoneTravelGraph`,
   `EstimateTravelDistance *usecases.EstimateTravelDistance`) and wire it
   in the composition root (`cmd/facility/main.go`).

Write at least one httptest per endpoint: one success path, one error
path — see `internal/adapters/inbound/http/travel_test.go`.

## 4. Contract: update OpenAPI, then regenerate docs

Add the path to `apis/openapi.yaml` (request/response schemas, the RFC
7807 problem-detail response for each error case — see the existing
`/zones/{zoneId}/cross-aisles` entry, `operationId: registerCrossAisle`,
for the shape, including its 404/409/422 problem responses).

Regenerate the Docusaurus REST reference — this repo's `docs-api-drift`
CI job (`.github/workflows/ci.yml`) fails the PR if you skip this:

```bash
cd docs
npm run clean-api-docs
npm run gen-api-docs
```

## 5. Behaviour: add a godog scenario

If this endpoint is user-facing behaviour (not purely internal
plumbing), add a `.feature` file under `features/` exercising it
end-to-end against the real HTTP server — see `features/structure.feature`
for the exact Given/When/Then shape this repo's `bdd` CI job expects (real
HTTP, not mocked): registering a full chain from site to slot, then the
rejection paths (unknown aisle, decommissioned aisle, duplicate code).

## 6. Verify before opening the PR

```bash
make check       # fmt-check vet build lint test
make check-all    # + coverage (90% gate) + arch-test + bdd
```

`make coverage` gates `./internal/domain/...,./internal/application/...`
at 90% — a new use case with no test on its failure path is the most
common way to miss this gate. Also run `go test ./internal/architecture/...
-v` explicitly if you touched `internal/adapters/inbound/http/` — this
repo's fitness tests include `TestNoAuthMiddlewareReintroduced`
(`internal/architecture/fitness_test.go`), which fails the build if a new
handler reintroduces bearer/JWT auth; ADR-0014 added it and ADR-0015
reverted it fleet-wide, so a "helpful" auth middleware on a new endpoint
is a regression, not an improvement.
