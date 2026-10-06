---
id: sequence-diagrams
title: UML sequence diagrams
sidebar_label: Sequence diagrams
sidebar_position: 9
description: UML sequence diagrams for every Facility Layout command use case — idempotency middleware, unit of work, optimistic concurrency, transactional outbox and relay, plus the analytics projector.
---

# UML sequence diagrams

Derived from the use-case function bodies in
`internal/application/usecases/` and the adapters they run through. All
diagrams show the **Postgres + `EVENT_PUBLISHER=kafka`** configuration, the
only one with a `UnitOfWork` and the transactional outbox
(`cmd/facility/main.go`, `outboxAdapters`). In the other configurations:

- no `DATABASE_URL` → in-memory repositories, no Idempotency-Key check, no
  transaction; the publisher is the log publisher or (with
  `EVENT_PUBLISHER=kafka`) a direct `FanOut` to both topics;
- `DATABASE_URL` without `kafka` → Postgres repositories and the
  Idempotency-Key check, but the log publisher and **no** `UnitOfWork`
  (`Save` and `Publish` run back to back, `atomically` with a nil unit of
  work).

Participants: **Client**, the **inbound adapter** (HTTP handler, MCP tool,
Kafka consumer), the **use case**, the **aggregate**, the **repositories**,
the **outbox / publisher** and the **downstream** topic.

## 1. Register a structural resource (site, zone, aisle, location type, placement rule, cross-aisle, fixed structure)

Seven creation use cases share one shape. `RegisterZone` is shown; the table
below lists how each differs.

```mermaid
sequenceDiagram
    autonumber
    actor C as Client
    participant MW as RequireIdempotencyKey
    participant H as HTTP handler
    participant UC as RegisterZone
    participant SR as SiteRepo
    participant Z as Zone
    participant ZR as ZoneRepo
    participant UOW as UnitOfWork
    participant OB as OutboxPublisher
    participant DB as Postgres

    C->>MW: POST /sites/WH1/zones with Idempotency-Key
    alt header missing
        MW-->>C: 400 idempotency-key-required
    end
    MW->>DB: BEGIN then INSERT idempotency_keys ON CONFLICT DO NOTHING
    alt key already exists
        MW->>DB: ROLLBACK then SELECT stored outcome
        alt same request hash
            MW-->>C: replay stored status, headers and body
        else different body
            MW-->>C: 422 idempotency-key-reused
        end
    end
    MW->>H: request with transaction in context
    H->>UC: Execute siteCode, areaCode, zoneCode, temperatureClass, hazmat, pitch
    UC->>SR: FindByCode WH1
    alt site missing or not Active
        UC-->>H: ErrSiteNotFound 404 or ErrSiteNotActive 409
    end
    UC->>Z: NewZone and optional SetPitch
    alt invalid code, temperature class or pitch
        Z-->>UC: domain error 400 or 422
    end
    UC->>ZR: FindByID WH1-STOR-AMB
    alt already exists
        UC-->>H: ErrDuplicateZone 409
    end
    UC->>UOW: Execute
    UOW->>UOW: joins the middleware transaction
    UC->>ZR: Save
    UC->>OB: Publish ZoneRegistered
    OB->>DB: INSERT outbox_events for events topic and analytics topic
    UC-->>H: Zone
    H-->>MW: 201 Created with Location
    MW->>DB: UPDATE idempotency_keys with outcome then COMMIT
    MW-->>C: 201 Created
```

Source: `internal/adapters/inbound/http/idempotency.go`
(`RequireIdempotencyKey`, `replayCachedResponse`, `runFreshRequest`),
`internal/application/usecases/register_zone.go`, `atomically.go`,
`internal/adapters/outbound/postgres/unit_of_work.go` (a nested `Execute`
joins the transaction already in the context),
`outbox_publisher.go`. Omitted: request JSON decoding errors (400), the
OpenTelemetry spans, and the relay (diagram 5).

| Use case | Route | Pre-checks before the save | Event |
|---|---|---|---|
| `RegisterSite` | `POST /sites` | `FindByCode` duplicate → `ErrDuplicateSite` | `SiteRegistered` |
| `RegisterZone` | `POST /sites/{siteCode}/zones` | site exists and Active; zone id unique | `ZoneRegistered` |
| `RegisterAisle` | `POST /zones/{zoneId}/aisles` | zone exists and Active; aisle id unique | `AisleRegistered` |
| `RegisterLocationType` | `POST /location-types` | name unique | `LocationTypeRegistered` |
| `DefinePlacementRule` | `POST /placement-rules` | location type exists; rule id unique | `PlacementRuleDefined` |
| `RegisterCrossAisle` | `POST /zones/{zoneId}/cross-aisles` | zone exists; both aisles in the zone; pair+bay unique | `CrossAisleRegistered` |
| `RegisterFixedStructure` | `POST /sites/{siteCode}/structures` | site exists; id unique | `FixedStructureRegistered` |

## 2. Register a location slot — the chain of custody

