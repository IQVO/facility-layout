-- Site role (ADR-0037). Additive: a new site_role column on sites, then a
-- ONE-TIME data migration that classifies the sites that already exist.
--
-- Every site starts UNSPECIFIED (the registration default for a caller that
-- states no role). The backfill below maps the seeded display names, whose
-- pattern is '<CODE> - <Role words> (<city>)', to an explicit role ONCE, so
-- nothing ever derives behaviour from a display name at run time again. The
-- role words are the name with its leading '<CODE> - ' removed; a role
-- matches only when its exact phrase is followed by ' (' (the city), so
-- 'National Inbound Cross-Dock' never reads as 'Inbound Cross-Dock':
--
--   Robotics Sortable Fulfillment Center (...)    -> ROBOTICS_SORTABLE_FC
--   Non-Sortable Fulfillment Center (...)         -> NON_SORTABLE_FC
--   Traditional Sortable Fulfillment Center (...) -> TRADITIONAL_SORTABLE_FC
--   Sub-Same-Day Fulfillment Center (...)         -> SUB_SAME_DAY_FC
--   Fresh Grocery Fulfillment Center (...)        -> FRESH_GROCERY_FC
--   XL (Extra-Large) Fulfillment Center (...)     -> XL_FC
--   Soft-Lines Sortable Fulfillment Center (...)  -> SOFT_LINES_SORTABLE_FC
--   Micro-Fulfillment Center (...)                -> MICRO_FC
--   Simulated FC (...)                            -> SIMULATED_FC
--   Inbound Cross-Dock (...)                      -> INBOUND_CROSS_DOCK
--   National Inbound Cross-Dock (...)             -> NATIONAL_INBOUND_CROSS_DOCK
--   Reverse Logistics / Returns Center (...)      -> RETURNS_CENTER
--   Sortation Center (...)                        -> SORTATION_CENTER
--   Delivery Station (...)                        -> DELIVERY_STATION
--   Air Hub Gateway (...)                         -> AIR_HUB_GATEWAY
--   'Simulated FC' (the SIM1 site's whole name) -> SIMULATED_FC
--   anything else                                 -> UNSPECIFIED
--
-- 'Micro-Fulfillment Center (store-attached) (City)' matches through its
-- first ' ('. The backfill does not bump sites.version and publishes no
-- SiteRoleChanged: it records what the names already said; consumers read
-- the role on GET /sites. Later role changes go through
-- PUT /sites/{siteCode}/role, which does publish SiteRoleChanged. A role never
-- changes a site's transfer capability (ADR-0035 stays the only writer).
ALTER TABLE sites ADD COLUMN site_role TEXT NOT NULL DEFAULT 'UNSPECIFIED';

UPDATE sites AS s
SET site_role = m.site_role
FROM (
    SELECT n.code,
      CASE
        WHEN n.role_words LIKE 'Robotics Sortable Fulfillment Center (%' THEN 'ROBOTICS_SORTABLE_FC'
        WHEN n.role_words LIKE 'Non-Sortable Fulfillment Center (%' THEN 'NON_SORTABLE_FC'
        WHEN n.role_words LIKE 'Traditional Sortable Fulfillment Center (%' THEN 'TRADITIONAL_SORTABLE_FC'
        WHEN n.role_words LIKE 'Sub-Same-Day Fulfillment Center (%' THEN 'SUB_SAME_DAY_FC'
        WHEN n.role_words LIKE 'Fresh Grocery Fulfillment Center (%' THEN 'FRESH_GROCERY_FC'
        WHEN n.role_words LIKE 'XL (Extra-Large) Fulfillment Center (%' THEN 'XL_FC'
        WHEN n.role_words LIKE 'Soft-Lines Sortable Fulfillment Center (%' THEN 'SOFT_LINES_SORTABLE_FC'
        WHEN n.role_words LIKE 'Micro-Fulfillment Center (%' THEN 'MICRO_FC'
        WHEN n.role_words LIKE 'Simulated FC (%' THEN 'SIMULATED_FC'
        WHEN n.role_words LIKE 'Inbound Cross-Dock (%' THEN 'INBOUND_CROSS_DOCK'
        WHEN n.role_words LIKE 'National Inbound Cross-Dock (%' THEN 'NATIONAL_INBOUND_CROSS_DOCK'
        WHEN n.role_words LIKE 'Reverse Logistics / Returns Center (%' THEN 'RETURNS_CENTER'
        WHEN n.role_words LIKE 'Sortation Center (%' THEN 'SORTATION_CENTER'
        WHEN n.role_words LIKE 'Delivery Station (%' THEN 'DELIVERY_STATION'
        WHEN n.role_words LIKE 'Air Hub Gateway (%' THEN 'AIR_HUB_GATEWAY'
        WHEN n.role_words = 'Simulated FC' THEN 'SIMULATED_FC'
        ELSE 'UNSPECIFIED'
      END AS site_role
    FROM (SELECT code, regexp_replace(name, '^[A-Z0-9]+ - ', '') AS role_words FROM sites) AS n
) AS m
WHERE s.code = m.code AND s.site_role = 'UNSPECIFIED';

-- The domain is the authority on the role set; the CHECK keeps a hand-written
-- row from holding a value the service cannot rehydrate.
ALTER TABLE sites ADD CONSTRAINT sites_site_role_known CHECK (site_role IN (
    'UNSPECIFIED', 'ROBOTICS_SORTABLE_FC', 'NON_SORTABLE_FC', 'TRADITIONAL_SORTABLE_FC', 'SUB_SAME_DAY_FC', 'FRESH_GROCERY_FC', 'XL_FC', 'SOFT_LINES_SORTABLE_FC', 'MICRO_FC', 'SIMULATED_FC', 'INBOUND_CROSS_DOCK', 'NATIONAL_INBOUND_CROSS_DOCK', 'RETURNS_CENTER', 'SORTATION_CENTER', 'DELIVERY_STATION', 'AIR_HUB_GATEWAY'
));
