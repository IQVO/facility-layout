package http

import (
	"errors"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/structure"
	"github.com/claudioed/facility-layout/internal/domain/travel"
)

func toFixedStructureResponse(f *structure.FixedStructure) fixedStructureResponse {
	footprint := f.Footprint()
	return fixedStructureResponse{
		ID:       f.ID(),
		SiteCode: f.SiteCode(),
		Kind:     string(f.Kind()),
		Origin:   toPoint3DResponse(footprint.Origin()),
		Size:     toDimensionsResponse(footprint.Size()),
		Label:    f.Label(),
	}
}

// errMissingGeometryField reports a null or omitted coordinate/dimension in
// a geometry DTO. Both are schema violations (the fields are required
// numbers), but encoding/json would silently coerce null to the zero value,
// so the DTOs use pointers and this error turns the coercion into a 400.
var errMissingGeometryField = errors.New("geometry coordinates and dimensions are required and must be numbers, not null")

// errMissingSequenceHint reports an omitted or null sequenceHint on aisle
// registration — required by the contract, but encoding/json would coerce
// the absence to 0 and silently accept the request.
var errMissingSequenceHint = errors.New("aisle sequenceHint is required")

// fromPoint3DRequest maps a point3DRequest DTO to a domain Point3D.
func fromPoint3DRequest(req point3DRequest) (shared.Point3D, error) {
	if req.XM == nil || req.YM == nil || req.ZM == nil {
		return shared.Point3D{}, errMissingGeometryField
	}
	return shared.NewPoint3D(*req.XM, *req.YM, *req.ZM)
}

// fromDimensionsRequest maps a dimensionsRequest DTO to a domain Dimensions.
func fromDimensionsRequest(req dimensionsRequest) (shared.Dimensions, error) {
	if req.WidthM == nil || req.DepthM == nil || req.HeightM == nil {
		return shared.Dimensions{}, errMissingGeometryField
	}
	return shared.NewDimensions(*req.WidthM, *req.DepthM, *req.HeightM)
}

// fromSegmentRequest maps a segmentRequest DTO to a domain Segment.
func fromSegmentRequest(req segmentRequest) (shared.Segment, error) {
	start, err := fromPoint3DRequest(req.Start)
	if err != nil {
		return shared.Segment{}, err
	}
	end, err := fromPoint3DRequest(req.End)
	if err != nil {
		return shared.Segment{}, err
	}
	return shared.NewSegment(start, end)
}

// fromRectRequest maps a rectRequest DTO to a domain Rect.
func fromRectRequest(req rectRequest) (shared.Rect, error) {
	origin, err := fromPoint3DRequest(req.Origin)
	if err != nil {
		return shared.Rect{}, err
	}
	size, err := fromDimensionsRequest(req.Size)
	if err != nil {
		return shared.Rect{}, err
	}
	return shared.NewRect(origin, size)
}

func toCrossAisleResponse(c *aisle.CrossAisle) crossAisleResponse {
	return crossAisleResponse{
		ZoneID:    c.ZoneID(),
		FromAisle: c.FromAisle(),
		ToAisle:   c.ToAisle(),
		AtBay:     c.AtBay(),
		Active:    c.IsActive(),
	}
}

func toTravelNodeResponse(n travel.Node) travelNodeResponse {
	return travelNodeResponse{AisleID: n.AisleID, Bay: n.Bay}
}

func toTravelGraphResponse(view *usecases.TravelGraphView) travelGraphResponse {
	out := travelGraphResponse{
		Nodes: make([]travelNodeResponse, 0, len(view.Nodes)),
		Edges: make([]travelEdgeResponse, 0, len(view.Edges)),
	}
	for _, n := range view.Nodes {
		out.Nodes = append(out.Nodes, toTravelNodeResponse(n))
	}
	for _, e := range view.Edges {
		out.Edges = append(out.Edges, travelEdgeResponse{
			From:      toTravelNodeResponse(e.From),
			To:        toTravelNodeResponse(e.To),
			MetresM:   e.MetresM,
			Estimated: e.Estimated,
		})
	}
	return out
}

func toTravelDistanceResponse(d *usecases.TravelDistance) travelDistanceResponse {
	route := make([]travelNodeResponse, 0, len(d.Route))
	for _, n := range d.Route {
		route = append(route, toTravelNodeResponse(n))
	}
	return travelDistanceResponse{MetresM: d.MetresM, Estimated: d.Estimated, Route: route}
}
