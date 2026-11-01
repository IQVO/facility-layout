import { useState } from "react";
import { FACILITY_API_BASE } from "../config";
import type { LocationRole, Site, SiteLayout } from "../types";
import { Card, StatusPill, DataTable, useFetch } from "@warehouse/ui-kit";
import { useFetchAll } from "../useFetchAll";
import { RoleBadge, RoleFilterChips } from "../components/RoleBadge";

/**
 * Facility list + drill-down layout browser. GET /sites lists every
 * registered site as a table; selecting one (simple local state, no
 * router sub-route needed -- mirrors OrdersScreen's single-screen search
 * pattern) fetches GET /sites/{siteCode}/layout and renders the nested
 * zone -> aisle -> slot structure as a tree of Cards. facility-layout's
 * own CLAUDE.md describes this endpoint as pre-grouped, pre-ordered data
 * meant for a "drawable" floor-plan projection -- a clean nested-card
 * tree is the v1 rendering of that; a real floor-plan/grid visualization
 * is a natural fast-follow once this pilot is validated.
 *
 * Each slot chip carries a role badge (ADR-0016) -- Storage locations
 * render with a near-invisible marker, while Dock/Yard/WorkCenter/etc.
 * chips get a colored glyph, so a non-storage functional location is
 * visible at a glance in an otherwise dense wall of storage slots. The
 * role filter chip row lets an operator narrow the whole tree down to
 * just one role (e.g. "show me this site's dock doors") -- zones/aisles
 * with no matching slot are hidden entirely rather than shown empty.
 */