```mermaid
sequenceDiagram
    autonumber
    actor C as Client
    participant MW as RequireIdempotencyKey
    participant H as HTTP handler
    participant UC as RegisterLocationSlot
    participant R as Repositories
    participant S as LocationSlot
    participant RS as RuleSet
    participant UOW as UnitOfWork
    participant OB as OutboxPublisher
    participant M as LocationMetrics

    C->>MW: POST /locations with Idempotency-Key
    MW->>H: request with transaction in context
    H->>H: ParseLocationCode and NewCapacity for the override
    alt malformed code or bad capacity
        H-->>C: 400 malformed-location-code or 422 invalid-max-weight
    end
    H->>UC: Execute code, locationType, capacityOverride, dockFlow, activities
    UC->>R: SlotRepo.FindByCode
    alt code exists, even Decommissioned
        UC-->>H: ErrDuplicateLocationCode 409
    end
    UC->>R: SiteRepo, ZoneRepo, AisleRepo lookups - resolveChain
    alt any parent missing or not Active
        UC-->>H: 404 not-found or 409 not-active
    end
    UC->>R: LocationTypeRepo.FindByName
    alt unknown type
        UC-->>H: ErrLocationTypeNotFound 404
    end
    UC->>UC: functionalAttributesFor role, dockFlow, activities
    UC->>R: PlacementRuleRepo.List
    UC->>S: NewLocationSlot code, type, override, functional, zoneAttributes, rules
    S->>RS: Check locationType against zone attributes
    alt a Deny matches or the zone allow-list excludes the type
        RS-->>S: ErrPlacementRuleViolated naming the rule
        S-->>UC: 422 placement-rule-violated
    end
    UC->>UOW: Execute
    UC->>R: SlotRepo.Save
    UC->>OB: Publish LocationSlotRegistered
    UC->>M: LocationSlotRegistered outcome
    UC-->>H: LocationSlot
    H-->>C: 201 Created with Location /locations/CODE
```

Source: `internal/adapters/inbound/http/server.go`
(`handleRegisterLocationSlot`), `internal/application/usecases/register_location_slot.go`
(`Execute`, `register`, `resolveChain`, `functionalAttributesFor`,
`registrationOutcome`), `internal/domain/slot/location_slot.go`,
`internal/domain/placement/rules.go`. The metrics call runs for **every**
attempt, accepted or rejected (outcome `accepted`,
`rejected_by_placement_rule` or `rejected`). Omitted: the idempotency
branches (diagram 1) and the zone-mismatch and capacity-required errors.

## 3. Update a slot — decommission and geometry (optimistic concurrency)

```mermaid
sequenceDiagram
    autonumber
    actor C as Client
    participant H as HTTP handler
    participant UC as DecommissionLocationSlot or SetLocationGeometry
    participant SR as SlotRepo Postgres
    participant S as LocationSlot
    participant UOW as UnitOfWork
    participant OB as OutboxPublisher

    C->>H: POST /locations/CODE/decommission or PUT /locations/CODE/geometry
    H->>UC: Execute
    UC->>SR: FindByCode - loads row with version N
    alt not found
        UC-->>H: ErrLocationSlotNotFound 404
    end
    alt decommission
        UC->>S: Decommission
        alt already Decommissioned
            S-->>UC: ErrAlreadyDecommissioned 409
        end
    else geometry
        UC->>S: SetGeometry position, dimensions and SetPickSequence when given
        alt slot Decommissioned or invalid values
            S-->>UC: ErrSlotDecommissioned 409 or 422 invalid geometry
        end
    end
    UC->>UOW: Execute
    UOW->>SR: Save - upsert WHERE version = N, sets version N+1
    alt row version moved on
        SR-->>UC: ErrConcurrentModification 409 concurrent-modification
    end
    UC->>OB: Publish LocationSlotDecommissioned or LocationGeometryUpdated
    UC-->>H: ok
    H-->>C: 204 or 200
```

Source: `internal/application/usecases/decommission_location_slot.go`,
`set_location_geometry.go`, `internal/adapters/outbound/postgres/slot_repo.go`
(`Save`), [ADR 0025](../adr/0025-optimistic-concurrency-version-column.md).
Neither route is wrapped by the Idempotency-Key middleware
([ADR 0019](../adr/0019-idempotency-key-middleware.md)). `SetAisleGeometry`
(`PUT /zones/{zoneId}/aisles/{aisleCode}/geometry`) follows the same shape on
`Aisle.SetCentreline` without a version check (aisles are not versioned) and
publishes `AisleGeometryUpdated`. Omitted: request decoding and status-code
details of the handlers.

## 4. Bulk import — partial success

