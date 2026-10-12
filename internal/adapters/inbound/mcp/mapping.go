// Package mcp is the inbound Model Context Protocol adapter: it exposes this
// bounded context to the AI ecosystem as a second driving adapter over the
// same application-layer use cases the HTTP adapter uses. It is built on the
// official MCP Go SDK and served over Streamable HTTP.
//
// Per ADR-0007 and the MCP governance charter, this package depends inward on
// the application layer (use cases and ports) and the domain only — never on
// an outbound adapter. The composition root (cmd/mcp) wires concrete
// repositories into the use cases and query port. Tool handlers call use
// cases; domain structs never leak across the tool boundary.
//
// facility-layout is a read-only Open Host Service: its warehouse map is
// consumed, never mutated, by the rest of the estate. This server therefore
// registers only read tools, a resource, and a prompt — no write tool.
package mcp

import (
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/site"
	"github.com/claudioed/facility-layout/internal/domain/slot"
	"github.com/claudioed/facility-layout/internal/domain/structure"
	"github.com/claudioed/facility-layout/internal/domain/travel"
)

// tool-boundary DTOs -----------------------------------------------------------
//
// These are the ONLY shapes that cross the tool boundary. Domain structs
// (site.Site, zone.Zone, aisle.Aisle, slot.LocationSlot) never leave this
// package: they are mapped into the compact, bounded DTOs below, sized for an
// agent to reason over rather than for completeness.

// siteRef is the compact identity of a Site: its code and human name, plus
// its transfer capability and the revision of the last published
// SiteCapabilityChanged (ADR-0035; revision 0 = never set, capable of
// nothing). It is the entry in list_sites and the header of a site layout.
type siteRef struct {
	Code                       string `json:"code"`
	Name                       string `json:"name"`
	TransferOriginEnabled      bool   `json:"transferOriginEnabled"`
	TransferDestinationEnabled bool   `json:"transferDestinationEnabled"`
	CapabilityRevision         int64  `json:"capabilityRevision"`
}

// siteLayoutDTO is the compact, nested projection of one Site's structure —
// zones -> aisles -> slot codes -> fixed structures — returned by
// get_site_layout. It is a bounded view: each slot is reduced to its code
// string, not the full aggregate, so the payload stays scoped to "what is
// the shape of this site" rather than dumping every slot's capacity
// envelope. Structures and per-slot geometry (ADR-0017) are included only
// when actually present.
type siteLayoutDTO struct {
	Site            siteRef             `json:"site"`
	Zones           []zoneLayoutDTO     `json:"zones"`
	FixedStructures []fixedStructureDTO `json:"fixedStructures,omitempty"`
}

// fixedStructureDTO is the compact projection of one FixedStructure
// (ADR-0017): its kind, footprint, and label.
type fixedStructureDTO struct {
	ID      string  `json:"id"`
	Kind    string  `json:"kind"`
	XM      float64 `json:"xM"`
	YM      float64 `json:"yM"`
	ZM      float64 `json:"zM"`
	WidthM  float64 `json:"widthM"`
	DepthM  float64 `json:"depthM"`
	HeightM float64 `json:"heightM"`
	Label   string  `json:"label"`
}

// zoneLayoutDTO is one Zone and its aisles within a site layout.
type zoneLayoutDTO struct {
	ZoneID           string           `json:"zoneId"`
	AreaCode         string           `json:"areaCode"`
	ZoneCode         string           `json:"zoneCode"`
	TemperatureClass string           `json:"temperatureClass"`
	Hazmat           bool             `json:"hazmat"`
	Aisles           []aisleLayoutDTO `json:"aisles"`
}

// aisleLayoutDTO is one Aisle and the codes of the slots inside it, in
// walk/coordinate order. Slots are their location-code strings only — the
// intent-level "map", not the full slot records.
type aisleLayoutDTO struct {
	AisleID      string   `json:"aisleId"`
	AisleCode    string   `json:"aisleCode"`
	SequenceHint int      `json:"sequenceHint"`
	Direction    string   `json:"direction"`
	SlotCodes    []string `json:"slotCodes"`
}

// zoneGridDTO is the compact projection of one Zone's drawable grid, returned
// by get_zone_grid. Rows are Levels, columns are (Aisle, Bay) pairs in walk
// order, and each cell holds the location-code strings at that coordinate.
type zoneGridDTO struct {
	ZoneID  string          `json:"zoneId"`
	Columns []gridColumnDTO `json:"columns"`
	Levels  []string        `json:"levels"`
	Rows    []gridRowDTO    `json:"rows"`
}

