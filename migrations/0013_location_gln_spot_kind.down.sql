DROP TABLE IF EXISTS location_glns;

ALTER TABLE location_slots
    DROP CONSTRAINT IF EXISTS location_slots_spot_kind_yard_only,
    DROP CONSTRAINT IF EXISTS location_slots_spot_kind_known,
    DROP CONSTRAINT IF EXISTS location_slots_gln_shape,
    DROP COLUMN IF EXISTS spot_kind,
    DROP COLUMN IF EXISTS gln;

ALTER TABLE zones
    DROP CONSTRAINT IF EXISTS zones_gln_shape,
    DROP COLUMN IF EXISTS gln;

ALTER TABLE sites
    DROP CONSTRAINT IF EXISTS sites_gln_shape,
    DROP COLUMN IF EXISTS gln;
