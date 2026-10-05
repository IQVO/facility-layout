// Package http is the inbound chi adapter: DTOs, handlers, routing, and
// domain-error-to-HTTP-status mapping. Domain structs never cross this
// boundary — every response here is a DTO defined in this file.
package http

// ------------------------------------------------------------- requests ----

type registerSiteRequest struct {
	SiteCode string `json:"siteCode"`
	Name     string `json:"name"`
}

type registerZoneRequest struct {
	AreaCode         string   `json:"areaCode"`
	ZoneCode         string   `json:"zoneCode"`
	TemperatureClass string   `json:"temperatureClass"`
	Hazmat           bool     `json:"hazmat"`
	// BayPitchM/LevelPitchM are optional ADR-0017 pitch overrides for the
	// travel graph's estimated-distance fallback. nil keeps the defaults;
	// supplying exactly one of the two is a 422 (invalid-pitch).
	BayPitchM   *float64 `json:"bayPitchM,omitempty"`
	LevelPitchM *float64 `json:"levelPitchM,omitempty"`
}

type registerAisleRequest struct {
	AisleCode string `json:"aisleCode"`
	// SequenceHint is a pointer so an omitted or null field is
	// distinguishable from a genuine 0 — encoding/json would otherwise
	// coerce both to 0 and the required-field violation would be silently
	// accepted.
	SequenceHint *int   `json:"sequenceHint"`
	Direction    string `json:"direction"`
}

type capacityRequest struct {
	MaxWeightKg float64 `json:"maxWeightKg"`
	MaxVolumeM3 float64 `json:"maxVolumeM3"`
}

type registerLocationTypeRequest struct {
	Name            string          `json:"name"`
	Role            string          `json:"role,omitempty"`
	DefaultCapacity capacityRequest `json:"defaultCapacity,omitempty"`
}

type zonePredicateRequest struct {
	ZoneCode         string `json:"zoneCode,omitempty"`
	TemperatureClass string `json:"temperatureClass,omitempty"`
	Hazmat           *bool  `json:"hazmat,omitempty"`
}

type definePlacementRuleRequest struct {
	RuleID       string               `json:"ruleId"`
	LocationType string               `json:"locationType"`
	Effect       string               `json:"effect"`
	Zone         zonePredicateRequest `json:"zone"`
}

type registerLocationSlotRequest struct {
	LocationCode     string           `json:"locationCode"`
	LocationType     string           `json:"locationType"`
	DockFlow         string           `json:"dockFlow,omitempty"`
	Activities       []string         `json:"activities,omitempty"`
	CapacityOverride *capacityRequest `json:"capacityOverride,omitempty"`
}

type importRowRequest struct {
	SiteCode         string           `json:"siteCode"`
	SiteName         string           `json:"siteName,omitempty"`
	AreaCode         string           `json:"areaCode"`
	ZoneCode         string           `json:"zoneCode"`
	TemperatureClass string           `json:"temperatureClass"`
	Hazmat           bool             `json:"hazmat"`
	AisleCode        string           `json:"aisleCode"`
	SequenceHint     int              `json:"sequenceHint"`
	Direction        string           `json:"direction,omitempty"`
	Bay              string           `json:"bay"`
	Level            string           `json:"level"`
	Position         string           `json:"position"`
	LocationType     string           `json:"locationType"`
	DockFlow         string           `json:"dockFlow,omitempty"`
	Activities       []string         `json:"activities,omitempty"`
	CapacityOverride *capacityRequest `json:"capacityOverride,omitempty"`
	Geometry         *geometryRequest `json:"geometry,omitempty"`
	PickSequence     *int             `json:"pickSequence,omitempty"`
}

// point3DRequest is a position in the site's local coordinate frame, in
// metres (ADR-0017). Fields are pointers so a JSON null or an omitted
// coordinate is distinguishable from a genuine 0 — encoding/json would
// otherwise coerce null to 0 and the contract violation would be silently
// accepted.
type point3DRequest struct {
	XM *float64 `json:"xM"`
	YM *float64 `json:"yM"`
	ZM *float64 `json:"zM"`
}

