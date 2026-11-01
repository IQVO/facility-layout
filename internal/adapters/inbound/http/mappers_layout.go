package http

import (
	"github.com/claudioed/facility-layout/internal/application/usecases"
)

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
					Role:         string(s.Role()),
					Status:       string(s.Status()),
				})
			}
			cells = append(cells, &gridCellResponse{Positions: positions})
		}
		out.Rows = append(out.Rows, gridRowResponse{Level: row.Level, Cells: cells})
	}
	return out
}
