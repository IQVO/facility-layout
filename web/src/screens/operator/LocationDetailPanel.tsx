import { useState, type FormEvent, type ReactNode } from "react";
import { Card, StatusPill } from "@warehouse/ui-kit";
import { apiGet, apiPost, ApiError } from "../../api";
import type { LocationSlot } from "../../types";
import { RoleBadge } from "../../components/RoleBadge";
import { InlineError, InlineSuccess, SubmitButton, TextField } from "../../components/formkit";

/** A labeled read-only field in the detail grid. */
function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
      <span style={{ fontSize: "var(--wh-font-size-xs)", color: "var(--wh-color-text-muted)", fontWeight: 600 }}>
        {label}
      </span>
      <span style={{ fontFamily: "var(--wh-font-mono)", fontSize: "var(--wh-font-size-sm)" }}>{children}</span>
    </div>
  );
}

/** Location detail (GET /locations/{locationCode}): the code exploded into
 *  its typed segments, the slot's classification (role + location type),
 *  its zone/aisle chain-of-custody, its recorded geometry, and -- behind
 *  an explicit two-step confirm -- POST /locations/{locationCode}/decommission.
 *  Decommission is one-way (a second call is a 409), which is exactly why
 *  the button demands a confirmation step; the server's RFC 7807
 *  problem detail is rendered verbatim for 404 (unknown code) and 409
 *  (already decommissioned) alike. */
export function LocationDetailPanel() {
  const [codeInput, setCodeInput] = useState("");
  const [slot, setSlot] = useState<LocationSlot | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [decommissionError, setDecommissionError] = useState<string | null>(null);
  const [decommissionSuccess, setDecommissionSuccess] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [loading, setLoading] = useState(false);
  const [working, setWorking] = useState(false);

  async function onLoad(e: FormEvent) {
    e.preventDefault();
    const code = codeInput.trim().toUpperCase();
    setLoadError(null);
    setDecommissionError(null);
    setDecommissionSuccess(null);
    setConfirming(false);
    setSlot(null);
    setLoading(true);
    try {
      const found = await apiGet<LocationSlot>(`/locations/${encodeURIComponent(code)}`);
      setSlot(found);
    } catch (err) {
      setLoadError(err instanceof ApiError ? err.message : "Failed to load location.");
    } finally {
      setLoading(false);
    }
  }

  async function onDecommission() {
    if (!slot) return;
    setDecommissionError(null);
    setDecommissionSuccess(null);
    setWorking(true);
    try {
      // apiPost mints the per-submit Idempotency-Key per the remote's
      // own convention (ADR-0019); a 204 with no body means done.
      await apiPost<void>(`/locations/${encodeURIComponent(slot.locationCode)}/decommission`, {});
      setDecommissionSuccess(`Location ${slot.locationCode} decommissioned. The transition is one-way.`);
      setConfirming(false);
      setSlot({ ...slot, status: "Decommissioned" });
    } catch (err) {
      setDecommissionError(err instanceof ApiError ? err.message : "Failed to decommission location.");
      setConfirming(false);
    } finally {
      setWorking(false);
    }
  }

  return (
    <Card title="Location detail">
      <form onSubmit={onLoad} style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-3)" }}>
        <div style={{ display: "flex", gap: "var(--wh-space-3)", flexWrap: "wrap", alignItems: "end" }}>
          <TextField
            label="Location code"
            value={codeInput}
            onChange={setCodeInput}
            placeholder="WH1-STOR-AMB-A07-03-02-B"
            required
          />
          <SubmitButton disabled={loading || !codeInput.trim()}>
            {loading ? "Loading…" : "Look up"}
          </SubmitButton>
        </div>
        <InlineError message={loadError} />
      </form>

      {slot && (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "var(--wh-space-4)",
            marginTop: "var(--wh-space-4)",
          }}
        >
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))",
              gap: "var(--wh-space-4)",
            }}
          >
            <Field label="Location code">{slot.locationCode}</Field>
            <Field label="Status">
              <StatusPill status={slot.status} size="sm" />
            </Field>
            <Field label="Role (classification)">
              <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
                <RoleBadge role={slot.role} dockFlow={slot.dockFlow} />
                {slot.role}
                {slot.dockFlow ? ` · ${slot.dockFlow}` : ""}
              </span>
            </Field>
            <Field label="Location type (classification)">{slot.locationType}</Field>
            {slot.activities && slot.activities.length > 0 && (
              <Field label="Activities">{slot.activities.join(", ")}</Field>
            )}
            <Field label="Zone">{slot.zoneId}</Field>
            <Field label="Aisle">{slot.aisleId}</Field>
            <Field label="Coordinates">
              {[
                slot.coordinates.site,
                slot.coordinates.area,
                slot.coordinates.zone,
                slot.coordinates.aisle,
                slot.coordinates.bay,
                slot.coordinates.level,
                slot.coordinates.position,
              ].join("-")}
            </Field>
            <Field label="Capacity">
              {slot.capacity.maxWeightKg} kg · {slot.capacity.maxVolumeM3} m³
            </Field>
            <Field label="Position (x/y/z m)">
              {slot.position
                ? `${slot.position.xM} / ${slot.position.yM} / ${slot.position.zM}`
                : "not recorded"}
            </Field>
            <Field label="Dimensions (w×d×h m)">
              {slot.dimensions
                ? `${slot.dimensions.widthM} × ${slot.dimensions.depthM} × ${slot.dimensions.heightM}`
                : "not recorded"}
            </Field>
            <Field label="Pick sequence">
              {slot.pickSequence !== undefined ? slot.pickSequence : "not set"}
            </Field>
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-3)" }}>
            {!confirming ? (
              <SubmitButton
                type="button"
                onClick={() => setConfirming(true)}
                disabled={working || slot.status === "Decommissioned"}
              >
                Decommission…
              </SubmitButton>
            ) : (
              <div
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: "var(--wh-space-2)",
                  padding: "12px",
                  borderRadius: "var(--wh-radius-md)",
                  border: "1px solid var(--wh-color-status-danger)",
                  background: "var(--wh-color-status-danger-bg)",
                  maxWidth: 560,
                }}
              >
                <span style={{ fontSize: "var(--wh-font-size-sm)" }}>
                  Decommission <strong style={{ fontFamily: "var(--wh-font-mono)" }}>{slot.locationCode}</strong>?
                  This permanently retires the slot: the transition is one-way, the code cannot be
                  re-registered afterwards, and inventory must be moved first.
                </span>
                <div style={{ display: "flex", gap: "var(--wh-space-2)" }}>
                  <SubmitButton type="button" onClick={onDecommission} disabled={working}>
                    {working ? "Decommissioning…" : "Confirm decommission"}
                  </SubmitButton>
                  <SubmitButton type="button" onClick={() => setConfirming(false)} disabled={working}>
                    Cancel
                  </SubmitButton>
                </div>
              </div>
            )}
            <InlineError message={decommissionError} />
            <InlineSuccess message={decommissionSuccess} />
          </div>
        </div>
      )}
    </Card>
  );
}
