import { useMemo, useState, type FormEvent } from "react";
import { Card, StatusPill } from "@warehouse/ui-kit";
import { useFetchAll } from "../useFetchAll";
import { RackPlanCanvas, type PlannedCell } from "../components/RackPlanCanvas";
import {
  CheckboxField,
  FormRow,
  InlineError,
  InlineSuccess,
  SelectField,
  SubmitButton,
  TextField,
} from "../components/formkit";
import { apiPost, ApiError } from "../api";
import { FACILITY_API_BASE } from "../config";
import type { ImportReport, LocationType, Site, Zone } from "../types";

const TEMPERATURE_CLASSES = ["Ambient", "Chilled", "Frozen"];
const DIRECTIONS = ["OneWay", "TwoWay"];

function cellKey(bay: string, level: string, position: string): string {
  return `${bay}|${level}|${position}`;
}

/** Parses "01-04" (inclusive numeric range, zero-padded to the widest
 *  input width) or a comma list "01,02,05" into an ordered code array.
 *  This is deliberately forgiving -- a rack is rarely one clean sequence
 *  in real warehouses (a bay might be skipped for a support column). */
function parseCodes(raw: string): string[] {
  const trimmed = raw.trim();
  if (!trimmed) return [];
  const rangeMatch = trimmed.match(/^(\d+)\s*-\s*(\d+)$/);
  if (rangeMatch) {
    const [, startStr, endStr] = rangeMatch;
    const start = Number(startStr);
    const end = Number(endStr);
    const width = Math.max(startStr.length, endStr.length);
    const codes: string[] = [];
    for (let n = start; n <= end; n++) {
      codes.push(String(n).padStart(width, "0"));
    }
    return codes;
  }
  return trimmed
    .split(",")
    .map((s) => s.trim().toUpperCase())
    .filter(Boolean);
}

/**
 * Rack Planner: sketch a brand-new (or extend an existing) Site/Zone/Aisle
 * as a BLANK rack shape -- bay range x level range x positions -- entirely
 * client-side, then deploy the whole thing in one call to
 * POST /location-imports. This is the actual "draw a warehouse and then
 * deploy it" workflow: unlike the Layout Designer (which places one slot
 * at a time into an already-live grid), nothing here touches the backend
 * until Deploy is pressed, and Deploy is one atomic bulk call that creates
 * the Site/Zone/Aisle (if they don't already exist) and every planned slot
 * in a single round trip, with per-row partial-success reporting.
 */
