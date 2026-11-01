import { useState, type FormEvent } from "react";
import { Card, DataTable, StatusPill } from "@warehouse/ui-kit";
import { useFetchAll } from "../../useFetchAll";
import { apiPost, ApiError } from "../../api";
import { FACILITY_API_BASE } from "../../config";
import type { Aisle, Site, Zone } from "../../types";
import {
  FormRow,
  InlineError,
  InlineSuccess,
  SelectField,
  SubmitButton,
  TextField,
} from "../../components/formkit";

const DIRECTIONS = ["OneWay", "TwoWay"];

/** RegisterAisle: POST /zones/{zoneId}/aisles -- a physical corridor scoped
 *  to a Zone, carrying SequenceHint (walk-order for travel-path
 *  optimization) and Direction. Site -> Zone cascading pickers mirror the
 *  domain scoping chain exactly. */
export function AislesPanel() {
  const [refreshKey, setRefreshKey] = useState(0);
  const [siteCode, setSiteCode] = useState("");
  const [zoneId, setZoneId] = useState("");
  const [aisleCode, setAisleCode] = useState("");
  const [sequenceHint, setSequenceHint] = useState("0");
  const [direction, setDirection] = useState("TwoWay");
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const { data: sites } = useFetchAll<Site>(`${FACILITY_API_BASE}/sites`);
  const { data: zones } = useFetchAll<Zone>(siteCode ? `${FACILITY_API_BASE}/sites/${encodeURIComponent(siteCode)}/zones` : null,
  );
  const { data: aisles, loading, error: listError } = useFetchAll<Aisle>(zoneId ? `${FACILITY_API_BASE}/zones/${encodeURIComponent(zoneId)}/aisles` : null, refreshKey,
  );

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSuccess(null);
    setSubmitting(true);
    try {
      await apiPost<Aisle>(`/zones/${encodeURIComponent(zoneId)}/aisles`, {
        aisleCode: aisleCode.trim(),
        sequenceHint: Number(sequenceHint) || 0,
        direction,
      });
      setSuccess(`Aisle ${aisleCode.trim()} registered under ${zoneId}.`);
      setAisleCode("");
      setSequenceHint("0");
      setRefreshKey((k) => k + 1);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to register aisle.");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <Card title="Register an aisle">
        <form onSubmit={onSubmit} style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-3)" }}>
          <FormRow>
            <SelectField
              label="Site"
              value={siteCode}
              onChange={(v) => {
                setSiteCode(v);
                setZoneId("");
              }}
              options={(sites ?? []).map((s) => ({ value: s.siteCode, label: `${s.siteCode} — ${s.name}` }))}
              required
            />
            <SelectField
              label="Zone"
              value={zoneId}
              onChange={setZoneId}
              options={(zones ?? []).map((z) => ({ value: z.zoneId, label: `${z.zoneId} (${z.temperatureClass})` }))}
              required
            />
            <TextField label="Aisle code" value={aisleCode} onChange={setAisleCode} placeholder="A07" required />
            <TextField
              label="Sequence hint"
              type="number"
              value={sequenceHint}
              onChange={setSequenceHint}
              placeholder="7"
              required
            />
            <SelectField
              label="Direction"
              value={direction}
              onChange={setDirection}
              options={DIRECTIONS.map((d) => ({ value: d, label: d }))}
              required
            />
          </FormRow>
          <FormRow>
            <SubmitButton disabled={submitting || !zoneId || !aisleCode.trim()}>
              {submitting ? "Registering…" : "Register aisle"}
            </SubmitButton>
          </FormRow>
          <InlineError message={error} />
          <InlineSuccess message={success} />
        </form>
      </Card>

      <Card title={zoneId ? `Aisles — ${zoneId}` : "Aisles"}>
        {listError && <InlineError message={listError.message} />}
        <DataTable
          rowKey={(a) => a.aisleId}
          rows={aisles ?? []}
          loading={loading}
          emptyState={<span>{zoneId ? "No aisles registered for this zone yet." : "Select a site and zone above."}</span>}
          columns={[
            { key: "aisleId", header: "Aisle id", render: (a) => a.aisleId },
            { key: "aisleCode", header: "Code", render: (a) => a.aisleCode },
            { key: "sequenceHint", header: "Seq", render: (a) => a.sequenceHint },
            { key: "direction", header: "Direction", render: (a) => a.direction },
            { key: "status", header: "Status", render: (a) => <StatusPill status={a.status} size="sm" /> },
          ]}
        />
      </Card>
    </div>
  );
}