// dimensionsRequest is a rectangular footprint's extent, in metres
// (ADR-0017). Pointers for the same reason as point3DRequest.
type dimensionsRequest struct {
	WidthM  *float64 `json:"widthM"`
	DepthM  *float64 `json:"depthM"`
	HeightM *float64 `json:"heightM"`
}

// geometryRequest is a slot's position + footprint, submitted together
// (ADR-0017's all-or-nothing rule).
type geometryRequest struct {
	Position   point3DRequest    `json:"position"`
	Dimensions dimensionsRequest `json:"dimensions"`
}

type setLocationGeometryRequest struct {
	Position     point3DRequest    `json:"position"`
	Dimensions   dimensionsRequest `json:"dimensions"`
	PickSequence *int              `json:"pickSequence,omitempty"`
}

type setAisleGeometryRequest struct {
	Centreline segmentRequest `json:"centreline"`
}

// segmentRequest is an aisle's straight-line travel centreline (ADR-0017).
type segmentRequest struct {
	Start point3DRequest `json:"start"`
	End   point3DRequest `json:"end"`
}

type registerFixedStructureRequest struct {
	ID        string      `json:"id,omitempty"`
	Kind      string      `json:"kind"`
	Footprint rectRequest `json:"footprint"`
	Label     string      `json:"label"`
}

// rectRequest is a FixedStructure's footprint: an origin position plus its
// extent (ADR-0017).
type rectRequest struct {
	Origin point3DRequest    `json:"origin"`
	Size   dimensionsRequest `json:"size"`
}

// registerCrossAisleRequest declares a zone-scoped connection between two
// of its aisles at a bay ordinal (ADR-0017).
type registerCrossAisleRequest struct {
	FromAisle string `json:"fromAisle"`
	ToAisle   string `json:"toAisle"`
	AtBay     string `json:"atBay"`
}

// ------------------------------------------------------------ responses ----

type siteResponse struct {
	SiteCode string `json:"siteCode"`
	Name     string `json:"name"`
	Status   string `json:"status"`
}

type zoneResponse struct {
	ZoneID           string `json:"zoneId"`
	SiteCode         string `json:"siteCode"`
	AreaCode         string `json:"areaCode"`
	ZoneCode         string `json:"zoneCode"`
	TemperatureClass string `json:"temperatureClass"`
	Hazmat           bool   `json:"hazmat"`
	Status           string `json:"status"`
	// BayPitchM/LevelPitchM (ADR-0017) are the zone's effective pitch
	// values — the explicit override when one is set, else the service
	// default — so a consumer never has to know the defaults to reason
	// about estimated distances.
	BayPitchM   float64 `json:"bayPitchM"`
	LevelPitchM float64 `json:"levelPitchM"`
}

type aisleResponse struct {
	AisleID      string           `json:"aisleId"`
	ZoneID       string           `json:"zoneId"`
	AisleCode    string           `json:"aisleCode"`
	SequenceHint int              `json:"sequenceHint"`
	Direction    string           `json:"direction"`
	Status       string           `json:"status"`
	Centreline   *segmentResponse `json:"centreline,omitempty"`
}

type capacityResponse struct {
	MaxWeightKg float64 `json:"maxWeightKg"`
	MaxVolumeM3 float64 `json:"maxVolumeM3"`
}

type locationTypeResponse struct {
	Name            string           `json:"name"`
	Role            string           `json:"role"`
	DefaultCapacity capacityResponse `json:"defaultCapacity"`
}

type placementRuleResponse struct {
	RuleID       string                `json:"ruleId"`
	LocationType string                `json:"locationType"`
	Effect       string                `json:"effect"`
	Zone         zonePredicateResponse `json:"zone"`
	Description  string                `json:"description"`
}

