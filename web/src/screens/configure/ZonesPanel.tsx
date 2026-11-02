import { useState, type FormEvent } from "react";
import { Card, DataTable, StatusPill } from "@warehouse/ui-kit";
import { useFetchAll } from "../../useFetchAll";
import { apiPost, ApiError } from "../../api";
import { FACILITY_API_BASE } from "../../config";
import type { Site, Zone } from "../../types";
import {
  CheckboxField,
  FormRow,
  InlineError,
  InlineSuccess,
  SelectField,
  SubmitButton,
  TextField,
} from "../../components/formkit";

const TEMPERATURE_CLASSES = ["Ambient", "Chilled", "Frozen"];

/** RegisterZone: POST /sites/{siteCode}/zones -- a behavioral classification
 *  scoped to a Site (bundles the Area+Zone LocationCode segments). Requires
 *  an already-registered Site, so the site picker drives everything below
 *  it, mirroring the domain's own scoping rule (CLAUDE.md: "cannot register
 *  a Zone against an unknown or Decommissioned Site"). */
export function ZonesPanel() {
  const [refreshKey, setRefreshKey] = useState(0);
  const [siteCode, setSiteCode] = useState("");
  const [areaCode, setAreaCode] = useState("");
  const [zoneCode, setZoneCode] = useState("");
  const [temperatureClass, setTemperatureClass] = useState("Ambient");
  const [hazmat, setHazmat] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const { data: sites } = useFetchAll<Site>(`${FACILITY_API_BASE}/sites`);
  const { data: zones, loading, error: listError } = useFetchAll<Zone>(siteCode ? `${FACILITY_API_BASE}/sites/${encodeURIComponent(siteCode)}/zones` : null, refreshKey,
  );

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSuccess(null);
    setSubmitting(true);
    try {
      await apiPost<Zone>(`/sites/${encodeURIComponent(siteCode)}/zones`, {
        areaCode: areaCode.trim(),
        zoneCode: zoneCode.trim(),
        temperatureClass,
        hazmat,
      });
      setSuccess(`Zone ${areaCode.trim()}-${zoneCode.trim()} registered under ${siteCode}.`);
      setAreaCode("");
      setZoneCode("");
      setHazmat(false);
      setRefreshKey((k) => k + 1);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to register zone.");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <Card title="Register a zone">
        <form onSubmit={onSubmit} style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-3)" }}>
          <FormRow>
            <SelectField
              label="Site"
              value={siteCode}
              onChange={setSiteCode}
              options={(sites ?? []).map((s) => ({ value: s.siteCode, label: `${s.siteCode} — ${s.name}` }))}
              required
            />
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
          <FormRow>
            <SubmitButton disabled={submitting || !siteCode || !areaCode.trim() || !zoneCode.trim()}>
              {submitting ? "Registering…" : "Register zone"}
            </SubmitButton>
          </FormRow>
          <InlineError message={error} />
          <InlineSuccess message={success} />
        </form>
      </Card>

      <Card title={siteCode ? `Zones — ${siteCode}` : "Zones"}>
        {listError && <InlineError message={listError.message} />}
        <DataTable
          rowKey={(z) => z.zoneId}
          rows={zones ?? []}
          loading={loading}
          emptyState={<span>{siteCode ? "No zones registered for this site yet." : "Select a site above."}</span>}
          columns={[
            { key: "zoneId", header: "Zone id", render: (z) => z.zoneId },
            { key: "areaCode", header: "Area", render: (z) => z.areaCode },
            { key: "zoneCode", header: "Zone", render: (z) => z.zoneCode },
            { key: "temperatureClass", header: "Temp class", render: (z) => <StatusPill status={z.temperatureClass} size="sm" /> },
            { key: "hazmat", header: "Hazmat", render: (z) => (z.hazmat ? <StatusPill status="Hazmat" tone="danger" size="sm" /> : "—") },
            { key: "status", header: "Status", render: (z) => <StatusPill status={z.status} size="sm" /> },
          ]}
        />
      </Card>
    </div>
  );
}
