import { useState, type FormEvent } from "react";
import { Card, StatusPill, useFetch } from "@warehouse/ui-kit";
import { useFetchAll } from "../useFetchAll";
import { ZoneCanvas } from "../components/ZoneCanvas";
import {
  FormRow,
  InlineError,
  InlineSuccess,
  SelectField,
  SubmitButton,
  TextField,
} from "../components/formkit";
import { apiPost, apiGet, ApiError } from "../api";
import { FACILITY_API_BASE } from "../config";
import type { GridCell, LocationType, Site, TravelDistance, Zone, ZoneGrid } from "../types";

interface PendingCell {
  columnIndex: number;
  rowIndex: number;
  aisleCode: string;
  bay: string;
  level: string;
}

interface ProbeCell {
  columnIndex: number;
  rowIndex: number;
  locationCode: string;
}

/**
 * The "draw the warehouse" screen: pick a Site, pick one of its Zones, and
 * see + build that Zone's grid as an actual 2D canvas (Konva), not a text
 * table. Clicking any cell -- filled or an empty gap -- opens a form
 * scoped to that exact (Aisle, Bay, Level) coordinate; only Position and
 * LocationType are still free choices, so registering a slot is "click
 * where it goes, name it, submit" rather than typing a 7-segment
 * LocationCode by hand.
 *
 * GET /zones/{zoneId}/grid is re-fetched after every successful
 * registration so the canvas always reflects what the backend actually
 * has (POST /locations enforces the full chain-of-custody + PlacementRule
 * checks per CLAUDE.md -- this screen never assumes success client-side).
 */
