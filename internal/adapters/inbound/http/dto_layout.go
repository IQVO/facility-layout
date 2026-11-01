package http

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
