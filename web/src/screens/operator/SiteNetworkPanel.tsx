import { useState, type FormEvent } from "react";
import { Card, DataTable, StatusPill, useFetch } from "@warehouse/ui-kit";
import { apiGet, apiPut, ApiError } from "../../api";
import { FACILITY_API_BASE } from "../../config";
import { ASSIGNABLE_SITE_ROLES, siteRoleLabel } from "../../siteRoles";
import type { Site, SiteRoleTransferDefault, SiteTransferCapability } from "../../types";
import {
  CheckboxField,
  FormRow,
  InlineError,
  InlineSuccess,
  SelectField,
  SubmitButton,
  TextField,
} from "../../components/formkit";

/** Site network attributes (ADR-0037/0035): view and edit a site's role in
 *  the fulfilment network (PUT /sites/{siteCode}/role), view and edit
 *  whether it may donate/receive inter-site transfers
 *  (GET|PUT /sites/{siteCode}/transfer-capability), with the read-only
 *  role -> default-capability table (GET /site-roles/transfer-defaults)
 *  shown alongside as the reference it is: defaults are NEVER applied
 *  automatically -- the "Apply role's defaults" button is the explicit
 *  operator action that copies one row into the editable checkboxes. */
export function SiteNetworkPanel() {
  const [siteCodeInput, setSiteCodeInput] = useState("");
  const [site, setSite] = useState<Site | null>(null);
  const [capability, setCapability] = useState<SiteTransferCapability | null>(null);
  const [roleChoice, setRoleChoice] = useState("");
  const [origin, setOrigin] = useState(false);
  const [destination, setDestination] = useState(false);

  const [loadError, setLoadError] = useState<string | null>(null);
  const [roleError, setRoleError] = useState<string | null>(null);
  const [roleSuccess, setRoleSuccess] = useState<string | null>(null);
  const [capError, setCapError] = useState<string | null>(null);
  const [capSuccess, setCapSuccess] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [savingRole, setSavingRole] = useState(false);
  const [savingCap, setSavingCap] = useState(false);

  const { data: sites } = useFetch<Site[]>(`${FACILITY_API_BASE}/sites`);
  const { data: defaults } = useFetch<SiteRoleTransferDefault[]>(
    `${FACILITY_API_BASE}/site-roles/transfer-defaults`,
  );

  async function onLoadSite(e: FormEvent) {
    e.preventDefault();
    const code = siteCodeInput.trim().toUpperCase();
    setLoadError(null);
    setRoleError(null);
    setRoleSuccess(null);
    setCapError(null);
    setCapSuccess(null);
    setSite(null);
    setCapability(null);
    setLoading(true);
    try {
      const found = await apiGet<Site>(`/sites/${encodeURIComponent(code)}`);
      setSite(found);
      // A current role of UNSPECIFIED is not assignable, so the select
      // stays on its placeholder until the operator picks one.
      setRoleChoice(ASSIGNABLE_SITE_ROLES.some((r) => r.value === found.siteRole) ? found.siteRole : "");
      try {
        const cap = await apiGet<SiteTransferCapability>(
          `/sites/${encodeURIComponent(code)}/transfer-capability`,
        );
        setCapability(cap);
        setOrigin(cap.transferOriginEnabled);
        setDestination(cap.transferDestinationEnabled);
      } catch (err) {
        // GET /sites/{code}/transfer-capability 404s exactly like GET
        // /sites/{code}; the first error already says what is wrong.
        if (err instanceof ApiError && err.status !== 404) throw err;
      }
    } catch (err) {
      setLoadError(err instanceof ApiError ? err.message : "Failed to load site.");
    } finally {
      setLoading(false);
    }
  }

  async function onSaveRole() {
    if (!site) return;
    setRoleError(null);
    setRoleSuccess(null);
    setSavingRole(true);
    try {
      const updated = await apiPut<Site>(`/sites/${encodeURIComponent(site.siteCode)}/role`, {
        siteRole: roleChoice,
      });
      setSite(updated);
      setRoleSuccess(`Site ${updated.siteCode} role set to ${siteRoleLabel(updated.siteRole)}.`);
    } catch (err) {
      setRoleError(err instanceof ApiError ? err.message : "Failed to set site role.");
    } finally {
      setSavingRole(false);
    }
  }

  async function onSaveCapability() {
    if (!site) return;
    setCapError(null);
    setCapSuccess(null);
    setSavingCap(true);
    try {
      const updated = await apiPut<SiteTransferCapability>(
        `/sites/${encodeURIComponent(site.siteCode)}/transfer-capability`,
        // Both fields are always sent: the PUT is a full snapshot and the
        // server rejects an omitted field rather than reading it as false.
        { transferOriginEnabled: origin, transferDestinationEnabled: destination },
      );
      setCapability(updated);
      setCapSuccess(
        `Transfer capability saved (origin ${updated.transferOriginEnabled ? "on" : "off"}, ` +
          `destination ${updated.transferDestinationEnabled ? "on" : "off"}, revision ${updated.capabilityRevision}).`,
      );
    } catch (err) {
      setCapError(err instanceof ApiError ? err.message : "Failed to set transfer capability.");
    } finally {
      setSavingCap(false);
    }
  }

  /** Copies the loaded role's default row into the editable checkboxes --
   *  the only way defaults ever reach a site (ADR-0037: a role change
   *  never flips capability). */
  function applyRoleDefaults() {
    const row = defaults?.find((d) => d.siteRole === roleChoice);
    if (!row) return;
    setOrigin(row.transferOriginEnabled);
    setDestination(row.transferDestinationEnabled);
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <Card title="Site network">
        <form onSubmit={onLoadSite} style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-3)" }}>
          <FormRow>
            <TextField
              label="Site code"
              value={siteCodeInput}
              onChange={setSiteCodeInput}
              placeholder="WH1"
              list="site-network-sites"
              required
            />
            <SubmitButton disabled={loading || !siteCodeInput.trim()}>
              {loading ? "Loading…" : "Load site"}
            </SubmitButton>
          </FormRow>
          <datalist id="site-network-sites">
            {(sites ?? []).map((s) => (
              <option key={s.siteCode} value={s.siteCode} />
            ))}
          </datalist>
          <InlineError message={loadError} />
        </form>

        {site && (
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              gap: "var(--wh-space-4)",
              marginTop: "var(--wh-space-4)",
            }}
          >
            <div style={{ display: "flex", gap: "var(--wh-space-3)", alignItems: "center", flexWrap: "wrap" }}>
              <strong style={{ fontFamily: "var(--wh-font-mono)" }}>{site.siteCode}</strong>
              <span>{site.name}</span>
              <StatusPill status={site.status} size="sm" />
              <span style={{ color: "var(--wh-color-text-muted)", fontSize: "var(--wh-font-size-sm)" }}>
                current role: {siteRoleLabel(site.siteRole)}
              </span>
              {capability && (
                <span style={{ color: "var(--wh-color-text-muted)", fontSize: "var(--wh-font-size-sm)" }}>
                  capability revision {capability.capabilityRevision}
                </span>
              )}
            </div>

            <form
              onSubmit={(e) => {
                e.preventDefault();
                onSaveRole();
              }}
              style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-3)" }}
            >
              <FormRow>
                <SelectField
                  label="Site role (ADR-0037)"
                  value={roleChoice}
                  onChange={setRoleChoice}
                  options={ASSIGNABLE_SITE_ROLES.map((r) => ({ value: r.value, label: r.label }))}
                  placeholder="Choose a role…"
                  required
                />
                <SubmitButton type="submit" disabled={savingRole || !roleChoice}>
                  {savingRole ? "Saving…" : "Set role"}
                </SubmitButton>
              </FormRow>
              <InlineError message={roleError} />
              <InlineSuccess message={roleSuccess} />
            </form>

            <form
              onSubmit={(e) => {
                e.preventDefault();
                onSaveCapability();
              }}
              style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-3)" }}
            >
              <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                <span style={{ fontSize: "var(--wh-font-size-xs)", color: "var(--wh-color-text-muted)", fontWeight: 600 }}>
                  Transfer capability (ADR-0035)
                </span>
                <CheckboxField
                  label="Transfer origin — this site may donate stock to another site"
                  checked={origin}
                  onChange={setOrigin}
                />
                <CheckboxField
                  label="Transfer destination — this site may receive stock from another site"
                  checked={destination}
                  onChange={setDestination}
                />
              </div>
              <FormRow>
                <SubmitButton type="submit" disabled={savingCap}>
                  {savingCap ? "Saving…" : "Save capability"}
                </SubmitButton>
                <SubmitButton type="button" onClick={applyRoleDefaults} disabled={!roleChoice}>
                  Apply role&apos;s defaults
                </SubmitButton>
              </FormRow>
              <InlineError message={capError} />
              <InlineSuccess message={capSuccess} />
            </form>
          </div>
        )}
      </Card>

      <Card title="Role transfer defaults (reference)">
        <p style={{ color: "var(--wh-color-text-muted)", marginTop: 0, fontSize: "var(--wh-font-size-sm)" }}>
          The default capability of every site role (ADR-0037). Reference only — a role change never
          touches capability; use “Apply role’s defaults” to copy a row explicitly.
        </p>
        <DataTable
          rowKey={(row) => row.siteRole}
          rows={defaults ?? []}
          emptyState={<span>No defaults available.</span>}
          columns={[
            {
              key: "siteRole",
              header: "Site role",
              render: (row: SiteRoleTransferDefault) => siteRoleLabel(row.siteRole),
            },
            {
              key: "transferOriginEnabled",
              header: "Default origin",
              render: (row: SiteRoleTransferDefault) => (row.transferOriginEnabled ? "yes" : "no"),
            },
            {
              key: "transferDestinationEnabled",
              header: "Default destination",
              render: (row: SiteRoleTransferDefault) => (row.transferDestinationEnabled ? "yes" : "no"),
            },
          ]}
        />
      </Card>
    </div>
  );
}