type zonePredicateResponse struct {
	ZoneCode         string `json:"zoneCode,omitempty"`
	TemperatureClass string `json:"temperatureClass,omitempty"`
	Hazmat           *bool  `json:"hazmat,omitempty"`
}

// coordinatesResponse is the exploded LocationCode: the seven typed
// segments a renderer needs to position a slot without re-parsing the code.
type coordinatesResponse struct {
	Site     string `json:"site"`
	Area     string `json:"area"`
	Zone     string `json:"zone"`
	Aisle    string `json:"aisle"`
	Bay      string `json:"bay"`
	Level    string `json:"level"`
	Position string `json:"position"`
}

type locationSlotResponse struct {
	LocationCode string              `json:"locationCode"`
	ZoneID       string              `json:"zoneId"`
	AisleID      string              `json:"aisleId"`
	Coordinates  coordinatesResponse `json:"coordinates"`
	LocationType string              `json:"locationType"`
	Role         string              `json:"role"`
	DockFlow     string              `json:"dockFlow,omitempty"`
	Activities   []string            `json:"activities,omitempty"`
	Capacity     capacityResponse    `json:"capacity"`
	Status       string              `json:"status"`
	Position     *point3DResponse    `json:"position,omitempty"`
	Dimensions   *dimensionsResponse `json:"dimensions,omitempty"`
	PickSequence *int                `json:"pickSequence,omitempty"`
}

// point3DResponse mirrors point3DRequest for a slot/structure position.
type point3DResponse struct {
	XM float64 `json:"xM"`
	YM float64 `json:"yM"`
	ZM float64 `json:"zM"`
}

// dimensionsResponse mirrors dimensionsRequest for a slot/structure footprint.
type dimensionsResponse struct {
	WidthM  float64 `json:"widthM"`
	DepthM  float64 `json:"depthM"`
	HeightM float64 `json:"heightM"`
}

// segmentResponse is an aisle's travel centreline (ADR-0017).
type segmentResponse struct {
	Start point3DResponse `json:"start"`
	End   point3DResponse `json:"end"`
}

// fixedStructureResponse is one site-scoped physical obstacle (ADR-0017).
type fixedStructureResponse struct {
	ID       string             `json:"id"`
	SiteCode string             `json:"siteCode"`
	Kind     string             `json:"kind"`
	Origin   point3DResponse    `json:"origin"`
	Size     dimensionsResponse `json:"size"`
	Label    string             `json:"label"`
}

// locationClassificationResponse is the resolved subset of a slot's parent
// Zone that a cross-context caller (inventory-storage validating a
// classified SKU's stow target) needs to validate placement, without
// giving that caller the rest of the Zone or LocationSlot shape. This
// context remains the source of truth for these attributes — Zone already
// carries them; this DTO is a denormalized read, not a new aggregate.
type locationClassificationResponse struct {
	Hazmat           bool   `json:"hazmat"`
	TemperatureClass string `json:"temperatureClass"`
}

type importRowResultResponse struct {
	Index        int    `json:"index"`
	LocationCode string `json:"locationCode"`
	Succeeded    bool   `json:"succeeded"`
	Error        string `json:"error,omitempty"`
}

type importReportResponse struct {
	RowsSubmitted int                       `json:"rowsSubmitted"`
	SlotsImported int                       `json:"slotsImported"`
	RowsRejected  int                       `json:"rowsRejected"`
	Results       []importRowResultResponse `json:"results"`
}

// ------------------------------------------------- "draw the warehouse" ----

// siteLayoutResponse is the full nested, drawable structure of one site:
// zones -> aisles -> slots, pre-grouped and pre-ordered so a frontend can
// paint a floor plan without any client-side joining or sorting.
type siteLayoutResponse struct {
	Site            siteResponse             `json:"site"`
	Zones           []zoneLayoutResponse     `json:"zones"`
	FixedStructures []fixedStructureResponse `json:"fixedStructures"`
	Totals          siteLayoutTotalsResponse `json:"totals"`
}