export function LayoutDesignerScreen() {
  const [siteCode, setSiteCode] = useState("");
  const [zoneId, setZoneId] = useState("");
  const [refreshKey, setRefreshKey] = useState(0);
  const [pending, setPending] = useState<PendingCell | null>(null);
  const [position, setPosition] = useState("");
  const [locationType, setLocationType] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Distance probe (ADR-0017's estimate_travel_distance surfaced on this
  // screen). A wholly separate interaction mode from the placement flow
  // above -- clicking the canvas either opens the "register a slot" form
  // OR sets the probe's from/to cell, never both at once, so the two
  // modes' click handling can never conflict on the same click.
  const [probeMode, setProbeMode] = useState(false);
  const [probeFrom, setProbeFrom] = useState<ProbeCell | null>(null);
  const [probeTo, setProbeTo] = useState<ProbeCell | null>(null);
  const [probeResult, setProbeResult] = useState<TravelDistance | null>(null);
  const [probeError, setProbeError] = useState<string | null>(null);
  const [measuring, setMeasuring] = useState(false);

  const { data: sites } = useFetchAll<Site>(`${FACILITY_API_BASE}/sites`);
  const { data: zones } = useFetchAll<Zone>(siteCode ? `${FACILITY_API_BASE}/sites/${encodeURIComponent(siteCode)}/zones` : null,
  );
  const { data: types } = useFetchAll<LocationType>(`${FACILITY_API_BASE}/location-types`);
  const { data: grid, loading: gridLoading, error: gridError } = useFetch<ZoneGrid>(
    zoneId ? `${FACILITY_API_BASE}/zones/${encodeURIComponent(zoneId)}/grid?_r=${refreshKey}` : null,
  );

  function onCellClick(args: { column: ZoneGrid["columns"][number]; level: string }, columnIndex: number, rowIndex: number, cell: GridCell | null) {
    if (probeMode) {
      setProbeError(null);
      setProbeResult(null);
      if (!cell || cell.positions.length === 0) {
        setProbeError("That cell has no registered slot to measure from/to yet.");
        return;
      }
      // A cell may hold several Positions (different letters at the same
      // aisle/bay/level); the probe measures between two SLOTS, so it
      // picks the first registered position at the clicked cell -- precise
      // enough for "roughly how far is aisle A07 bay 01 from aisle A09 bay
      // 03", the question this probe answers, without forcing the operator
      // to also pick a specific letter.
      const locationCode = cell.positions[0].locationCode;
      const clicked: ProbeCell = { columnIndex, rowIndex, locationCode };
      if (!probeFrom || (probeFrom && probeTo)) {
        // Nothing set yet, or a prior from/to pair is already complete --
        // start a fresh pair rather than silently overwriting just one side.
        setProbeFrom(clicked);
        setProbeTo(null);
      } else {
        setProbeTo(clicked);
      }
      return;
    }
    setError(null);
    setSuccess(null);
    setPosition("");
    setPending({
      columnIndex,
      rowIndex,
      aisleCode: args.column.aisleCode,
      bay: args.column.bay,
      level: args.level,
    });
  }

  async function onMeasure() {
    if (!probeFrom || !probeTo) return;
    setProbeError(null);
    setMeasuring(true);
    try {
      const result = await apiGet<TravelDistance>(
        `/distance?from=${encodeURIComponent(probeFrom.locationCode)}&to=${encodeURIComponent(probeTo.locationCode)}`,
      );
      setProbeResult(result);
    } catch (err) {
      setProbeError(err instanceof ApiError ? err.message : "Failed to measure travel distance.");
    } finally {
      setMeasuring(false);
    }
  }

  function onResetProbe() {
    setProbeFrom(null);
    setProbeTo(null);
    setProbeResult(null);
    setProbeError(null);
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!pending || !grid) return;
    setError(null);
    setSuccess(null);
    setSubmitting(true);
    const locationCode = [
      grid.zone.siteCode,
      grid.zone.areaCode,
      grid.zone.zoneCode,
      pending.aisleCode,
      pending.bay,
      pending.level,
      position.trim().toUpperCase(),
    ].join("-");
    try {
      await apiPost("/locations", { locationCode, locationType });
      setSuccess(`Slot ${locationCode} registered.`);
      setPending(null);
      setPosition("");
      setLocationType("");
      setRefreshKey((k) => k + 1);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to register slot.");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <div>
        <h1 style={{ fontSize: "var(--wh-font-size-2xl)", margin: 0 }}>Layout designer</h1>
        <p style={{ color: "var(--wh-color-text-muted)", marginTop: 4 }}>
          facility-layout · draw a zone's grid and place coded storage slots directly on the canvas
        </p>
      </div>

      <Card
        title="Choose a zone to draw"
        actions={
          <button
            type="button"
            onClick={() => {
              setProbeMode((m) => !m);
              setPending(null);
              onResetProbe();
            }}
            style={{
              padding: "6px 12px",
              borderRadius: "var(--wh-radius-md)",
              border: probeMode ? "1px solid #f5b942" : "1px solid var(--wh-color-border)",
              background: probeMode ? "rgba(245, 185, 66, 0.12)" : "transparent",
              color: probeMode ? "#f5b942" : "var(--wh-color-text-muted)",
              fontSize: "var(--wh-font-size-xs)",
              fontWeight: 600,
              cursor: "pointer",
            }}
          >
            {probeMode ? "Exit distance probe" : "Measure travel distance"}
          </button>
        }
      >
        <FormRow>
          <SelectField
            label="Site"
            value={siteCode}
            onChange={(v) => {
              setSiteCode(v);
              setZoneId("");
              setPending(null);
              onResetProbe();
            }}
            options={(sites ?? []).map((s) => ({ value: s.siteCode, label: `${s.siteCode} — ${s.name}` }))}
            required
          />
          <SelectField
            label="Zone"
            value={zoneId}
            onChange={(v) => {
              setZoneId(v);
              setPending(null);
              onResetProbe();
            }}
            options={(zones ?? []).map((z) => ({ value: z.zoneId, label: `${z.zoneId} (${z.temperatureClass})` }))}
            required
          />
        </FormRow>
      </Card>

      {gridError && (
        <Card>
          <InlineError message={gridError.message} />
        </Card>
      )}

      {probeMode && (
        <Card title="Distance probe">
          <p style={{ margin: "0 0 var(--wh-space-2)", fontSize: "var(--wh-font-size-sm)", color: "var(--wh-color-text-muted)" }}>
            Click a registered slot to set <strong style={{ color: "#3dd68c" }}>From</strong>, then click another to set{" "}
            <strong style={{ color: "#f5b942" }}>To</strong>. Calls facility-layout's own{" "}
            <code style={{ fontFamily: "var(--wh-font-mono)" }}>estimate_travel_distance</code> read model (ADR-0017) — this
            reports map topology only, never travel time or congestion.
          </p>
          <FormRow>
            <div style={{ fontSize: "var(--wh-font-size-xs)", fontFamily: "var(--wh-font-mono)", color: "var(--wh-color-text-faint)" }}>
              From: {probeFrom?.locationCode ?? "—"}
              <br />
              To: {probeTo?.locationCode ?? "—"}
            </div>
            <SubmitButton type="button" onClick={onMeasure} disabled={!probeFrom || !probeTo || measuring}>
              {measuring ? "Measuring…" : "Measure"}
            </SubmitButton>
            <button
              type="button"
              onClick={onResetProbe}
              style={{
                padding: "9px 16px",
                borderRadius: "var(--wh-radius-md)",
                border: "1px solid var(--wh-color-border)",
                background: "transparent",
                color: "var(--wh-color-text-muted)",
                fontSize: "var(--wh-font-size-sm)",
                cursor: "pointer",
                height: 36,
              }}
            >
              Reset
            </button>
          </FormRow>
          <InlineError message={probeError} />
          {probeResult && (
            <div style={{ marginTop: "var(--wh-space-3)", display: "flex", alignItems: "center", gap: "var(--wh-space-3)" }}>
              <span style={{ fontSize: "var(--wh-font-size-2xl)", fontWeight: 700 }}>
                {probeResult.metresM.toFixed(1)}m
              </span>
              {probeResult.estimated && (
                <StatusPill status="Estimated" tone="progress" size="sm" />
              )}
              <span style={{ fontSize: "var(--wh-font-size-xs)", color: "var(--wh-color-text-faint)" }}>
                {probeResult.route.length} waypoint{probeResult.route.length === 1 ? "" : "s"} on the shortest route
              </span>
            </div>
          )}
        </Card>
      )}

      {zoneId && (
        <Card
          title={grid ? `Grid — ${grid.zone.zoneId}` : "Grid"}
          actions={
            grid && (
              <div style={{ display: "flex", gap: "var(--wh-space-2)" }}>
                <StatusPill status={grid.zone.temperatureClass} size="sm" />
                {grid.zone.hazmat && <StatusPill status="Hazmat" tone="danger" size="sm" />}
              </div>
            )
          }
        >
          {gridLoading && (
            <div style={{ color: "var(--wh-color-text-muted)", fontSize: "var(--wh-font-size-sm)" }}>
              Loading grid…
            </div>
          )}
          {grid && !gridLoading && grid.columns.length === 0 && (
            <div style={{ color: "var(--wh-color-text-muted)", fontSize: "var(--wh-font-size-sm)" }}>
              No aisles registered for this zone yet — register one under Configure → Aisles first.
            </div>
          )}
          {grid && !gridLoading && grid.columns.length > 0 && (
            <ZoneCanvas
              grid={grid}
              selected={pending ? { columnIndex: pending.columnIndex, rowIndex: pending.rowIndex } : null}
              highlightFrom={probeFrom ? { columnIndex: probeFrom.columnIndex, rowIndex: probeFrom.rowIndex } : null}
              highlightTo={probeTo ? { columnIndex: probeTo.columnIndex, rowIndex: probeTo.rowIndex } : null}
              onCellClick={(args) => {
                const columnIndex = grid.columns.findIndex((c) => c === args.column);
                const rowIndex = grid.rows.findIndex((r) => r.level === args.level);
                const cell = grid.rows[rowIndex]?.cells[columnIndex] ?? null;
                onCellClick(args, columnIndex, rowIndex, cell);
              }}
            />
          )}
        </Card>
      )}

      {pending && grid && !probeMode && (
        <Card
          title={`Register a slot — Aisle ${pending.aisleCode} · Bay ${pending.bay} · Level ${pending.level}`}
          actions={
            <button
              type="button"
              onClick={() => setPending(null)}
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
              Cancel
            </button>
          }
        >
          <form onSubmit={onSubmit} style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-3)" }}>
            <FormRow>
              <TextField label="Position" value={position} onChange={setPosition} placeholder="B" required />
              <SelectField
                label="Location type"
                value={locationType}
                onChange={setLocationType}
                options={(types ?? []).map((t) => ({ value: t.name, label: t.name }))}
                required
              />
              <SubmitButton disabled={submitting || !position.trim() || !locationType}>
                {submitting ? "Registering…" : "Register slot"}
              </SubmitButton>
            </FormRow>
            <p style={{ margin: 0, fontSize: "var(--wh-font-size-xs)", color: "var(--wh-color-text-faint)", fontFamily: "var(--wh-font-mono)" }}>
              Location code: {grid.zone.siteCode}-{grid.zone.areaCode}-{grid.zone.zoneCode}-{pending.aisleCode}-{pending.bay}-{pending.level}-{position.trim().toUpperCase() || "?"}
            </p>
            <InlineError message={error} />
            <InlineSuccess message={success} />
          </form>
        </Card>
      )}
    </div>
  );
}
