import { useState, type FormEvent } from "react";
import { Card, DataTable } from "@warehouse/ui-kit";
import { useFetchAll } from "../../useFetchAll";
import { apiPost, ApiError } from "../../api";
import { FACILITY_API_BASE } from "../../config";
import type { LocationType, PlacementRule } from "../../types";
import {
  CheckboxField,
  FormRow,
  InlineError,
  InlineSuccess,
  SelectField,
  SubmitButton,
  TextField,
} from "../../components/formkit";

const EFFECTS = ["Allow", "Deny"];
const TEMPERATURE_CLASSES = ["", "Ambient", "Chilled", "Frozen"];

/** DefinePlacementRule: POST /placement-rules -- declares which
 *  LocationTypes are legal in which Zones (by zone code and/or temperature
 *  class and/or hazmat flag). This is the mechanism CLAUDE.md calls out as
 *  preventing "ambient product in the frozen zone" -- enforced once here,
 *  checked automatically by RegisterLocationSlot / the layout designer's
 *  slot-registration form, never re-checked by every caller. */
export function PlacementRulesPanel() {
  const [refreshKey, setRefreshKey] = useState(0);
  const [ruleId, setRuleId] = useState("");
  const [locationType, setLocationType] = useState("");
  const [effect, setEffect] = useState("Allow");
  const [zoneCode, setZoneCode] = useState("");
  const [temperatureClass, setTemperatureClass] = useState("");
  const [hazmatEnabled, setHazmatEnabled] = useState(false);
  const [hazmat, setHazmat] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const { data: types } = useFetchAll<LocationType>(`${FACILITY_API_BASE}/location-types`);
  const { data: rules, loading, error: listError } = useFetchAll<PlacementRule>(`${FACILITY_API_BASE}/placement-rules`, refreshKey);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSuccess(null);
    setSubmitting(true);
    try {
      await apiPost<PlacementRule>("/placement-rules", {
        ruleId: ruleId.trim(),
        locationType,
        effect,
        zone: {
          ...(zoneCode.trim() ? { zoneCode: zoneCode.trim() } : {}),
          ...(temperatureClass ? { temperatureClass } : {}),
          ...(hazmatEnabled ? { hazmat } : {}),
        },
      });
      setSuccess(`Placement rule ${ruleId.trim()} defined.`);
      setRuleId("");
      setZoneCode("");
      setTemperatureClass("");
      setHazmatEnabled(false);
      setHazmat(false);
      setRefreshKey((k) => k + 1);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to define placement rule.");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <Card title="Define a placement rule">
        <form onSubmit={onSubmit} style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-3)" }}>
          <FormRow>
            <TextField label="Rule id" value={ruleId} onChange={setRuleId} placeholder="RULE-HAZ-ONLY-RACK" required />
            <SelectField
              label="Location type"
              value={locationType}
              onChange={setLocationType}
              options={(types ?? []).map((t) => ({ value: t.name, label: t.name }))}
              required
            />
            <SelectField
              label="Effect"
              value={effect}
              onChange={setEffect}
              options={EFFECTS.map((e) => ({ value: e, label: e }))}
              required
            />
          </FormRow>
          <p style={{ margin: 0, fontSize: "var(--wh-font-size-xs)", color: "var(--wh-color-text-faint)" }}>
            Zone predicate — leave a field blank to not constrain on it. At least one dimension must be set.
          </p>
          <FormRow>
            <TextField label="Zone code" value={zoneCode} onChange={setZoneCode} placeholder="HAZ (optional)" />
            <SelectField
              label="Temperature class"
              value={temperatureClass}
              onChange={setTemperatureClass}
              options={TEMPERATURE_CLASSES.filter((t) => t).map((t) => ({ value: t, label: t }))}
              placeholder="(optional)"
            />
          </FormRow>
          <CheckboxField label="Constrain on hazmat flag" checked={hazmatEnabled} onChange={setHazmatEnabled} />
          {hazmatEnabled && <CheckboxField label="Zone must be hazmat" checked={hazmat} onChange={setHazmat} />}
          <FormRow>
            <SubmitButton
              disabled={
                submitting ||
                !ruleId.trim() ||
                !locationType ||
                (!zoneCode.trim() && !temperatureClass && !hazmatEnabled)
              }
            >
              {submitting ? "Defining…" : "Define rule"}
            </SubmitButton>
          </FormRow>
          <InlineError message={error} />
          <InlineSuccess message={success} />
        </form>
      </Card>

      <Card title="Placement rules">
        {listError && <InlineError message={listError.message} />}
        <DataTable
          rowKey={(r) => r.ruleId}
          rows={rules ?? []}
          loading={loading}
          emptyState={<span>No placement rules defined yet — every location type is legal everywhere.</span>}
          columns={[
            { key: "ruleId", header: "Rule id", render: (r) => r.ruleId },
            { key: "locationType", header: "Location type", render: (r) => r.locationType },
            { key: "effect", header: "Effect", render: (r) => r.effect },
            {
              key: "zone",
              header: "Zone predicate",
              render: (r) =>
                [
                  r.zone.zoneCode && `zoneCode=${r.zone.zoneCode}`,
                  r.zone.temperatureClass && `temperatureClass=${r.zone.temperatureClass}`,
                  r.zone.hazmat !== undefined && `hazmat=${r.zone.hazmat}`,
                ]
                  .filter(Boolean)
                  .join(", ") || "—",
            },
          ]}
        />
      </Card>
    </div>
  );
}
