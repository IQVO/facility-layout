import type { SiteRole } from "./types";

/**
 * Site network-role metadata (ADR-0037), mirroring the domain's own
 * roleTable display order exactly (internal/domain/site/role.go). The
 * wire values are the stable UPPER_SNAKE strings the REST API, MCP tools
 * and CloudEvents all share; the labels are display-only.
 *
 * UNSPECIFIED is deliberately absent: it is the "nobody classified this
 * site yet" default and can never be assigned by PUT /sites/{code}/role
 * (the server answers 422 invalid-site-role), so it must not be offered
 * as a selectable option.
 */
export const ASSIGNABLE_SITE_ROLES: { value: SiteRole; label: string }[] = [
  { value: "ROBOTICS_SORTABLE_FC", label: "Robotics sortable FC" },
  { value: "NON_SORTABLE_FC", label: "Non-sortable FC" },
  { value: "TRADITIONAL_SORTABLE_FC", label: "Traditional sortable FC" },
  { value: "SUB_SAME_DAY_FC", label: "Sub same-day FC" },
  { value: "FRESH_GROCERY_FC", label: "Fresh grocery FC" },
  { value: "XL_FC", label: "XL FC" },
  { value: "SOFT_LINES_SORTABLE_FC", label: "Soft lines sortable FC" },
  { value: "MICRO_FC", label: "Micro FC" },
  { value: "SIMULATED_FC", label: "Simulated FC" },
  { value: "INBOUND_CROSS_DOCK", label: "Inbound cross-dock" },
  { value: "NATIONAL_INBOUND_CROSS_DOCK", label: "National inbound cross-dock" },
  { value: "RETURNS_CENTER", label: "Returns center" },
  { value: "SORTATION_CENTER", label: "Sortation center" },
  { value: "DELIVERY_STATION", label: "Delivery station" },
  { value: "AIR_HUB_GATEWAY", label: "Air hub gateway" },
];

/** Human label for a wire role value; the raw enum for anything unknown so
 *  a server-side role newer than this table still renders legibly. */
export function siteRoleLabel(role: string): string {
  const found = ASSIGNABLE_SITE_ROLES.find((r) => r.value === role);
  if (found) return found.label;
  if (role === "UNSPECIFIED") return "Unspecified";
  return role;
}
