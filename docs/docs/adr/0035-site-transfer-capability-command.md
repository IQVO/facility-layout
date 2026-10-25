---
id: 0035-site-transfer-capability-command
title: "35. Site transfer-capability command raises SiteCapabilityChanged"
sidebar_label: 35. Site transfer capability
sidebar_position: 35
description: "ADR 0035 — PUT /sites/{siteCode}/transfer-capability sets a site's transfer origin/destination capability on the Site aggregate, persists it with a per-site capability_revision behind a version guard, and raises SiteCapabilityChanged through the outbox. The event keeps its snake_case payload."
---

# 35. Site transfer-capability command raises SiteCapabilityChanged

## Status

**Accepted** (2026-10-12, gap-fix wave decisions D1 and D2). Extends
[ADR 0025](./0025-optimistic-concurrency-version-column.md)'s version guard
to the `Site` aggregate. Implements the `SiteCapabilityChanged` contract that
`apis/asyncapi.yaml` already declared. **Amended by
[ADR 0041](./0041-site-capability-heartbeat.md):** a periodic heartbeat also
re-publishes the current snapshot (revision + 1) so consumers' freshness
windows hold; the PUT's idempotent no-op below is unchanged, but the revision
is no longer a count of capability changes.

## Context

`com.warehouse.wms.facility-layout.site.SiteCapabilityChanged` was declared
in the AsyncAPI contract, had a domain event (`shared.NewSiteCapabilityChanged`),
a wire DTO (`internal/adapters/kafka/cloudevents/wire.go`), a partition key
(the site code), golden wire files and a contract fitness test. No use case,
REST route or MCP tool raised it. network-inventory-planning (NIP) consumes
it into its `site_capability` read model and approves an inter-warehouse
transfer only when the origin site may donate and the destination site may
receive. A site NIP has never heard about is capable of nothing, so with no
producer every transfer was refused.

Whether a building may act as a transfer origin or destination is site master
data. facility-layout owns the `Site` aggregate and is the only context that
may change it; NIP must not infer capability from layout topology.

The contract fixes the shape: a full last-write-wins snapshot
(`site_code`, `transfer_origin_enabled`, `transfer_destination_enabled`,
`capability_revision`) where consumers keep the highest revision. The payload
is snake_case while every other facility-layout payload is camelCase.

ADR 0025 left `sites` unversioned because `RegisterSite` was its only writer.
A capability command adds a read-modify-write on the same row. Two concurrent
changes would both load revision N and both commit revision N+1 with
different values; NIP keeps whichever arrives first at N+1 and drops the
other, while the `sites` row holds the last write. The read model and the
system of record would disagree.

## Decision

1. **The capability lives on the Site aggregate.** A `TransferCapability`
   value object (`originEnabled`, `destinationEnabled`) plus a
   `capabilityRevision` (int64). A new site is capable of nothing at
   revision 0, which means no snapshot was ever published.
   `Site.ChangeTransferCapability(c)`:
   - rejects a decommissioned site (`ErrSiteDecommissioned`, REST/MCP slug
     `site-not-active`, the slug used for the same condition elsewhere);
   - treats the first change as a change even when it restates the default,
     because nothing has been published yet;
   - treats an identical snapshot afterwards as a no-op (no revision, no
     event);
   - otherwise replaces the snapshot and advances the revision by exactly
     one.
2. **Command:** `PUT /sites/{siteCode}/transfer-capability` with body
   `{"transferOriginEnabled": bool, "transferDestinationEnabled": bool}`.
   Both fields are required: an omitted or null field is 400
   `missing-transfer-capability-field`, never a silent `false`. The response
   (200) is the resulting `{siteCode, transferOriginEnabled,
   transferDestinationEnabled, capabilityRevision}`. Unknown site: 404
   `site-not-found`. The PUT replaces a full snapshot and a repeat is a
   no-op, so it is idempotent by construction and is not wrapped by the
   Idempotency-Key middleware (ADR 0019), like the geometry PUTs.
   `GET /sites/{siteCode}/transfer-capability` returns the same shape.
