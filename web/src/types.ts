/** Wire types mirroring facility-layout's dto.go response shapes exactly
 *  (siteResponse / zoneResponse / aisleResponse / locationSlotResponse /
 *  siteLayoutResponse / locationClassificationResponse) -- kept hand-in-
 *  sync with the Go DTOs rather than code-generated for v1, same
 *  convention as order-mgmt-mfe's types.ts. */

export interface Site {
  siteCode: string;
  name: string;
  status: string;
  /** The site's role in the fulfilment network (ADR-0037); wire value is
   *  the UPPER_SNAKE SiteRole enum, "UNSPECIFIED" until classified. */
  siteRole: string;
}

/** SiteRole (ADR-0037) -- what kind of network node the building is.
 *  UNSPECIFIED is the registration default and can never be assigned. */
export type SiteRole =
  | "UNSPECIFIED"
  | "ROBOTICS_SORTABLE_FC"
  | "NON_SORTABLE_FC"
  | "TRADITIONAL_SORTABLE_FC"
  | "SUB_SAME_DAY_FC"
  | "FRESH_GROCERY_FC"
  | "XL_FC"
  | "SOFT_LINES_SORTABLE_FC"
  | "MICRO_FC"
  | "SIMULATED_FC"
  | "INBOUND_CROSS_DOCK"
  | "NATIONAL_INBOUND_CROSS_DOCK"
  | "RETURNS_CENTER"
  | "SORTATION_CENTER"
  | "DELIVERY_STATION"
  | "AIR_HUB_GATEWAY";

/** One row of GET /site-roles/transfer-defaults (ADR-0037): the reference
 *  default capability for a role -- never applied automatically. */
export interface SiteRoleTransferDefault {
  siteRole: string;
  transferOriginEnabled: boolean;
  transferDestinationEnabled: boolean;
}

/** GET|PUT /sites/{siteCode}/transfer-capability (ADR-0035): whether the
 *  site may donate/receive inter-site transfers, plus the revision of the
 *  last published SiteCapabilityChanged (0 = never set). */
export interface SiteTransferCapability {
  siteCode: string;
  transferOriginEnabled: boolean;
  transferDestinationEnabled: boolean;
  capabilityRevision: number;
}


export interface Zone {
  zoneId: string;
  siteCode: string;
  areaCode: string;
  zoneCode: string;
  temperatureClass: string;
  hazmat: boolean;
  status: string;
}

export interface Aisle {
  aisleId: string;
  zoneId: string;
  aisleCode: string;
  sequenceHint: number;
  direction: string;
  status: string;
  /** The aisle's travel centreline, when set (ADR-0017). Absent until
   *  PUT /zones/{zoneId}/aisles/{aisleCode}/geometry is called. */
  centreline?: Segment;
}

/** A position in the site's local coordinate frame, in metres (ADR-0017).
 *  X/Y are arbitrary; Z is height above floor and must be >= 0. */
export interface Point3D {
  xM: number;
  yM: number;
  zM: number;
}

/** A rectangular footprint's extent, in metres (ADR-0017) -- all three
 *  strictly positive. */
export interface Dimensions {
  widthM: number;
  depthM: number;
  heightM: number;
}

/** A straight-line travel centreline between two distinct points
 *  (ADR-0017). */
export interface Segment {
  start: Point3D;
  end: Point3D;
}

export interface Capacity {
  maxWeightKg: number;
  maxVolumeM3: number;
}

export interface LocationCoordinates {
  site: string;
  area: string;
  zone: string;
  aisle: string;
  bay: string;
  level: string;
  position: string;
}

/** LocationRole (ADR-0016): what a location on the map is FOR. Storage is
 *  the default and only role that existed before this ADR. */
export type LocationRole =
  | "Storage"
  | "Dock"
  | "Yard"
  | "WorkCenter"
  | "Drop"
  | "Staging"
  | "QC"
  | "Consolidation"
  | "Shipping";

export interface LocationSlot {
  locationCode: string;
  zoneId: string;
  aisleId: string;
  coordinates: LocationCoordinates;
  locationType: string;
  role: LocationRole;
  /** Set only when role is Dock (Inbound | Outbound | Both). */
  dockFlow?: string;
  /** Set only when role is WorkCenter (Pack, Sort, QC, VAS, ...). */
  activities?: string[];
  capacity: Capacity;
  status: string;
  /** The slot's physical position, when set (ADR-0017). Absent until
   *  PUT /locations/{locationCode}/geometry is called. */
  position?: Point3D;
  /** The slot's physical footprint, when set (ADR-0017). */
  dimensions?: Dimensions;
  /** Explicit pick-path override, when set (ADR-0017). */
  pickSequence?: number;
}

