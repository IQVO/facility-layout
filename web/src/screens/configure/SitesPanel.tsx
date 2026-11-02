import { useState, type FormEvent } from "react";
import { Card, DataTable, StatusPill } from "@warehouse/ui-kit";
import { useFetchAll } from "../../useFetchAll";
import { apiPost, ApiError } from "../../api";
import { FACILITY_API_BASE } from "../../config";
import type { Site } from "../../types";
import { FormRow, InlineError, InlineSuccess, SubmitButton, TextField } from "../../components/formkit";

/** RegisterSite: POST /sites -- the root of the hierarchy. Every Zone /
 *  Aisle / LocationSlot registration downstream depends on a Site already
 *  existing and Active, so this is necessarily the first configuration
 *  panel in the tab order. */
export function SitesPanel({ onChanged }: { onChanged?: () => void }) {
  const [siteCode, setSiteCode] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // useFetch has no refetch(); bump a cache-busting query param to force a
  // re-fetch after a successful mutation instead.
  const [refreshKey, setRefreshKey] = useState(0);
  const { data: sites, loading, error: listError } = useFetchAll<Site>(`${FACILITY_API_BASE}/sites`, refreshKey);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSuccess(null);
    setSubmitting(true);
    try {
      await apiPost<Site>("/sites", { siteCode: siteCode.trim(), name: name.trim() });
      setSuccess(`Site ${siteCode.trim()} registered.`);
      setSiteCode("");
      setName("");
      setRefreshKey((k) => k + 1);
      onChanged?.();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to register site.");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <Card title="Register a site">
        <form onSubmit={onSubmit} style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-3)" }}>
          <FormRow>
            <TextField
              label="Site code"
              value={siteCode}
              onChange={setSiteCode}
              placeholder="WH1"
              required
            />
            <TextField
              label="Name"
              value={name}
              onChange={setName}
              placeholder="Dallas Fulfillment Center"
              required
            />
            <SubmitButton disabled={submitting || !siteCode.trim() || !name.trim()}>
              {submitting ? "Registering…" : "Register site"}
            </SubmitButton>
          </FormRow>
          <InlineError message={error} />
          <InlineSuccess message={success} />
        </form>
      </Card>

      <Card title="Sites">
        {listError && <InlineError message={listError.message} />}
        <DataTable
          rowKey={(s) => s.siteCode}
          rows={sites ?? []}
          loading={loading}
          emptyState={<span>No sites registered yet.</span>}
          columns={[
            { key: "siteCode", header: "Site code", render: (s) => s.siteCode },
            { key: "name", header: "Name", render: (s) => s.name },
            { key: "status", header: "Status", render: (s) => <StatusPill status={s.status} size="sm" /> },
          ]}
        />
      </Card>
    </div>
  );
}
