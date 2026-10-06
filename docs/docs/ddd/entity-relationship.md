---
id: entity-relationship
title: Entity-relationship diagram
sidebar_label: ER diagram
sidebar_position: 8
description: ER diagrams of Facility Layout's final OLTP schema (migrations 0001–0007) and its separate analytics schema, with the table-to-aggregate mapping.
---

# Entity-relationship diagram

The **final** schema after applying `migrations/*.up.sql` in order
(`0001_init` → `0007_slot_version`), and separately the analytics database
(`migrations/analytics/0001_report.up.sql`). Both are applied by
golang-migrate (`internal/adapters/outbound/postgres/migrate.go`), which
also creates its own `schema_migrations` bookkeeping table in each database.

A relationship line is drawn **only** where a real `FOREIGN KEY`
(`REFERENCES`) exists. Key markers: `PK` primary key, `FK` foreign key, `UK`
part of a `UNIQUE` constraint, `PK,FK` both.

## OLTP database (`DATABASE_URL`)

```mermaid
erDiagram
    sites {
        text code PK
        text name
        text status
    }
    zones {
        text id PK
        text site_code FK,UK "UNIQUE with area_code, zone_code"
        text area_code UK
        text zone_code UK
        text temperature_class
        boolean hazmat
        text status
        double_precision bay_pitch_m "nullable, > 0"
        double_precision level_pitch_m "nullable, > 0"
    }
    aisles {
        text id PK
        text zone_id FK,UK "UNIQUE with aisle_code"
        text aisle_code UK
        integer sequence_hint ">= 0"
        text direction
        text status
        double_precision centreline_start_x_m "all six centreline columns all-or-nothing"
        double_precision centreline_start_y_m
        double_precision centreline_start_z_m
        double_precision centreline_end_x_m
        double_precision centreline_end_y_m
        double_precision centreline_end_z_m
    }
    cross_aisles {
        text zone_id PK,FK
        text from_aisle PK "CHECK from_aisle differs from to_aisle"
        text to_aisle PK
        text at_bay PK
        boolean decommissioned "default false"
    }
    location_types {
        text name PK
        double_precision default_max_weight_kg "nullable, > 0"
        double_precision default_max_volume_m3 "nullable, > 0"
        text role "default Storage"
    }
    placement_rules {
        text id PK
        text location_type FK
        text effect
        text zone_code "nullable"
        text temperature_class "nullable"
        boolean hazmat "nullable"
    }
    location_slots {
        text code PK
        text site_segment
        text area_segment
        text zone_segment
        text aisle_segment
        text bay_segment
        text level_segment
        text position_segment
        text zone_id FK
        text aisle_id FK
        text location_type FK
        double_precision max_weight_kg "nullable, > 0"
        double_precision max_volume_m3 "nullable, > 0"
        text status
        text role "default Storage"
        text dock_flow "nullable"
        text_array activities "TEXT[], nullable"
        double_precision x_m "geometry all-or-nothing"
        double_precision y_m
        double_precision z_m ">= 0"
        double_precision width_m "> 0"
        double_precision depth_m "> 0"
        double_precision height_m "> 0"
        integer pick_sequence "nullable, >= 0"
        integer version "default 1, ADR 0025"
    }
    fixed_structures {
        text id PK
        text site_code FK
        text kind
        double_precision x_m
        double_precision y_m
        double_precision z_m ">= 0"
        double_precision width_m "> 0"
        double_precision depth_m "> 0"
        double_precision height_m "> 0"
        text label
    }
    outbox_events {
        bigserial id PK
        text topic
        text event_type
        bytea key
        bytea value
        timestamptz created_at
        timestamptz published_at "NULL until relayed"
        integer attempts
        text last_error
    }
    idempotency_keys {
        text key PK
        text method
        text path
        text request_hash
        integer status_code
        bytea response_body
        jsonb response_headers
        timestamptz created_at
        timestamptz completed_at
    }
    schema_migrations {
        bigint version PK
        boolean dirty
    }

    sites ||--o{ zones : "site_code"
    sites ||--o{ fixed_structures : "site_code"
    zones ||--o{ aisles : "zone_id"
    zones ||--o{ cross_aisles : "zone_id"
    zones ||--o{ location_slots : "zone_id"
    aisles ||--o{ location_slots : "aisle_id"
    location_types ||--o{ location_slots : "location_type"
    location_types ||--o{ placement_rules : "location_type"
```