3. **Use case** `ChangeSiteTransferCapability` loads the site, applies the
   change and, only when it produced a revision, saves the site and
   publishes `shared.NewSiteCapabilityChanged(now, code, origin,
   destination, revision)` inside one `atomically` unit of work. With
   Postgres and `EVENT_PUBLISHER=kafka` the event leaves through the
   transactional outbox (ADR 0018) as a CloudEvents 1.0 message (ADR 0024)
   on `warehouse.facility.events` and `warehouse.facility.analytics`, keyed
   and subjected by the site code. The analytics projector ignores it.
4. **Persistence and concurrency.** Migration
   `0008_site_transfer_capability` adds `transfer_origin_enabled`,
   `transfer_destination_enabled` (default false), `capability_revision`
   (default 0) and `version` (default 1) to `sites`. All additive.
   `SiteRepo.Save` becomes the ADR 0025 single-statement version-guarded
   upsert (`... ON CONFLICT (code) DO UPDATE ... WHERE sites.version = $n`,
   `RowsAffected() == 0` → `ports.ErrConcurrentModification`, REST 409
   `concurrent-modification`). The loser's outbox row rolls back with its
   transaction, so two committed snapshots never share a revision. There
   is no `If-Match` header: this repository's mutable resources use the
   server-side version column only (ADR 0025), and a full-snapshot PUT has
   no stale-field hazard for the caller.
5. **MCP:** `list_sites` entries (and the `site` header of
   `get_site_layout`) gain `transferOriginEnabled`,
   `transferDestinationEnabled` and `capabilityRevision`. No write tool is
   added; the MCP server stays read-only.
6. **Payload casing is kept (D2).** `SiteCapabilityChanged` keeps its
   snake_case `data` fields. It is in the AsyncAPI contract and NIP decodes
   exactly those names. Recasing them would be a breaking `.v2` with a
   dual-publish migration for no consumer benefit. The inconsistency with
   every other camelCase payload is recorded here and in the AsyncAPI
   message description; a future `.v2` of this event, if one is ever
   needed for another reason, should take the camelCase names then.

## Consequences

**Positive**

- NIP's `site_capability` read model receives facts, so transfer approval
  can succeed for sites an operator enabled.
- The revision is owned by the aggregate and guarded by the row version, so
  last-write-wins on `capability_revision` is safe: the newest snapshot NIP
  keeps is the one the `sites` row holds.
- A retried PUT never inflates the revision or republishes.

**Negative / accepted**

- A concurrent capability change of the same site gets 409 and must re-read
  and retry; there is no automatic retry (fleet pattern).
- Every `sites` write is now version-guarded. `RegisterSite` and
  `ImportFacilityLayout` only save sites they have just proved absent, so
  they see no behaviour change.
- `RehydrateSite` gained parameters; only the Postgres adapter calls it.
- One payload in this context stays snake_case.
- Existing sites start capable of nothing at revision 0. An operator must PUT
  each site that may take part in transfers; until then NIP refuses
  transfers involving it (fail-closed, as before).

## Verification

- Domain: `internal/domain/site/capability_test.go` (first change, idempotent
  repeat, +1 per change, decommissioned site rejected, rehydration).
- Use case: `internal/application/usecases/transfer_capability_test.go`
  (event payload and revision, no-op repeat, unknown/retired site,
  infrastructure failures).
- REST: `internal/adapters/inbound/http/transfer_capability_test.go`.
- BDD: `features/site_transfer_capability.feature`.
- MCP: `TestServer_ListSitesOverTheWire` asserts the capability fields.
- Integration (testcontainers Postgres,
  `internal/adapters/outbound/postgres/site_capability_integration_test.go`):
  the outbox row is the exact CloudEvent NIP decodes (type, subject, key,
  dataschema, snake_case `data` decoded through a hand-mirrored copy of NIP's
  DTO); an identical repeat writes no row; two concurrent changes of one
  site produce exactly one winner and one `ErrConcurrentModification`.
- Contract: `apis/openapi.yaml` (`getSiteTransferCapability`,
  `setSiteTransferCapability`) under the Schemathesis `contract` job.

## Related

- [ADR 0018](./0018-transactional-outbox.md), [ADR 0024](./0024-cloudevents-mandatory-envelope.md),
  [ADR 0025](./0025-optimistic-concurrency-version-column.md),
  [ADR 0019](./0019-idempotency-key-middleware.md).
- [Use cases](../ddd/use-cases.md), [Domain events](../ddd/domain-events.md),
  [Consuming this service](../ecosystem/consuming-this-service.md).
