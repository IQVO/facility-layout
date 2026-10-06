---
id: catalog-growth-report
title: Layout Catalog Growth & Change Report
sidebar_label: Catalog growth report
description: The facility-layout analytical data product — a Layout Catalog Growth & Change read model built from the service's own domain events, bucketed by day, served read-only over REST and MCP. Contract, grain, inputs, freshness SLA, and versioning.
---

# Layout Catalog Growth & Change Report

The analytical **data product** owned by facility-layout. It is built entirely
from this service's own domain events (never another service's database) and
served read-only. See [ADR-0010](../adr/0010-analytical-data-product.md) for the
decision.

## Name & owner

- **Report:** Layout Catalog Growth & Change.
- **Owner:** the facility-layout service/team (the same team that owns the OLTP
  write model — the "warehouse map").

## Why daily buckets

The warehouse map is a **slow-changing reference catalog** — a Generic Subdomain
the rest of the estate conforms to, not a high-frequency transactional stream.
Sites, zones, aisles, location types, placement rules and coded slots are
registered occasionally and change rarely once established. Bucketing by hour
would produce a sparse, mostly-empty rollup; bucketing by **UTC day** matches the
actual cadence of catalog change and keeps the report legible. This is the
deliberate difference from the fulfillment-execution throughput pilot, which
buckets by hour.

## Grain

One row per **(scope × day bucket)**, where `dayBucket` is the UTC day (midnight
UTC) the row aggregates and `scope` is:

- a **site code** (e.g. `WH1`) for site- and zone-level growth — a
  `SiteRegistered` or a `ZoneRegistered` is scoped to its site;
- a **zone id** (e.g. `WH1-STOR-AMB`) for aisle- and slot-level growth — an
  `AisleRegistered`, `LocationSlotRegistered` or `LocationSlotDecommissioned` is
  scoped to its zone (derived from the location code's first three segments);
- the **catalog-wide empty scope** (`""`) for definitions that are not tied to
  any one site or zone: `LocationTypeRegistered`, `PlacementRuleDefined`, and
  `FacilityLayoutImported`.

Metrics per row:

| Metric | Meaning |
|---|---|
| `sitesRegistered` | Count of `SiteRegistered` in the bucket. |
| `zonesRegistered` | Count of `ZoneRegistered`. |
| `aislesRegistered` | Count of `AisleRegistered`. |
| `locationTypesRegistered` | Count of `LocationTypeRegistered`. |
| `placementRulesDefined` | Count of `PlacementRuleDefined`. |
| `slotsRegistered` | Count of `LocationSlotRegistered`. |
| `slotsDecommissioned` | Count of `LocationSlotDecommissioned`. |
| `bulkImports` | Count of `FacilityLayoutImported`. |
| `importRowsSubmitted` | Sum of `rowsSubmitted` over the bucket's imports. |
| `importRowsImported` | Sum of `slotsImported` over the bucket's imports. |
| `importRowsRejected` | Sum of `rowsRejected` over the bucket's imports. |

Together these answer "how did the warehouse map grow and change over this
period, and where?"

## Inputs

The report is projected from the catalog-change event set on
`warehouse.facility.analytics`:

`SiteRegistered`, `ZoneRegistered`, `AisleRegistered`, `LocationTypeRegistered`,
`PlacementRuleDefined`, `LocationSlotRegistered`, `LocationSlotDecommissioned`,
`FacilityLayoutImported`.

Each arrives as a **CloudEvents 1.0** event in structured content mode
([ADR-0024](../adr/0024-cloudevents-mandatory-envelope.md)): `specversion`,
`id`, `source=/warehouse/facility-layout`, `type`
(`com.warehouse.wms.facility-layout.<entity>.<EventName>`), `subject`
(aggregate id), `time`, `datacontenttype=application/json` and
`dataschema=urn:warehouse:facility-layout:analytics:<EventName>:v1`, with the
Kafka header `content-type: application/cloudevents+json; charset=UTF-8`. The
`data` field carries the event's JSON wire DTO (built by the cloudevents adapter; the domain events carry no struct tags). The projector
**dispatches on the full `type` and ignores unknown types**, **dedupes on the
CloudEvents `id`**, and dead-letters anything that is not a valid CloudEvent.

## REST contract

Served by `cmd/facility-reports` (read-only):

### `GET /reports/catalog-growth`

Query parameters:

| Param | Required | Meaning |
|---|---|---|
| `from` | yes | Window start, inclusive, RFC3339. |
| `to` | yes | Window end, exclusive, RFC3339. |
| `scope` | no | Exact-match scope filter (site code, zone id, or omit for all). |
| `granularity` | no | Only `day` is supported (the default). |

Response:

```json
{
  "rows": [
    {
      "scope": "WH1-STOR-AMB",
      "dayBucket": "2026-06-01T00:00:00Z",
      "sitesRegistered": 0,
      "zonesRegistered": 0,
      "aislesRegistered": 3,
      "locationTypesRegistered": 0,
      "placementRulesDefined": 0,
      "slotsRegistered": 120,
      "slotsDecommissioned": 4,
      "bulkImports": 0,
      "importRowsSubmitted": 0,
      "importRowsImported": 0,
      "importRowsRejected": 0
    }
  ]
}
```

### `GET /reports/catalog-growth/freshness`

```json
{ "lagSeconds": 12.5 }
```

`lagSeconds` is how far the read model trails real time (now minus the most
recently applied event's `occurred_at`). It is `0` when the read model is empty.

## MCP tool

`get_facility_catalog_growth_report` (read-only) exposes the same
report to AI hosts. It calls the reports REST service — it never opens the
analytical database directly — so the single read path is preserved. It is
registered only when the MCP server is given `REPORTS_BASE_URL`.

## Freshness SLA

Eventually consistent by design. Target: **p95 event-to-report lag < 30s**. The
report is a projection rebuilt from events, not a real-time view; consumers must
tolerate this lag.

## Versioning

Every analytics event carries
`dataschema=urn:warehouse:facility-layout:analytics:<EventName>:v1`. The `data`
payloads evolve additively (new fields only). A breaking change publishes a new
`.v2` type with a `:v2` dataschema as a new contract, never an in-place change.
