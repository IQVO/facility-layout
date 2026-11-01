package http

import (
	"errors"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
	"github.com/claudioed/facility-layout/internal/domain/slot"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

func toSiteResponse(s *site.Site) siteResponse {
	return siteResponse{SiteCode: s.Code(), Name: s.Name(), Status: string(s.Status()), SiteRole: string(s.Role())}
}

// fromRegisterSiteRole maps the optional registration role (ADR-0037): an
// omitted role is UNSPECIFIED, anything else must be a known role.
func fromRegisterSiteRole(raw string) (site.Role, error) {
	if raw == "" {
		return site.RoleUnspecified, nil
	}
	return site.ParseRole(raw)
}

// fromSetSiteRoleRequest maps the PUT /sites/{siteCode}/role body: the role
// is required and exact; the domain then rejects UNSPECIFIED.
func fromSetSiteRoleRequest(req setSiteRoleRequest) (site.Role, error) {
	return site.ParseRole(req.SiteRole)
}

func toSiteRoleTransferDefaultsResponse(rows []site.RoleTransferDefault) []siteRoleTransferDefaultResponse {
	out := make([]siteRoleTransferDefaultResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, siteRoleTransferDefaultResponse{
			SiteRole:                   string(row.Role),
			TransferOriginEnabled:      row.Capability.OriginEnabled(),
			TransferDestinationEnabled: row.Capability.DestinationEnabled(),
		})
	}
	return out
}

func toTransferCapabilityResponse(s *site.Site) transferCapabilityResponse {
	c := s.TransferCapability()
	return transferCapabilityResponse{
		SiteCode:                   s.Code(),
		TransferOriginEnabled:      c.OriginEnabled(),
		TransferDestinationEnabled: c.DestinationEnabled(),
		CapabilityRevision:         s.CapabilityRevision(),
	}
}

// errMissingTransferCapabilityField reports an omitted or null
// transferOriginEnabled/transferDestinationEnabled: the PUT body is a full
// snapshot (ADR-0035), and encoding/json would otherwise coerce the absence
// to false and silently switch a capability off.
var errMissingTransferCapabilityField = errors.New("transferOriginEnabled and transferDestinationEnabled are both required booleans")

// fromTransferCapabilityRequest maps the PUT body to the domain value object.
func fromTransferCapabilityRequest(req setTransferCapabilityRequest) (site.TransferCapability, error) {
	if req.TransferOriginEnabled == nil || req.TransferDestinationEnabled == nil {
		return site.TransferCapability{}, errMissingTransferCapabilityField
	}
	return site.NewTransferCapability(*req.TransferOriginEnabled, *req.TransferDestinationEnabled), nil
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
		BayPitchM:        z.BayPitchM(),
		LevelPitchM:      z.LevelPitchM(),
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
		HandlingUnit: string(s.HandlingUnit()),
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
