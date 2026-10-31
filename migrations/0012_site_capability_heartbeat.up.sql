-- Site capability heartbeat (ADR-0041). Additive and nullable: when the last
-- SiteCapabilityChanged heartbeat was raised for the site. NULL means none
-- yet, so the first heartbeat pass after the upgrade refreshes every site
-- that already has a declared capability. Written only by the heartbeat
-- (through SiteRepo.Save, under the ADR-0025 version guard); a PUT leaves it
-- as loaded.
ALTER TABLE sites ADD COLUMN capability_heartbeat_at TIMESTAMPTZ;
