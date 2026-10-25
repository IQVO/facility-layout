-- ADR-0040: optional handling unit (EACH | CASE | PALLET) on a location
-- slot. Nullable: NULL = none recorded, which is every row registered before
-- this migration and keeps today's behaviour.
ALTER TABLE location_slots
    ADD COLUMN handling_unit TEXT,
    ADD CONSTRAINT location_slots_handling_unit_check
        CHECK (handling_unit IS NULL OR handling_unit IN ('EACH', 'CASE', 'PALLET'));
