import { useState, type FormEvent } from "react";
import { Card } from "@warehouse/ui-kit";
import { apiGet, apiPut, ApiError } from "../../api";
import { fmt, parseCentreline, parseDimensions, parsePoint3D } from "../../geometry";
import type { Aisle, LocationSlot, Point3D } from "../../types";
import { FormRow, InlineError, InlineSuccess, SubmitButton, TextField } from "../../components/formkit";

/** Geometry corrections (ADR-0017): fix what a survey got wrong on an
 *  EXISTING structure -- a slot's recorded position/footprint
 *  (PUT /locations/{locationCode}/geometry) and an aisle's travel
 *  centreline (PUT /zones/{zoneId}/aisles/{aisleCode}/geometry). Values
 *  are validated client-side to the spec's own ranges (x/y finite, z >= 0,
 *  dims strictly positive, distinct centreline endpoints) and any error
 *  the server still returns is surfaced verbatim. */

const DEFAULT_POSITION: Point3D = { xM: 0, yM: 0, zM: 0 };

function LocationGeometryCard() {
  const [codeInput, setCodeInput] = useState("");
  const [slot, setSlot] = useState<LocationSlot | null>(null);
  const [x, setX] = useState("");
  const [y, setY] = useState("");
  const [z, setZ] = useState("");
  const [width, setWidth] = useState("");
  const [depth, setDepth] = useState("");
  const [height, setHeight] = useState("");
  const [pickSequence, setPickSequence] = useState("");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);

  async function onLoad(e: FormEvent) {
    e.preventDefault();
    const code = codeInput.trim().toUpperCase();
    setLoadError(null);
    setError(null);
    setSuccess(null);
    setSlot(null);
    setLoading(true);
    try {
      const found = await apiGet<LocationSlot>(`/locations/${encodeURIComponent(code)}`);
      setSlot(found);
      const p = found.position ?? DEFAULT_POSITION;
      setX(fmt(p.xM));
      setY(fmt(p.yM));
      setZ(fmt(p.zM));
      setWidth(fmt(found.dimensions?.widthM));
      setDepth(fmt(found.dimensions?.depthM));
      setHeight(fmt(found.dimensions?.heightM));
      setPickSequence(found.pickSequence !== undefined ? String(found.pickSequence) : "");
    } catch (err) {
      setLoadError(err instanceof ApiError ? err.message : "Failed to load location.");
    } finally {
      setLoading(false);
    }
  }

  async function onSave(e: FormEvent) {
    e.preventDefault();
    if (!slot) return;
    setError(null);
    setSuccess(null);

    const pos = parsePoint3D("position", { xM: x, yM: y, zM: z });
    if ("error" in pos) {
      setError(pos.error);
      return;
    }
    const dims = parseDimensions({ widthM: width, depthM: depth, heightM: height });
    if ("error" in dims) {
      setError(dims.error);
      return;
    }
    let pick: number | undefined;
    if (pickSequence.trim() !== "") {
      pick = Number(pickSequence.trim());
      if (!Number.isInteger(pick) || pick < 0) {
        setError("pick sequence must be a non-negative integer");
        return;
      }
    }

    setSaving(true);
    try {
      const updated = await apiPut<LocationSlot>(
        `/locations/${encodeURIComponent(slot.locationCode)}/geometry`,
        {
          position: pos.point,
          dimensions: dims.dimensions,
          ...(pick !== undefined ? { pickSequence: pick } : {}),
        },
      );
      setSlot(updated);
      setSuccess(`Geometry saved for ${updated.locationCode}.`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to save geometry.");
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card title="Location geometry">
      <form onSubmit={onLoad} style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-3)" }}>
        <FormRow>
          <TextField
            label="Location code"
            value={codeInput}
            onChange={setCodeInput}
            placeholder="WH1-STOR-AMB-A07-03-02-B"
            required
          />
          <SubmitButton disabled={loading || !codeInput.trim()}>
            {loading ? "Loading…" : "Load current geometry"}
          </SubmitButton>
        </FormRow>
        <InlineError message={loadError} />
      </form>

      {slot && (
        <form
          onSubmit={onSave}
          style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-3)", marginTop: "var(--wh-space-4)" }}
        >
          <p style={{ margin: 0, color: "var(--wh-color-text-muted)", fontSize: "var(--wh-font-size-sm)" }}>
            Correcting <strong style={{ fontFamily: "var(--wh-font-mono)" }}>{slot.locationCode}</strong> — the
            slot must exist and not be decommissioned; position and dimensions are saved together.
          </p>
          <FormRow>
            <TextField label="x (m)" value={x} onChange={setX} type="number" required />
            <TextField label="y (m)" value={y} onChange={setY} type="number" required />
            <TextField label="z (m, height ≥ 0)" value={z} onChange={setZ} type="number" required />
          </FormRow>
          <FormRow>
            <TextField label="width (m, > 0)" value={width} onChange={setWidth} type="number" required />
            <TextField label="depth (m, > 0)" value={depth} onChange={setDepth} type="number" required />
            <TextField label="height (m, > 0)" value={height} onChange={setHeight} type="number" required />
            <TextField label="pick sequence (optional)" value={pickSequence} onChange={setPickSequence} type="number" />
            <SubmitButton disabled={saving}>{saving ? "Saving…" : "Save geometry"}</SubmitButton>
          </FormRow>
          <InlineError message={error} />
          <InlineSuccess message={success} />
        </form>
      )}
    </Card>
  );
}