```mermaid
sequenceDiagram
    autonumber
    actor C as Client
    participant H as HTTP handler
    participant UC as ImportFacilityLayout
    participant R as Repositories
    participant RLS as RegisterLocationSlot
    participant SLG as SetLocationGeometry
    participant OB as OutboxPublisher

    C->>H: POST /locations/import rows
    H->>UC: Execute rows
    alt no rows
        UC-->>H: ErrEmptyImport 400
    end
    loop each row
        UC->>UC: NewLocationCode from the row segments
        UC->>R: ensureSite - reuse if Active, else create in its own unit of work
        R-->>OB: SiteRegistered when created
        UC->>R: ensureZone - same rule
        R-->>OB: ZoneRegistered when created
        UC->>R: ensureAisle - same rule
        R-->>OB: AisleRegistered when created
        UC->>RLS: Execute - full chain of custody, diagram 2
        RLS-->>OB: LocationSlotRegistered
        opt row carries geometry
            UC->>SLG: Execute
            SLG-->>OB: LocationGeometryUpdated
        end
        alt any step fails
            UC->>UC: record row error, rowsRejected plus 1
        else
            UC->>UC: slotsImported plus 1
        end
    end
    UC->>OB: Publish FacilityLayoutImported counts
    UC-->>H: ImportReport per row
    H-->>C: 200 with per-row results
```

Source: `internal/application/usecases/import_facility_layout.go`
(`Execute`, `importRow`, `ensureSite`, `ensureZone`, `ensureAisle`),
[ADR 0006](../adr/0006-partial-success-bulk-import.md). Each step is its own
`atomically` scope: a row that fails at the slot step keeps any parent it
already created. The route is not wrapped by the Idempotency-Key middleware,
and answers `200` even when every row was rejected (the per-row report
carries the failures — see [Bulk import](../api-reference/bulk-import.md)).
Omitted: request decoding errors.

## 5. Outbox relay and the downstream consumers

```mermaid
sequenceDiagram
    autonumber
    participant RL as OutboxRelay
    participant DB as Postgres outbox_events
    participant SK as RelaySink
    participant K as Kafka
    participant IS as inventory-storage
    participant PJ as facility-projector
    participant ADB as Analytics DB

    loop every OUTBOX_RELAY_INTERVAL, immediately again after a full batch
        RL->>DB: BEGIN, SELECT up to 100 unpublished ORDER BY id FOR UPDATE SKIP LOCKED
        loop each row in order
            RL->>SK: Send encoded CloudEvent to its own topic
            SK->>K: write with content-type application/cloudevents+json
            alt send fails
                RL->>DB: attempts plus 1, last_error, COMMIT, stop the pass
            else
                RL->>DB: published_at now
            end
        end
        RL->>DB: COMMIT
    end
    K-->>IS: warehouse.facility.events
    K-->>PJ: warehouse.facility.analytics, group facility-analytics
    PJ->>PJ: cloudevents.Decode - invalid goes to the .dlq topic
    PJ->>ADB: IsProcessed on the CloudEvents id
    PJ->>ADB: claim analytics_processed_events and UPSERT catalog_growth_rollup in one tx
    PJ->>ADB: MarkProcessed in analytics_consumed_events
    PJ->>K: commit offset
```

Source: `internal/adapters/outbound/postgres/outbox_relay.go`
(`Run`, `RelayOnce`), `internal/adapters/outbound/kafka/relay_sink.go`,
`internal/adapters/kafka/cloudevents/cloudevents.go`,
`internal/adapters/inbound/kafka/analytics_consumer.go`
(`handleMessage`, `HandleMessage`),
`internal/adapters/outbound/analyticsstore/postgres_projection.go`. Omitted:
the projector's bounded retry with backoff before dead-lettering
([ADR 0030](../adr/0030-dlq-bounded-retry-and-slog-otel-bridge.md)), the
housekeeping sweeper that later deletes published rows, and the
`warehouse-planning` consumer (same topic as `inventory-storage`).

## 6. Read models over REST and MCP

```mermaid
sequenceDiagram
    autonumber
    actor C as Client or MCP agent
    participant IN as HTTP handler or MCP tool
    participant UC as EstimateTravelDistance
    participant R as Repositories
    participant B as buildZoneGraph
    participant G as travel.Graph

    C->>IN: GET /distance?from=A&to=B or estimate_travel_distance
    IN->>UC: Execute from, to
    UC->>R: SlotRepo.FindByCode for both
    alt either missing
        UC-->>IN: ErrLocationSlotNotFound 404
    end
    alt different zones
        alt both slots have positions
            UC-->>IN: straight-line metres, estimated true
        else
            UC-->>IN: ErrNoRouteBetweenZones 422
        end
    else same zone
        UC->>B: zone, aisles, slots, cross-aisles of the zone
        B->>G: Build aisles, crossAisles, pitch
        UC->>G: Distance from node, to node
        alt no path or unknown waypoint
            G-->>UC: ErrNoRoute or ErrUnknownNode 422
        end
        G-->>UC: Route metres, estimated, nodes
    end
    UC-->>IN: TravelDistance
    IN-->>C: metresM, estimated, route
```

Source: `internal/application/usecases/estimate_travel_distance.go`,
`travel_graph_builder.go`, `internal/domain/travel/graph.go`,
`internal/adapters/inbound/mcp/tools.go`. The other read models
(`GetSiteLayout`, `GetZoneGrid`, `GetZoneTravelGraph`, `ListLocationsByRole`,
`GetLocationClassification` and the single-resource reads) follow the same
"load through repositories, assemble, return" shape with no transaction and
no event. Omitted: response DTO mapping.
