package http

import (
	"errors"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
	"github.com/claudioed/facility-layout/internal/domain/slot"
	"github.com/claudioed/facility-layout/internal/domain/structure"
	"github.com/claudioed/facility-layout/internal/domain/travel"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

func toSiteResponse(s *site.Site) siteResponse {
	return siteResponse{SiteCode: s.Code(), Name: s.Name(), Status: string(s.Status())}
}

func toZoneResponse(z *zone.Zone) zoneResponse {
	return zoneResponse{
		ZoneID:           z.ID(),
		SiteCode:         z.SiteCode(),
		AreaCode:         z.AreaCode(),
		ZoneCode:         z.ZoneCode(),
		TemperatureClass: string(z.TemperatureClass()),
		Hazmat:           z.Hazmat(),
		Status:           string(z.Status()),
	}
}

func toAisleResponse(a *aisle.Aisle) aisleResponse {
	out := aisleResponse{
		AisleID:      a.ID(),
		ZoneID:       a.ZoneID(),
		AisleCode:    a.AisleCode(),
		SequenceHint: a.SequenceHint(),
		Direction:    string(a.Direction()),
		Status:       string(a.Status()),
	}
	if centreline := a.Centreline(); !centreline.IsZero() {
		out.Centreline = &segmentResponse{
			Start: toPoint3DResponse(centreline.Start()),
			End:   toPoint3DResponse(centreline.End()),
		}
	}
	return out
}

// toPoint3DResponse maps a domain Point3D to its response DTO.
func toPoint3DResponse(p shared.Point3D) point3DResponse {
	return point3DResponse{XM: p.XM(), YM: p.YM(), ZM: p.ZM()}
}

// toDimensionsResponse maps a domain Dimensions to its response DTO.
func toDimensionsResponse(d shared.Dimensions) dimensionsResponse {
	return dimensionsResponse{WidthM: d.WidthM(), DepthM: d.DepthM(), HeightM: d.HeightM()}
}

func toCapacityResponse(c shared.Capacity) capacityResponse {
	return capacityResponse{MaxWeightKg: c.MaxWeightKg(), MaxVolumeM3: c.MaxVolumeM3()}
}

func toLocationTypeResponse(t placement.LocationType) locationTypeResponse {
	return locationTypeResponse{Name: t.Name(), Role: string(t.Role()), DefaultCapacity: toCapacityResponse(t.DefaultCapacity())}
}

func toPlacementRuleResponse(rule placement.PlacementRule) placementRuleResponse {
	predicate := rule.Predicate()
	return placementRuleResponse{
		RuleID:       rule.ID(),
		LocationType: rule.LocationType(),
		Effect:       string(rule.Effect()),
		Zone: zonePredicateResponse{
			ZoneCode:         predicate.ZoneCode(),
			TemperatureClass: string(predicate.TemperatureClass()),
			Hazmat:           predicate.Hazmat(),
		},
		Description: rule.Describe(),
	}
}

func toCoordinatesResponse(code shared.LocationCode) coordinatesResponse {
	return coordinatesResponse{
		Site:     code.Site(),
		Area:     code.Area(),
		Zone:     code.Zone(),
		Aisle:    code.Aisle(),
		Bay:      code.Bay(),
		Level:    code.Level(),
		Position: code.Position(),
	}
}

func toLocationSlotResponse(s *slot.LocationSlot) locationSlotResponse {
	code := s.Code()
	f := s.Functional()
	out := locationSlotResponse{
		LocationCode: code.String(),
		ZoneID:       code.ZoneID(),
		AisleID:      code.AisleID(),
		Coordinates:  toCoordinatesResponse(code),
		LocationType: s.LocationType(),
		Role:         string(s.Role()),
		DockFlow:     string(f.DockFlow()),
		Activities:   activityStringsHTTP(f.Activities()),
		Capacity:     toCapacityResponse(s.Capacity()),
		Status:       string(s.Status()),
		PickSequence: s.PickSequence(),
	}
	if position := s.Position(); !position.IsZero() {
		p := toPoint3DResponse(position)
		out.Position = &p
	}
	if dimensions := s.Dimensions(); !dimensions.IsZero() {
		d := toDimensionsResponse(dimensions)
		out.Dimensions = &d
	}
	return out
}

// activityStringsHTTP converts a slot's Activity set to plain strings for
// the response DTO, or nil when there are none, so "activities" is omitted
// entirely for every non-WorkCenter slot.
func activityStringsHTTP(activities []slot.Activity) []string {
	if len(activities) == 0 {
		return nil
	}
	out := make([]string, 0, len(activities))
	for _, a := range activities {
		out = append(out, string(a))
	}
	return out
}

func toLocationClassificationResponse(z *zone.Zone) locationClassificationResponse {
	return locationClassificationResponse{
		Hazmat:           z.Hazmat(),
		TemperatureClass: string(z.TemperatureClass()),
	}
}

func toImportRow(row importRowRequest) usecases.ImportRow {
	out := usecases.ImportRow{
		SiteCode:         row.SiteCode,
		SiteName:         row.SiteName,
		AreaCode:         row.AreaCode,
		ZoneCode:         row.ZoneCode,
		TemperatureClass: shared.TemperatureClass(row.TemperatureClass),
		Hazmat:           row.Hazmat,
		AisleCode:        row.AisleCode,
		SequenceHint:     row.SequenceHint,
		Direction:        shared.Direction(row.Direction),
		Bay:              row.Bay,
		Level:            row.Level,
		Position:         row.Position,
		LocationType:     row.LocationType,
		DockFlow:         row.DockFlow,
		Activities:       row.Activities,
		PickSequence:     row.PickSequence,
	}
	if row.CapacityOverride != nil {
		out.MaxWeightKg = row.CapacityOverride.MaxWeightKg
		out.MaxVolumeM3 = row.CapacityOverride.MaxVolumeM3
	}
	if row.Geometry != nil {
		out.XM = row.Geometry.Position.XM
		out.YM = row.Geometry.Position.YM
		out.ZM = row.Geometry.Position.ZM
		out.WidthM = row.Geometry.Dimensions.WidthM
		out.DepthM = row.Geometry.Dimensions.DepthM
		out.HeightM = row.Geometry.Dimensions.HeightM
	}
	return out
}

func toImportReportResponse(report *usecases.ImportReport) importReportResponse {
	results := make([]importRowResultResponse, 0, len(report.Results))
	for _, result := range report.Results {
		results = append(results, importRowResultResponse{
			Index:        result.Index,
			LocationCode: result.LocationCode,
			Succeeded:    result.Succeeded,
			Error:        result.Error,
		})
	}
	return importReportResponse{
		RowsSubmitted: report.RowsSubmitted,
		SlotsImported: report.SlotsImported,
		RowsRejected:  report.RowsRejected,
		Results:       results,
	}
}

func toSiteLayoutResponse(layout *usecases.SiteLayout) siteLayoutResponse {
	out := siteLayoutResponse{
		Site:            toSiteResponse(layout.Site),
		Zones:           make([]zoneLayoutResponse, 0, len(layout.Zones)),
		FixedStructures: make([]fixedStructureResponse, 0, len(layout.FixedStructures)),
	}

	for _, f := range layout.FixedStructures {
		out.FixedStructures = append(out.FixedStructures, toFixedStructureResponse(f))
	}

	for _, zoneLayout := range layout.Zones {
		zoneOut := zoneLayoutResponse{
			zoneResponse: toZoneResponse(zoneLayout.Zone),
			Aisles:       make([]aisleLayoutResponse, 0, len(zoneLayout.Aisles)),
		}
		for _, aisleLayout := range zoneLayout.Aisles {
			slots := make([]locationSlotResponse, 0, len(aisleLayout.Slots))
			for _, s := range aisleLayout.Slots {
				slots = append(slots, toLocationSlotResponse(s))
			}
			zoneOut.Aisles = append(zoneOut.Aisles, aisleLayoutResponse{
				aisleResponse: toAisleResponse(aisleLayout.Aisle),
				Slots:         slots,
			})
			out.Totals.Aisles++
			out.Totals.Slots += len(slots)
		}
		out.Zones = append(out.Zones, zoneOut)
		out.Totals.Zones++
	}
	return out
}

func toZoneGridResponse(grid *usecases.ZoneGrid) zoneGridResponse {
	out := zoneGridResponse{
		Zone:    toZoneResponse(grid.Zone),
		Columns: make([]gridColumnResponse, 0, len(grid.Columns)),
		Levels:  grid.Levels,
		Rows:    make([]gridRowResponse, 0, len(grid.Rows)),
	}
	if out.Levels == nil {
		out.Levels = []string{}
	}

	for _, column := range grid.Columns {
		out.Columns = append(out.Columns, gridColumnResponse{
			AisleID:      column.AisleID,
			AisleCode:    column.AisleCode,
			Bay:          column.Bay,
			SequenceHint: column.SequenceHint,
		})
	}

	for _, row := range grid.Rows {
		cells := make([]*gridCellResponse, 0, len(row.Cells))
		for _, cell := range row.Cells {
			if len(cell.Slots) == 0 {
				// A gap in the rack: JSON null, per CLAUDE.md's grid contract.
				cells = append(cells, nil)
				continue
			}
			positions := make([]gridPositionResponse, 0, len(cell.Slots))
			for _, s := range cell.Slots {
				positions = append(positions, gridPositionResponse{
					LocationCode: s.Code().String(),
					Position:     s.Code().Position(),
					LocationType: s.LocationType(),
					Status:       string(s.Status()),
				})
			}
			cells = append(cells, &gridCellResponse{Positions: positions})
		}
		out.Rows = append(out.Rows, gridRowResponse{Level: row.Level, Cells: cells})
	}
	return out
}

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