Source: `migrations/0001_init.up.sql` … `migrations/0007_slot_version.up.sql`.
Omitted: indexes (`idx_zones_site_code`, `idx_aisles_zone_id`,
`idx_location_slots_aisle_id`, `idx_location_slots_zone_id`,
`idx_location_slots_grid`, `idx_location_slots_role`,
`idx_fixed_structures_site_code`, `idx_cross_aisles_zone_id`,
`idx_placement_rules_location_type`, partial
`idx_outbox_events_unpublished`, `idx_idempotency_keys_created_at`) and the
`events` table that `0001` created and `0005` dropped. Postgres types are
written with underscores (`double_precision`, `text_array` for `TEXT[]`)
because Mermaid attribute types must be one word.

### Logical links with no foreign key

These are real references the code relies on, deliberately **not** enforced
by a foreign key — each one is an aggregate boundary or an infrastructure
concern:

| From | To | Why no FK |
|---|---|---|
| `cross_aisles.from_aisle`, `to_aisle` | `aisles.aisle_code` (within `zone_id`) | They are aisle **codes**, not aisle ids; `RegisterCrossAisle` checks both belong to the zone (`ErrCrossAisleAisleMismatch`). |
| `location_slots.site_segment` … `position_segment` | `sites` / `zones` / `aisles` | Denormalised `LocationCode` segments for the zone-grid query; the chain is enforced by `RegisterLocationSlot.resolveChain` and the `zone_id` / `aisle_id` FKs. |
| `placement_rules.zone_code`, `temperature_class`, `hazmat` | `zones` columns | A predicate, not a reference: a rule matches **any** zone with those attributes. |
| `outbox_events` | any aggregate table | One row per encoded Kafka message; `event_type` and `key` name the source. |
| `idempotency_keys` | any aggregate table | Cached HTTP outcome keyed by the caller's `Idempotency-Key`. |

## Analytics database (`ANALYTICS_DATABASE_URL`)

Written only by `cmd/facility-projector`, read only by
`cmd/facility-reports` ([ADR 0010](../adr/0010-analytical-data-product.md)).
No foreign keys.

```mermaid
erDiagram
    analytics_processed_events {
        text event_id PK "CloudEvents id"
        timestamptz occurred_at
        timestamptz applied_at
    }
    analytics_consumed_events {
        text event_id PK "consumer dedupe gate"
        timestamptz processed_at
    }
    catalog_growth_rollup {
        text scope PK "site code, zone id, or empty"
        timestamptz day_bucket PK "midnight UTC"
        bigint sites_registered
        bigint zones_registered
        bigint aisles_registered
        bigint location_types_registered
        bigint placement_rules_defined
        bigint slots_registered
        bigint slots_decommissioned
        bigint bulk_imports
        bigint import_rows_submitted
        bigint import_rows_imported
        bigint import_rows_rejected
    }
    schema_migrations {
        bigint version PK
        boolean dirty
    }
```

Source: `migrations/analytics/0001_report.up.sql`. Omitted: indexes
`idx_analytics_processed_events_occurred_at`,
`idx_catalog_growth_rollup_day_bucket`.

## Table ≠ aggregate

| Table | Kind | Aggregate / owner | Notes |
|---|---|---|---|
| `sites` | aggregate | `Site` | |
| `zones` | aggregate | `Zone` | id = `SITE-AREA-ZONE` |
| `aisles` | aggregate | `Aisle` | id = `ZoneID-AISLE`; centreline columns are the `Segment` value object flattened |
| `cross_aisles` | aggregate | `CrossAisle` | composite natural key; status stored as a boolean |
| `location_types` | aggregate | `LocationType` | capacity columns are the `Capacity` value object flattened |
| `placement_rules` | aggregate | `PlacementRule` | predicate columns are the `ZonePredicate` value object flattened |
| `location_slots` | aggregate | `LocationSlot` | `LocationCode`, `Capacity`, `FunctionalAttributes`, `Point3D`, `Dimensions` flattened into one row; the only versioned table |
| `fixed_structures` | aggregate | `FixedStructure` | `Rect` flattened |
| `outbox_events` | infrastructure | transactional outbox ([ADR 0018](../adr/0018-transactional-outbox.md)) | published rows deleted after `OUTBOX_RETENTION` by the sweeper ([ADR 0026](../adr/0026-housekeeping-sweeper.md)) |
| `idempotency_keys` | infrastructure | Idempotency-Key middleware ([ADR 0019](../adr/0019-idempotency-key-middleware.md)) | deleted after `IDEMPOTENCY_KEY_TTL` by the sweeper |
| `schema_migrations` | infrastructure | golang-migrate | one per database |
| `analytics_processed_events` | projection infrastructure | analytics projection | claimed in the same transaction as the rollup UPSERT |
| `analytics_consumed_events` | projection infrastructure | analytics consumer dedupe gate | |
| `catalog_growth_rollup` | projection | Layout Catalog Growth and Change report | derived, rebuildable from the analytics topic |

This repository has **no** `processed_events` inbox table in the OLTP
database: the service consumes no other context's events.
