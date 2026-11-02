// Package http is the inbound chi adapter: DTOs, handlers, routing, and
// domain-error-to-HTTP-status mapping. Domain structs never cross this
// boundary — every response here is a DTO defined in this package (dto*.go).
package http

// ------------------------------------------------------------- requests ----

type registerSiteRequest struct {
	SiteCode string `json:"siteCode"`
	Name     string `json:"name"`
	// SiteRole is optional (ADR-0037): omitted means UNSPECIFIED.
	SiteRole string `json:"siteRole,omitempty"`
}

// setSiteRoleRequest is the body of PUT /sites/{siteCode}/role (ADR-0037).
// A missing role decodes to "" and is rejected as invalid-site-role.
type setSiteRoleRequest struct {
	SiteRole string `json:"siteRole"`
}

// setTransferCapabilityRequest is the full snapshot PUT
// /sites/{siteCode}/transfer-capability replaces (ADR-0035). Both fields
// are required booleans; pointers turn an omitted or null field into a 400
// instead of encoding/json's silent false.
type setTransferCapabilityRequest struct {
	TransferOriginEnabled      *bool `json:"transferOriginEnabled"`
	TransferDestinationEnabled *bool `json:"transferDestinationEnabled"`
}

type registerZoneRequest struct {
	AreaCode         string `json:"areaCode"`
	ZoneCode         string `json:"zoneCode"`
	TemperatureClass string `json:"temperatureClass"`
	Hazmat           bool   `json:"hazmat"`
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
	HandlingUnit     string           `json:"handlingUnit,omitempty"`
}

// declareHandlingUnitRequest is the body of PUT /locations/{code}/handling-unit
// (ADR-0040): the handling unit is required there, validated by the domain.
type declareHandlingUnitRequest struct {
	HandlingUnit string `json:"handlingUnit"`
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
