-- ADR-0045: optional GS1 Global Location Number on sites, zones and slots,
-- and an optional yard spot kind on slots. Every column is nullable: NULL =
-- none recorded, which is every row registered before this migration and
-- keeps today's behaviour. The CHECKs only guard the shape (13 digits, a known
-- kind); the GS1 mod-10 check digit is the domain's rule.
ALTER TABLE sites
    ADD COLUMN gln TEXT,
    ADD CONSTRAINT sites_gln_shape CHECK (gln IS NULL OR gln ~ '^[0-9]{13}$');

ALTER TABLE zones
    ADD COLUMN gln TEXT,
    ADD CONSTRAINT zones_gln_shape CHECK (gln IS NULL OR gln ~ '^[0-9]{13}$');

ALTER TABLE location_slots
    ADD COLUMN gln TEXT,
    ADD COLUMN spot_kind TEXT,
    ADD CONSTRAINT location_slots_gln_shape CHECK (gln IS NULL OR gln ~ '^[0-9]{13}$'),
    ADD CONSTRAINT location_slots_spot_kind_known CHECK (
        spot_kind IS NULL OR spot_kind IN ('PARKING', 'HOLDING', 'REEFER_PLUG', 'HAZMAT', 'STAGING_LANE')),
    ADD CONSTRAINT location_slots_spot_kind_yard_only CHECK (spot_kind IS NULL OR role = 'Yard');

-- One GLN identifies exactly one location across sites, zones and slots. A
-- single registry table is the only way a unique constraint can span the three
-- aggregates; `owner` is "site:<code>", "zone:<id>" or "slot:<code>" and is
-- unique too, so a location holds at most one GLN. Rows are inserted in the
-- same transaction as the owner (a rollback releases the claim).
CREATE TABLE location_glns (
    gln   TEXT PRIMARY KEY CHECK (gln ~ '^[0-9]{13}$'),
    owner TEXT NOT NULL UNIQUE
);