export function RackPlannerScreen() {
  // Structural targets -- free text so a brand-new Site/Zone/Aisle can be
  // named here without visiting Configure first; existing codes are
  // reused rather than re-created (see ImportFacilityLayout's ensure*
  // semantics: a row naming an existing parent never mutates it).
  const [siteCode, setSiteCode] = useState("");
  const [siteName, setSiteName] = useState("");
  const [areaCode, setAreaCode] = useState("");
  const [zoneCode, setZoneCode] = useState("");
  const [temperatureClass, setTemperatureClass] = useState("Ambient");
  const [hazmat, setHazmat] = useState(false);
  const [aisleCode, setAisleCode] = useState("");
  const [sequenceHint, setSequenceHint] = useState("1");
  const [direction, setDirection] = useState("TwoWay");

  // Rack shape.
  const [bayRange, setBayRange] = useState("01-04");
  const [levelRange, setLevelRange] = useState("01-03");
  const [positions, setPositions] = useState("A,B");
  const [locationType, setLocationType] = useState("");

  const [cells, setCells] = useState<Map<string, PlannedCell>>(new Map());
  const [generated, setGenerated] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [report, setReport] = useState<ImportReport | null>(null);
  const [deploying, setDeploying] = useState(false);

  const { data: sites } = useFetchAll<Site>(`${FACILITY_API_BASE}/sites`);
  const { data: types } = useFetchAll<LocationType>(`${FACILITY_API_BASE}/location-types`);
  const { data: existingZones } = useFetchAll<Zone>(siteCode ? `${FACILITY_API_BASE}/sites/${encodeURIComponent(siteCode)}/zones` : null,
  );

  const bays = useMemo(() => parseCodes(bayRange), [bayRange]);
  const levels = useMemo(() => parseCodes(levelRange), [levelRange]);
  const positionList = useMemo(() => parseCodes(positions), [positions]);

  const includedCount = useMemo(
    () => Array.from(cells.values()).filter((c) => c.included).length,
    [cells],
  );

  function onGenerate(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setReport(null);
    if (bays.length === 0 || levels.length === 0 || positionList.length === 0) {
      setError("Bay range, level range and positions must each resolve to at least one code.");
      return;
    }
    const next = new Map<string, PlannedCell>();
    for (const bay of bays) {
      for (const level of levels) {
        for (const position of positionList) {
          next.set(cellKey(bay, level, position), {
            bay,
            level,
            position,
            locationType,
            included: true,
          });
        }
      }
    }
    setCells(next);
    setGenerated(true);
  }

  function onCellClick(bay: string, level: string) {
    setCells((prev) => {
      const next = new Map(prev);
      // Cycle every position at this (bay, level) together: click once to
      // exclude the whole cell, click again to bring it back -- keeps the
      // canvas interaction simple (one click = one coordinate) even though
      // a cell may hold several positions.
      const keysAtCoordinate = positionList.map((p) => cellKey(bay, level, p));
      const anyIncluded = keysAtCoordinate.some((k) => next.get(k)?.included);
      for (const key of keysAtCoordinate) {
        const existing = next.get(key);
        if (existing) next.set(key, { ...existing, included: !anyIncluded });
      }
      return next;
    });
  }

  async function onDeploy() {
    setError(null);
    setReport(null);
    setDeploying(true);
    try {
      const rows = Array.from(cells.values())
        .filter((c) => c.included)
        .map((c) => ({
          siteCode: siteCode.trim(),
          siteName: siteName.trim() || undefined,
          areaCode: areaCode.trim(),
          zoneCode: zoneCode.trim(),
          temperatureClass,
          hazmat,
          aisleCode: aisleCode.trim(),
          sequenceHint: Number(sequenceHint) || 0,
          direction,
          bay: c.bay,
          level: c.level,
          position: c.position,
          locationType: c.locationType,
        }));
      const result = await apiPost<ImportReport>("/location-imports", rows);
      setReport(result);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to deploy rack.");
    } finally {
      setDeploying(false);
    }
  }

  const canGenerate =
    siteCode.trim() && areaCode.trim() && zoneCode.trim() && aisleCode.trim() && locationType;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <div>
        <h1 style={{ fontSize: "var(--wh-font-size-2xl)", margin: 0 }}>Rack planner</h1>
        <p style={{ color: "var(--wh-color-text-muted)", marginTop: 4 }}>
          facility-layout · sketch a whole rack's shape, then deploy it in one bulk import
        </p>
      </div>

      <Card title="1. Where this rack lives">
        <form onSubmit={onGenerate} style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-3)" }}>
          <FormRow>
            <TextField
              label="Site code"
              value={siteCode}
              onChange={setSiteCode}
              placeholder="WH2 (new or existing)"
              required
              list="rack-planner-sites"
            />
            <TextField
              label="Site name (if new)"
              value={siteName}
              onChange={setSiteName}
              placeholder="Reno Fulfillment Center"
            />
          </FormRow>
          <datalist id="rack-planner-sites">
            {(sites ?? []).map((s) => (
              <option key={s.siteCode} value={s.siteCode}>
                {s.name}
              </option>
            ))}
          </datalist>
          <FormRow>
            <TextField label="Area code" value={areaCode} onChange={setAreaCode} placeholder="STOR" required />
            <TextField label="Zone code" value={zoneCode} onChange={setZoneCode} placeholder="AMB" required />
            <SelectField
              label="Temperature class"
              value={temperatureClass}
              onChange={setTemperatureClass}
              options={TEMPERATURE_CLASSES.map((t) => ({ value: t, label: t }))}
              required
            />
          </FormRow>
          <CheckboxField label="Hazmat zone" checked={hazmat} onChange={setHazmat} />
          {existingZones && existingZones.some((z) => z.areaCode === areaCode.trim() && z.zoneCode === zoneCode.trim()) && (
            <p style={{ margin: 0, fontSize: "var(--wh-font-size-xs)", color: "var(--wh-color-text-faint)" }}>
              This zone already exists — its temperature class/hazmat flag on file will be used as-is; the values above are ignored for an existing zone.
            </p>
          )}
          <FormRow>
            <TextField label="Aisle code" value={aisleCode} onChange={setAisleCode} placeholder="A09 (new or existing)" required />
            <TextField label="Sequence hint" type="number" value={sequenceHint} onChange={setSequenceHint} placeholder="9" />
            <SelectField
              label="Direction"
              value={direction}
              onChange={setDirection}
              options={DIRECTIONS.map((d) => ({ value: d, label: d }))}
            />
          </FormRow>

          <div style={{ height: 1, background: "var(--wh-color-border-subtle)" }} />

          <FormRow>
            <TextField label="Bay range" value={bayRange} onChange={setBayRange} placeholder="01-04" required />
            <TextField label="Level range" value={levelRange} onChange={setLevelRange} placeholder="01-03" required />
            <TextField label="Positions" value={positions} onChange={setPositions} placeholder="A,B" required />
            <SelectField
              label="Location type"
              value={locationType}
              onChange={setLocationType}
              options={(types ?? []).map((t) => ({ value: t.name, label: t.name }))}
              required
            />
          </FormRow>
          <FormRow>
            <SubmitButton disabled={!canGenerate}>Draw rack</SubmitButton>
          </FormRow>
          <InlineError message={error} />
        </form>
      </Card>

      {generated && (
        <Card
          title={`2. Review — ${bays.length} bays × ${levels.length} levels × ${positionList.length} positions`}
          actions={<StatusPill status={`${includedCount} planned`} tone="progress" size="sm" />}
        >
          <p style={{ margin: "0 0 var(--wh-space-3)", fontSize: "var(--wh-font-size-xs)", color: "var(--wh-color-text-faint)" }}>
            Nothing is saved yet. Click a cell to exclude/include it (e.g. a support column with no rack), then deploy.
          </p>
          <RackPlanCanvas bays={bays} levels={levels} cells={cells} onCellClick={onCellClick} />
          <FormRow>
            <SubmitButton type="button" onClick={onDeploy} disabled={deploying || includedCount === 0}>
              {deploying ? "Deploying…" : `Deploy ${includedCount} slot${includedCount === 1 ? "" : "s"}`}
            </SubmitButton>
          </FormRow>
        </Card>
      )}

      {report && (
        <Card title="Deploy report">
          <div style={{ display: "flex", gap: "var(--wh-space-5)", fontSize: "var(--wh-font-size-sm)", marginBottom: "var(--wh-space-3)" }}>
            <span>{report.rowsSubmitted} submitted</span>
            <span style={{ color: "var(--wh-color-status-success)" }}>{report.slotsImported} imported</span>
            {report.rowsRejected > 0 && (
              <span style={{ color: "var(--wh-color-status-danger)" }}>{report.rowsRejected} rejected</span>
            )}
          </div>
          {report.rowsRejected > 0 && (
            <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
              {report.results
                .filter((r) => !r.succeeded)
                .map((r) => (
                  <div key={r.index} style={{ fontSize: "var(--wh-font-size-xs)", fontFamily: "var(--wh-font-mono)", color: "var(--wh-color-status-danger)" }}>
                    {r.locationCode}: {r.error}
                  </div>
                ))}
            </div>
          )}
          {report.rowsRejected === 0 && <InlineSuccess message="Rack deployed — every planned slot was registered." />}
        </Card>
      )}
    </div>
  );
}
