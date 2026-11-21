-- ADR-0048: one AS/RS is one logical location. An aisle holds at most one
-- active AutomatedStorage slot; the use case checks first, this index refuses
-- the concurrent race. Decommissioned slots do not count. No existing row can
-- violate it: no AutomatedStorage slot exists before this change.
CREATE UNIQUE INDEX uq_location_slots_automated_storage_per_aisle
    ON location_slots (aisle_id)
    WHERE location_type = 'AutomatedStorage' AND status <> 'Decommissioned';
