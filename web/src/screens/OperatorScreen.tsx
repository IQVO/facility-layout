import { useState } from "react";
import { Tabs } from "../components/formkit";
import { SiteNetworkPanel } from "./operator/SiteNetworkPanel";
import { LocationDetailPanel } from "./operator/LocationDetailPanel";
import { GeometryPanel } from "./operator/GeometryPanel";

const TABS = [
  { id: "network", label: "Site network" },
  { id: "location", label: "Location" },
  { id: "geometry", label: "Geometry" },
];

/** The note every operator screen carries: structural authoring (registering
 *  zones, aisles, cross-aisles, fixed structures, whole sites) stays on the
 *  documented bulk-import path (POST /locations/import, see the Rack
 *  planner); these screens are the OPERATOR surface -- classify a site,
 *  set its transfer capability, inspect and retire locations, and correct
 *  recorded geometry. */
const STRUCTURAL_NOTE =
  "Structural changes (new sites, zones, aisles, cross-aisles, structures) arrive via layout import — see the Rack planner.";

/**
 * Operator screens (Phase 4.2): the day-2 actions on an EXISTING layout.
 * Where Configure registers structure and the Rack planner bulk-imports a
 * whole building, this screen is for the operator who needs to reclassify
 * a site in the fulfilment network, set whether it may donate/receive
 * inter-site transfers, look one location up, decommission a broken slot,
 * or fix a badly-surveyed position -- without any structural authoring.
 */
export function OperatorScreen() {
  const [active, setActive] = useState("network");

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <div>
        <h1 style={{ fontSize: "var(--wh-font-size-2xl)", margin: 0 }}>Operate facility</h1>
        <p style={{ color: "var(--wh-color-text-muted)", marginTop: 4 }}>
          facility-layout · site network attributes, location detail &amp; geometry corrections
        </p>
        <p style={{ color: "var(--wh-color-text-faint)", marginTop: 2, fontSize: "var(--wh-font-size-xs)" }}>
          {STRUCTURAL_NOTE}
        </p>
      </div>

      <Tabs tabs={TABS} active={active} onChange={setActive} />

      {active === "network" && <SiteNetworkPanel />}
      {active === "location" && <LocationDetailPanel />}
      {active === "geometry" && <GeometryPanel />}
    </div>
  );
}