export function FacilityScreen() {
  const [selectedSite, setSelectedSite] = useState<string | null>(null);
  const [roleFilter, setRoleFilter] = useState<LocationRole | null>(null);

  const {
    data: sites,
    loading: sitesLoading,
    error: sitesError,
  } = useFetchAll<Site>(`${FACILITY_API_BASE}/sites`);

  const layoutUrl = selectedSite
    ? `${FACILITY_API_BASE}/sites/${encodeURIComponent(selectedSite)}/layout`
    : null;
  const { data: layout, loading: layoutLoading, error: layoutError } = useFetch<SiteLayout>(layoutUrl);

  // Apply the active role filter to the nested zone -> aisle -> slot tree:
  // a zone/aisle with no slot matching the filter is dropped entirely
  // rather than rendered empty, so narrowing to "Dock" collapses the tree
  // down to only the zones/aisles that actually have dock doors.
  const visibleZones = (layout?.zones ?? [])
    .map((zone) => ({
      ...zone,
      aisles: zone.aisles
        .map((aisle) => ({
          ...aisle,
          slots: roleFilter ? aisle.slots.filter((s) => s.role === roleFilter) : aisle.slots,
        }))
        .filter((aisle) => !roleFilter || aisle.slots.length > 0),
    }))
    .filter((zone) => !roleFilter || zone.aisles.length > 0);

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <div>
        <h1 style={{ fontSize: "var(--wh-font-size-2xl)", margin: 0 }}>Facility</h1>
        <p style={{ color: "var(--wh-color-text-muted)", marginTop: 4 }}>
          facility-layout · sites, zones, aisles &amp; coded storage slots
        </p>
      </div>

      {sitesError && (
        <Card>
          <div style={{ color: "var(--wh-color-status-danger)" }}>{sitesError.message}</div>
        </Card>
      )}

      <Card title="Sites">
        <DataTable
          rowKey={(s) => s.siteCode}
          rows={sites ?? []}
          loading={sitesLoading}
          onRowClick={(s) => setSelectedSite(s.siteCode)}
          emptyState={<span>No sites registered yet.</span>}
          columns={[
            { key: "siteCode", header: "Site code", render: (s) => s.siteCode },
            { key: "name", header: "Name", render: (s) => s.name },
            {
              key: "status",
              header: "Status",
              render: (s) => <StatusPill status={s.status} size="sm" />,
            },
          ]}
        />
      </Card>

      {selectedSite && (
        <Card
          title={`Layout — ${selectedSite}`}
          actions={
            <button
              type="button"
              onClick={() => setSelectedSite(null)}
              style={{
                padding: "6px 12px",
                borderRadius: "var(--wh-radius-md)",
                border: "1px solid var(--wh-color-border)",
                background: "transparent",
                color: "var(--wh-color-text-muted)",
                fontSize: "var(--wh-font-size-xs)",
                cursor: "pointer",
              }}
            >
              Close
            </button>
          }
        >
          {layoutError && (
            <div style={{ color: "var(--wh-color-status-danger)" }}>{layoutError.message}</div>
          )}

          {layoutLoading && (
            <div style={{ color: "var(--wh-color-text-muted)", fontSize: "var(--wh-font-size-sm)" }}>
              Loading layout…
            </div>
          )}

          {layout && !layoutLoading && (
            <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-4)" }}>
              <div
                style={{
                  display: "flex",
                  gap: "var(--wh-space-6)",
                  fontSize: "var(--wh-font-size-sm)",
                  color: "var(--wh-color-text-muted)",
                }}
              >
                <span>{layout.totals.zones} zones</span>
                <span>{layout.totals.aisles} aisles</span>
                <span>{layout.totals.slots} slots</span>
              </div>

              <RoleFilterChips active={roleFilter} onChange={setRoleFilter} />

              {layout.zones.length === 0 && (
                <div style={{ color: "var(--wh-color-text-muted)", fontSize: "var(--wh-font-size-sm)" }}>
                  No zones registered for this site yet.
                </div>
              )}

              {layout.zones.length > 0 && visibleZones.length === 0 && (
                <div style={{ color: "var(--wh-color-text-muted)", fontSize: "var(--wh-font-size-sm)" }}>
                  No {roleFilter} locations at this site.
                </div>
              )}

              {visibleZones.map((zone) => (
                <section
                  key={zone.zoneId}
                  style={{
                    border: "1px solid var(--wh-color-border-subtle)",
                    borderRadius: "var(--wh-radius-md)",
                    padding: "var(--wh-space-4)",
                    background: "var(--wh-color-bg-sunken)",
                  }}
                >
                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      justifyContent: "space-between",
                      marginBottom: "var(--wh-space-3)",
                    }}
                  >
                    <div>
                      <strong style={{ fontSize: "var(--wh-font-size-md)" }}>
                        {zone.areaCode}-{zone.zoneCode}
                      </strong>
                      <span
                        style={{
                          marginLeft: "var(--wh-space-2)",
                          color: "var(--wh-color-text-faint)",
                          fontSize: "var(--wh-font-size-xs)",
                        }}
                      >
                        {zone.zoneId}
                      </span>
                    </div>
                    <div style={{ display: "flex", gap: "var(--wh-space-2)" }}>
                      <StatusPill status={zone.temperatureClass} size="sm" />
                      {zone.hazmat && <StatusPill status="Hazmat" tone="danger" size="sm" />}
                      <StatusPill status={zone.status} size="sm" />
                    </div>
                  </div>

                  {zone.aisles.length === 0 ? (
                    <div
                      style={{
                        color: "var(--wh-color-text-faint)",
                        fontSize: "var(--wh-font-size-xs)",
                      }}
                    >
                      No aisles in this zone yet.
                    </div>
                  ) : (
                    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-2)" }}>
                      {zone.aisles.map((aisle) => (
                        <div
                          key={aisle.aisleId}
                          style={{
                            border: "1px solid var(--wh-color-border-subtle)",
                            borderRadius: "var(--wh-radius-sm)",
                            padding: "var(--wh-space-3)",
                            background: "var(--wh-color-bg-raised)",
                          }}
                        >
                          <div
                            style={{
                              display: "flex",
                              alignItems: "center",
                              justifyContent: "space-between",
                            }}
                          >
                            <span style={{ fontFamily: "var(--wh-font-mono)", fontWeight: 600 }}>
                              Aisle {aisle.aisleCode}
                            </span>
                            <div
                              style={{
                                display: "flex",
                                gap: "var(--wh-space-4)",
                                fontSize: "var(--wh-font-size-xs)",
                                color: "var(--wh-color-text-muted)",
                              }}
                            >
                              <span>seq {aisle.sequenceHint}</span>
                              <span>{aisle.direction}</span>
                              <StatusPill status={aisle.status} size="sm" />
                            </div>
                          </div>
                          {aisle.slots.length > 0 && (
                            <div
                              style={{
                                marginTop: "var(--wh-space-2)",
                                display: "flex",
                                flexWrap: "wrap",
                                gap: "var(--wh-space-2)",
                              }}
                            >
                              {aisle.slots.map((slot) => (
                                <span
                                  key={slot.locationCode}
                                  title={`${slot.locationType} · ${slot.role} · ${slot.status}`}
                                  style={{
                                    display: "inline-flex",
                                    alignItems: "center",
                                    gap: 4,
                                    fontFamily: "var(--wh-font-mono)",
                                    fontSize: "var(--wh-font-size-xs)",
                                    padding: "3px 8px",
                                    borderRadius: "var(--wh-radius-pill)",
                                    background: "var(--wh-color-bg-sunken)",
                                    border: "1px solid var(--wh-color-border-subtle)",
                                    color: "var(--wh-color-text-muted)",
                                  }}
                                >
                                  <RoleBadge role={slot.role} dockFlow={slot.dockFlow} />
                                  {slot.locationCode}
                                </span>
                              ))}
                            </div>
                          )}
                        </div>
                      ))}
                    </div>
                  )}
                </section>
              ))}
            </div>
          )}
        </Card>
      )}
    </div>
  );
}