function AisleGeometryCard() {
  const [zoneId, setZoneId] = useState("");
  const [aisleCode, setAisleCode] = useState("");
  const [aisle, setAisle] = useState<Aisle | null>(null);
  const [startX, setStartX] = useState("");
  const [startY, setStartY] = useState("");
  const [startZ, setStartZ] = useState("");
  const [endX, setEndX] = useState("");
  const [endY, setEndY] = useState("");
  const [endZ, setEndZ] = useState("");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);

  function path(): string | null {
    const z = zoneId.trim().toUpperCase();
    const a = aisleCode.trim().toUpperCase();
    if (!z || !a) return null;
    return `/zones/${encodeURIComponent(z)}/aisles/${encodeURIComponent(a)}/geometry`;
  }

  async function onLoad(e: FormEvent) {
    e.preventDefault();
    const z = zoneId.trim().toUpperCase();
    const a = aisleCode.trim().toUpperCase();
    setLoadError(null);
    setError(null);
    setSuccess(null);
    setAisle(null);
    setLoading(true);
    try {
      const found = await apiGet<Aisle>(`/zones/${encodeURIComponent(z)}/aisles/${encodeURIComponent(a)}`);
      setAisle(found);
      const c = found.centreline;
      setStartX(fmt(c?.start.xM));
      setStartY(fmt(c?.start.yM));
      setStartZ(fmt(c?.start.zM));
      setEndX(fmt(c?.end.xM));
      setEndY(fmt(c?.end.yM));
      setEndZ(fmt(c?.end.zM));
    } catch (err) {
      setLoadError(err instanceof ApiError ? err.message : "Failed to load aisle.");
    } finally {
      setLoading(false);
    }
  }

  async function onSave(e: FormEvent) {
    e.preventDefault();
    const target = path();
    if (!aisle || !target) return;
    setError(null);
    setSuccess(null);

    const line = parseCentreline(
      { xM: startX, yM: startY, zM: startZ },
      { xM: endX, yM: endY, zM: endZ },
    );
    if ("error" in line) {
      setError(line.error);
      return;
    }

    setSaving(true);
    try {
      const updated = await apiPut<Aisle>(target, {
        centreline: { start: line.start, end: line.end },
      });
      setAisle(updated);
      setSuccess(`Centreline saved for ${updated.aisleId}.`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to save centreline.");
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card title="Aisle centreline">
      <form onSubmit={onLoad} style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-3)" }}>
        <FormRow>
          <TextField
            label="Zone id"
            value={zoneId}
            onChange={setZoneId}
            placeholder="WH1-STOR-AMB"
            required
          />
          <TextField label="Aisle code" value={aisleCode} onChange={setAisleCode} placeholder="A07" required />
          <SubmitButton disabled={loading || !zoneId.trim() || !aisleCode.trim()}>
            {loading ? "Loading…" : "Load current centreline"}
          </SubmitButton>
        </FormRow>
        <InlineError message={loadError} />
      </form>

      {aisle && (
        <form
          onSubmit={onSave}
          style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-3)", marginTop: "var(--wh-space-4)" }}
        >
          <p style={{ margin: 0, color: "var(--wh-color-text-muted)", fontSize: "var(--wh-font-size-sm)" }}>
            Correcting the travel centreline of{" "}
            <strong style={{ fontFamily: "var(--wh-font-mono)" }}>{aisle.aisleId}</strong>
            {aisle.centreline ? "" : " — no centreline recorded yet"}. The two endpoints must differ.
          </p>
          <FormRow>
            <TextField label="start x (m)" value={startX} onChange={setStartX} type="number" required />
            <TextField label="start y (m)" value={startY} onChange={setStartY} type="number" required />
            <TextField label="start z (m, ≥ 0)" value={startZ} onChange={setStartZ} type="number" required />
          </FormRow>
          <FormRow>
            <TextField label="end x (m)" value={endX} onChange={setEndX} type="number" required />
            <TextField label="end y (m)" value={endY} onChange={setEndY} type="number" required />
            <TextField label="end z (m, ≥ 0)" value={endZ} onChange={setEndZ} type="number" required />
            <SubmitButton disabled={saving}>{saving ? "Saving…" : "Save centreline"}</SubmitButton>
          </FormRow>
          <InlineError message={error} />
          <InlineSuccess message={success} />
        </form>
      )}
    </Card>
  );
}

export function GeometryPanel() {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <LocationGeometryCard />
      <AisleGeometryCard />
    </div>
  );
}
