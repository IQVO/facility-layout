package http

// ------------------------------------------------------------ responses ----

type siteResponse struct {
	SiteCode string `json:"siteCode"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	// SiteRole is the site's role in the fulfilment network (ADR-0037).
	SiteRole string `json:"siteRole"`
	// GLN is the site's GS1 Global Location Number, omitted when none was
	// recorded (ADR-0045).
	GLN string `json:"gln,omitempty"`
}

// siteRoleTransferDefaultResponse is one row of
// GET /site-roles/transfer-defaults (ADR-0037).
type siteRoleTransferDefaultResponse struct {
	SiteRole                   string `json:"siteRole"`
	TransferOriginEnabled      bool   `json:"transferOriginEnabled"`
	TransferDestinationEnabled bool   `json:"transferDestinationEnabled"`
}

// transferCapabilityResponse is a site's current transfer capability and
// the revision of its last published SiteCapabilityChanged (0 = never).
type transferCapabilityResponse struct {
	SiteCode                   string `json:"siteCode"`
	TransferOriginEnabled      bool   `json:"transferOriginEnabled"`
	TransferDestinationEnabled bool   `json:"transferDestinationEnabled"`
	CapabilityRevision         int64  `json:"capabilityRevision"`
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
	// GLN is the zone's GS1 Global Location Number, omitted when none was
	// recorded (ADR-0045).
	GLN string `json:"gln,omitempty"`
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
	HandlingUnit string              `json:"handlingUnit,omitempty"`
	// GLN and SpotKind are omitted when none was recorded (ADR-0045);
	// SpotKind is only ever present on a Yard slot.
	GLN      string `json:"gln,omitempty"`
	SpotKind string `json:"spotKind,omitempty"`
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
