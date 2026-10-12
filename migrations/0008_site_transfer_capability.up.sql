-- Site transfer capability (ADR-0035). Additive only: every existing site
-- starts "capable of nothing" at capability_revision 0 (no
-- SiteCapabilityChanged has ever been published for it), which is exactly
-- how network-inventory-planning already treats a site it has never heard
-- about (fail-closed).
--
-- version extends ADR-0025's optimistic-concurrency guard to sites: two
-- concurrent PUT /sites/{siteCode}/transfer-capability calls would otherwise
-- both load revision N and both commit revision N+1 with different values,
-- breaking the per-site monotonic revision consumers rely on. Existing rows
-- default to 1, exactly what a fresh aggregate's first Save writes.
ALTER TABLE sites ADD COLUMN transfer_origin_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE sites ADD COLUMN transfer_destination_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE sites ADD COLUMN capability_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