// gridColumnDTO is one (Aisle, Bay) column of the grid.
type gridColumnDTO struct {
	AisleID      string `json:"aisleId"`
	AisleCode    string `json:"aisleCode"`
	Bay          string `json:"bay"`
	SequenceHint int    `json:"sequenceHint"`
}

// gridRowDTO is one Level of the grid; Cells is index-aligned with Columns.
type gridRowDTO struct {
	Level string        `json:"level"`
	Cells []gridCellDTO `json:"cells"`
}

// gridCellDTO holds the slot codes at one (Aisle, Bay, Level) coordinate.
// An empty SlotCodes is a gap in the rack.
type gridCellDTO struct {
	SlotCodes []string `json:"slotCodes"`
}

// functionalLocationDTO is the compact projection of a non-storage-role
// slot returned by list_functional_locations: code, coordinates, role, and
// the role-conditional dockFlow/activities (ADR-0016). Deliberately not
// the full LocationSlot shape (no locationType/capacity/status noise) —
// the tool answers "where are this site's dock doors", not "describe this
// slot in full".
type functionalLocationDTO struct {
	LocationCode string   `json:"locationCode"`
	ZoneID       string   `json:"zoneId"`
	AisleID      string   `json:"aisleId"`
	Role         string   `json:"role"`
	DockFlow     string   `json:"dockFlow,omitempty"`
	Activities   []string `json:"activities,omitempty"`
}

// toFunctionalLocationDTO maps a domain LocationSlot to its compact tool DTO.
func toFunctionalLocationDTO(s *slot.LocationSlot) functionalLocationDTO {
	code := s.Code()
	f := s.Functional()
	return functionalLocationDTO{
		LocationCode: code.String(),
		ZoneID:       code.ZoneID(),
		AisleID:      code.AisleID(),
		Role:         string(s.Role()),
		DockFlow:     string(f.DockFlow()),
		Activities:   activityStringsMCP(f.Activities()),
	}
}

// activityStringsMCP converts a slot's Activity set to plain strings, or nil
// when there are none, so "activities" is omitted for every non-WorkCenter
// location in the tool response.
func activityStringsMCP(activities []slot.Activity) []string {
	if len(activities) == 0 {
		return nil
	}
	out := make([]string, 0, len(activities))
	for _, a := range activities {
		out = append(out, string(a))
	}
	return out
}

// mapping functions ------------------------------------------------------------

// toSiteRef maps a domain Site to its compact reference DTO.
func toSiteRef(s *site.Site) siteRef {
	c := s.TransferCapability()
	return siteRef{
		Code:                       s.Code(),
		Name:                       s.Name(),
		TransferOriginEnabled:      c.OriginEnabled(),
		TransferDestinationEnabled: c.DestinationEnabled(),
		CapabilityRevision:         s.CapabilityRevision(),
	}
}

// toSiteLayoutDTO maps the GetSiteLayout read model into the bounded,
// nested tool DTO. Nothing but this file's DTOs crosses the tool boundary.
func toSiteLayoutDTO(layout *usecases.SiteLayout) siteLayoutDTO {
	out := siteLayoutDTO{Site: toSiteRef(layout.Site), Zones: make([]zoneLayoutDTO, 0, len(layout.Zones))}
	for _, f := range layout.FixedStructures {
		out.FixedStructures = append(out.FixedStructures, toFixedStructureDTO(f))
	}
	for _, zl := range layout.Zones {
		z := zoneLayoutDTO{
			ZoneID:           zl.Zone.ID(),
			AreaCode:         zl.Zone.AreaCode(),
			ZoneCode:         zl.Zone.ZoneCode(),
			TemperatureClass: string(zl.Zone.TemperatureClass()),
			Hazmat:           zl.Zone.Hazmat(),
			Aisles:           make([]aisleLayoutDTO, 0, len(zl.Aisles)),
		}
		for _, al := range zl.Aisles {
			z.Aisles = append(z.Aisles, aisleLayoutDTO{
				AisleID:      al.Aisle.ID(),
				AisleCode:    al.Aisle.AisleCode(),
				SequenceHint: al.Aisle.SequenceHint(),
				Direction:    string(al.Aisle.Direction()),
				SlotCodes:    slotCodes(al.Slots),
			})
		}
		out.Zones = append(out.Zones, z)
	}
	return out
}

