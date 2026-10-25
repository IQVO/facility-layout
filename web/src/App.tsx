import { NavLink, Routes, Route } from "react-router-dom";
import { FacilityScreen } from "./screens/FacilityScreen";
import { ConfigureScreen } from "./screens/ConfigureScreen";
import { LayoutDesignerScreen } from "./screens/LayoutDesignerScreen";
import { RackPlannerScreen } from "./screens/RackPlannerScreen";
import { OperatorScreen } from "./screens/OperatorScreen";

const SUB_NAV = [
  { to: "", label: "Overview", end: true },
  { to: "configure", label: "Configure" },
  { to: "designer", label: "Layout designer" },
  { to: "planner", label: "Rack planner" },
  { to: "operator", label: "Operate" },
];

const linkStyle = ({ isActive }: { isActive: boolean }) => ({
  display: "inline-flex",
  padding: "6px 12px",
  borderRadius: "var(--wh-radius-pill)",
  fontSize: "var(--wh-font-size-sm)",
  fontWeight: isActive ? 600 : 500,
  color: isActive ? "var(--wh-color-text)" : "var(--wh-color-text-muted)",
  background: isActive ? "var(--wh-color-accent-muted)" : "transparent",
  textDecoration: "none",
});

/** Exposed as facility_mfe/App via Module Federation. Routed under
 *  /facility/* by the shell -- uses relative routes so this component
 *  works identically mounted under a prefix (in the shell) or at /
 *  (standalone dev, see main.tsx).
 *
 *  Four sub-screens, matching CLAUDE.md's own split between the
 *  structural/write side and the "draw the warehouse" read side:
 *   - Overview: the original read-only site/layout browser.
 *   - Configure: register sites, zones, aisles, location types and
 *     placement rules one at a time (the structural write side).
 *   - Layout designer: pick an existing zone and place individual
 *     LocationSlots directly onto its already-live drawn grid.
 *   - Rack planner: sketch a whole BLANK rack's shape (bay x level x
 *     position ranges) client-side first, then deploy the entire
 *     Site/Zone/Aisle/slots in one atomic bulk import -- the actual
 *     "draw a new warehouse, then deploy it" workflow.
 *   - Operate: day-2 operator actions on an existing layout -- site
 *     network role & transfer capability, location detail with
 *     decommission, and geometry corrections. */
export default function App() {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <nav style={{ display: "flex", gap: "var(--wh-space-2)" }}>
        {SUB_NAV.map((item) => (
          <NavLink key={item.to} to={item.to} end={item.end} style={linkStyle}>
            {item.label}
          </NavLink>
        ))}
      </nav>
      <Routes>
        <Route path="/" element={<FacilityScreen />} />
        <Route path="/configure" element={<ConfigureScreen />} />
        <Route path="/designer" element={<LayoutDesignerScreen />} />
        <Route path="/planner" element={<RackPlannerScreen />} />
        <Route path="/operator" element={<OperatorScreen />} />
      </Routes>
    </div>
  );
}