type siteLayoutTotalsResponse struct {
	Zones  int `json:"zones"`
	Aisles int `json:"aisles"`
	Slots  int `json:"slots"`
}

type zoneLayoutResponse struct {
	zoneResponse
	Aisles []aisleLayoutResponse `json:"aisles"`
}

type aisleLayoutResponse struct {
	aisleResponse
	Slots []locationSlotResponse `json:"slots"`
}

// zoneGridResponse is one zone's slots as an explicit matrix: rows are
// Levels, columns are (Aisle, Bay) pairs in aisle walk order, and a cell is
// null where the rack has a gap. A UI iterates rows x columns and paints.
type zoneGridResponse struct {
	Zone    zoneResponse         `json:"zone"`
	Columns []gridColumnResponse `json:"columns"`
	Levels  []string             `json:"levels"`
	Rows    []gridRowResponse    `json:"rows"`
}

type gridColumnResponse struct {
	AisleID      string `json:"aisleId"`
	AisleCode    string `json:"aisleCode"`
	Bay          string `json:"bay"`
	SequenceHint int    `json:"sequenceHint"`
}

type gridRowResponse struct {
	Level string `json:"level"`
	// Cells is index-aligned with the grid's Columns. A nil entry is a gap
	// in the rack and serializes as JSON null, per CLAUDE.md.
	Cells []*gridCellResponse `json:"cells"`
}

type gridCellResponse struct {
	Positions []gridPositionResponse `json:"positions"`
}

type gridPositionResponse struct {
	LocationCode string `json:"locationCode"`
	Position     string `json:"position"`
	LocationType string `json:"locationType"`
	// Role is the slot's LocationRole (ADR-0016): the site layout response
	// already carried it per slot, but the zone grid omitted it — an
	// operator painting a zone could not tell a Dock or WorkCenter cell
	// from a Storage one without a second request.
	Role   string `json:"role"`
	Status string `json:"status"`
}

// -------------------------------------------------------- travel graph ----

// crossAisleResponse is one zone-scoped connection between two aisles at a
// bay ordinal (ADR-0017).
type crossAisleResponse struct {
	ZoneID    string `json:"zoneId"`
	FromAisle string `json:"fromAisle"`
	ToAisle   string `json:"toAisle"`
	AtBay     string `json:"atBay"`
	Active    bool   `json:"active"`
}

// travelNodeResponse is one aisle/bay waypoint on the travel graph.
type travelNodeResponse struct {
	AisleID string `json:"aisleId"`
	Bay     string `json:"bay"`
}

// travelEdgeResponse is one directed, weighted connection between two
// waypoints on the travel graph.
type travelEdgeResponse struct {
	From      travelNodeResponse `json:"from"`
	To        travelNodeResponse `json:"to"`
	MetresM   float64            `json:"metresM"`
	Estimated bool               `json:"estimated"`
}

// travelGraphResponse is a zone's full travel graph: every waypoint and
// every directed edge between them (ADR-0017).
type travelGraphResponse struct {
	Nodes []travelNodeResponse `json:"nodes"`
	Edges []travelEdgeResponse `json:"edges"`
}

// travelDistanceResponse is the outcome of a GET /distance query: the
// shortest path's total length, whether any leg was estimated rather than
// measured, and the ordered waypoints traversed (ADR-0017).
type travelDistanceResponse struct {
	MetresM   float64              `json:"metresM"`
	Estimated bool                 `json:"estimated"`
	Route     []travelNodeResponse `json:"route"`
}

// ----------------------------------------------------------- RFC 7807 -----

// problemDetails is the RFC 7807 (Problem Details for HTTP APIs) response
// body used for every error response in this service, from day one.
type problemDetails struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance,omitempty"`
}