/** locationClassificationResponse -- the denormalized Hazmat/TemperatureClass
 *  read a downstream consumer (inventory-storage) resolves from a
 *  LocationCode's parent Zone. Not rendered by this screen yet, but kept
 *  here since it's part of this service's published DTO shape. */
export interface LocationClassification {
  hazmat: boolean;
  temperatureClass: string;
}

// ------------------------------------------------------ configuration -----

export interface Capacity {
  maxWeightKg: number;
  maxVolumeM3: number;
}

/** locationTypeResponse. */
export interface LocationType {
  name: string;
  role: LocationRole;
  defaultCapacity: Capacity;
}

/** zonePredicateResponse -- a PlacementRule's target: a specific zone code
 *  and/or a temperature class and/or a hazmat flag. All fields optional;
 *  any combination present must ALL match for the rule to apply. */
export interface ZonePredicate {
  zoneCode?: string;
  temperatureClass?: string;
  hazmat?: boolean;
}

/** placementRuleResponse. */
export interface PlacementRule {
  ruleId: string;
  locationType: string;
  effect: "Allow" | "Deny";
  zone: ZonePredicate;
  description: string;
}

// --------------------------------------------- "draw the warehouse" -----

/** aisleLayoutResponse: aisleResponse embedded + Slots. */
export interface AisleLayout extends Aisle {
  slots: LocationSlot[];
}

/** zoneLayoutResponse: zoneResponse embedded + Aisles. */
export interface ZoneLayout extends Zone {
  aisles: AisleLayout[];
}

export interface SiteLayoutTotals {
  zones: number;
  aisles: number;
  slots: number;
}

/** siteLayoutResponse -- the full nested, drawable structure of one site:
 *  zones -> aisles -> slots, as returned by GET /sites/{siteCode}/layout. */
export interface SiteLayout {
  site: Site;
  zones: ZoneLayout[];
  totals: SiteLayoutTotals;
}

// --------------------------------------------------------- zone grid -----

/** zoneGridResponse: one zone's slots as an explicit matrix -- rows are
 *  Levels, columns are (Aisle, Bay) pairs in aisle walk order, and a cell
 *  is null where the rack has a gap. Returned by GET /zones/{zoneId}/grid. */
export interface GridColumn {
  aisleId: string;
  aisleCode: string;
  bay: string;
  sequenceHint: number;
}

export interface GridPosition {
  locationCode: string;
  position: string;
  locationType: string;
  status: string;
}

export interface GridCell {
  positions: GridPosition[];
}

export interface GridRow {
  level: string;
  /** Index-aligned with ZoneGrid.columns. Null entry = a gap in the rack. */
  cells: (GridCell | null)[];
}

export interface ZoneGrid {
  zone: Zone;
  columns: GridColumn[];
  levels: string[];
  rows: GridRow[];
}

// ------------------------------------------------------- travel graph -----

/** travelNodeDTO -- one aisle/bay waypoint on the travel graph (ADR-0017). */
export interface TravelNode {
  aisleId: string;
  bay: string;
}

/** travelDistanceDTO -- the outcome of GET /distance: the shortest route's
 *  total length in metres, whether any leg was graph-estimated rather than
 *  measured from real aisle centreline geometry, and the ordered waypoints
 *  traversed (ADR-0017). */
export interface TravelDistance {
  metresM: number;
  estimated: boolean;
  route: TravelNode[];
}

// ------------------------------------------------------- bulk import -----

/** importRowRequest -- one fully-specified row of a facility layout
 *  import: a whole Site/Area/Zone/Aisle/Bay/Level/Position address plus
 *  the LocationType for the slot at it. Structural parents named by a row
 *  are created on first sight (POST /locations/import), which is what
 *  lets the Rack Planner deploy a brand-new Site/Zone/Aisle and all its
 *  slots in one call instead of requiring each to be pre-registered. */
export interface ImportRow {
  siteCode: string;
  siteName?: string;
  areaCode: string;
  zoneCode: string;
  temperatureClass: string;
  hazmat: boolean;
  aisleCode: string;
  sequenceHint: number;
  direction?: string;
  bay: string;
  level: string;
  position: string;
  locationType: string;
  capacityOverride?: Capacity;
}

export interface ImportRowResult {
  index: number;
  locationCode: string;
  succeeded: boolean;
  error?: string;
}

/** importReportResponse -- the full outcome of POST /locations/import:
 *  every row is processed, partial success is reported per row rather
 *  than aborting the whole import on the first bad row. */
export interface ImportReport {
  rowsSubmitted: number;
  slotsImported: number;
  rowsRejected: number;
  results: ImportRowResult[];
}