// toZoneGridDTO maps the GetZoneGrid read model into the compact grid DTO.
func toZoneGridDTO(grid *usecases.ZoneGrid) zoneGridDTO {
	out := zoneGridDTO{
		ZoneID:  grid.Zone.ID(),
		Columns: make([]gridColumnDTO, 0, len(grid.Columns)),
		Levels:  grid.Levels,
		Rows:    make([]gridRowDTO, 0, len(grid.Rows)),
	}
	for _, c := range grid.Columns {
		out.Columns = append(out.Columns, gridColumnDTO{
			AisleID:      c.AisleID,
			AisleCode:    c.AisleCode,
			Bay:          c.Bay,
			SequenceHint: c.SequenceHint,
		})
	}
	for _, r := range grid.Rows {
		row := gridRowDTO{Level: r.Level, Cells: make([]gridCellDTO, 0, len(r.Cells))}
		for _, cell := range r.Cells {
			row.Cells = append(row.Cells, gridCellDTO{SlotCodes: slotCodes(cell.Slots)})
		}
		out.Rows = append(out.Rows, row)
	}
	return out
}

// slotCodes reduces a list of LocationSlot aggregates to their code strings,
// the only slot detail the map-level tools expose.
func slotCodes(slots []*slot.LocationSlot) []string {
	codes := make([]string, 0, len(slots))
	for _, s := range slots {
		codes = append(codes, s.Code().String())
	}
	return codes
}

// toFixedStructureDTO maps a domain FixedStructure to its compact tool DTO.
func toFixedStructureDTO(f *structure.FixedStructure) fixedStructureDTO {
	footprint := f.Footprint()
	return fixedStructureDTO{
		ID:      f.ID(),
		Kind:    string(f.Kind()),
		XM:      footprint.Origin().XM(),
		YM:      footprint.Origin().YM(),
		ZM:      footprint.Origin().ZM(),
		WidthM:  footprint.Size().WidthM(),
		DepthM:  footprint.Size().DepthM(),
		HeightM: footprint.Size().HeightM(),
		Label:   f.Label(),
	}
}

// travelNodeDTO is one aisle/bay waypoint on the travel graph (ADR-0017).
type travelNodeDTO struct {
	AisleID string `json:"aisleId"`
	Bay     string `json:"bay"`
}

// toTravelNodeDTO maps a domain travel.Node to its tool DTO.
func toTravelNodeDTO(n travel.Node) travelNodeDTO {
	return travelNodeDTO{AisleID: n.AisleID, Bay: n.Bay}
}

// travelDistanceDTO is the outcome of estimate_travel_distance: the
// shortest path's total length, whether any leg was estimated rather than
// measured, and the ordered waypoints traversed.
type travelDistanceDTO struct {
	MetresM   float64         `json:"metresM"`
	Estimated bool            `json:"estimated"`
	Route     []travelNodeDTO `json:"route"`
}

// toTravelDistanceDTO maps the EstimateTravelDistance read model into the
// tool DTO.
func toTravelDistanceDTO(d *usecases.TravelDistance) travelDistanceDTO {
	route := make([]travelNodeDTO, 0, len(d.Route))
	for _, n := range d.Route {
		route = append(route, toTravelNodeDTO(n))
	}
	return travelDistanceDTO{MetresM: d.MetresM, Estimated: d.Estimated, Route: route}
}

// travelEdgeDTO is one directed, weighted connection between two waypoints
// on the travel graph.
type travelEdgeDTO struct {
	From      travelNodeDTO `json:"from"`
	To        travelNodeDTO `json:"to"`
	MetresM   float64       `json:"metresM"`
	Estimated bool          `json:"estimated"`
}

// travelGraphDTO is a zone's full travel graph: every waypoint and every
// directed edge between them, returned by get_zone_travel_graph.
type travelGraphDTO struct {
	Nodes []travelNodeDTO `json:"nodes"`
	Edges []travelEdgeDTO `json:"edges"`
}

// toTravelGraphDTO maps the GetZoneTravelGraph read model into the tool DTO.
func toTravelGraphDTO(view *usecases.TravelGraphView) travelGraphDTO {
	out := travelGraphDTO{Nodes: make([]travelNodeDTO, 0, len(view.Nodes)), Edges: make([]travelEdgeDTO, 0, len(view.Edges))}
	for _, n := range view.Nodes {
		out.Nodes = append(out.Nodes, toTravelNodeDTO(n))
	}
	for _, e := range view.Edges {
		out.Edges = append(out.Edges, travelEdgeDTO{
			From:      toTravelNodeDTO(e.From),
			To:        toTravelNodeDTO(e.To),
			MetresM:   e.MetresM,
			Estimated: e.Estimated,
		})
	}
	return out
}
