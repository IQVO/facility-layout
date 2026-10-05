package http

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// SVG geometry. The plan is laid out top-down: one band per Zone, one row
// of slot rects per Aisle inside it.
const (
	svgMarginX     = 24
	svgZoneHeader  = 34
	svgAisleHeader = 18
	svgSlotWidth   = 26
	svgSlotHeight  = 16
	svgSlotGap     = 3
	svgAisleGap    = 6
	svgZoneGap     = 16
	svgLabelWidth  = 150
	svgMinWidth    = 640
)

// renderLayoutSVG draws a site layout as a minimal, standalone SVG floor
// plan: one titled band per Zone, one row of rects per Aisle, one small
// rect per LocationSlot, coloured by the zone's temperature class and
// hazmat flag.
//
// This is an ADAPTER-ONLY concern by design: it consumes the exact same
// read model the JSON layout endpoint does and adds no domain or
// application code. Nothing about SVG leaks inward.
func renderLayoutSVG(layout *usecases.SiteLayout) string {
	var body strings.Builder
	y := 40
	maxSlots := 0

	for _, zoneLayout := range layout.Zones {
		zoneHeight := svgZoneHeader
		for _, aisleLayout := range zoneLayout.Aisles {
			zoneHeight += svgAisleHeader + svgSlotHeight + svgAisleGap
			if n := len(aisleLayout.Slots); n > maxSlots {
				maxSlots = n
			}
		}

		fill, stroke := zoneColours(zoneLayout.Zone.TemperatureClass(), zoneLayout.Zone.Hazmat())
		// %%WIDTH%% is substituted once the widest aisle is known.
		writef(&body, `  <rect x="%d" y="%d" width="%%WIDTH%%" height="%d" fill="%s" stroke="%s" stroke-width="1" rx="6"/>`+"\n",
			svgMarginX, y, zoneHeight, fill, stroke)
		writef(&body, `  <text x="%d" y="%d" font-family="monospace" font-size="13" font-weight="bold" fill="#1f2933">%s</text>`+"\n",
			svgMarginX+10, y+21,
			escape(fmt.Sprintf("%s  (%s%s)", zoneLayout.Zone.ID(), zoneLayout.Zone.TemperatureClass(), hazmatSuffix(zoneLayout.Zone.Hazmat()))))

		rowY := y + svgZoneHeader
		for _, aisleLayout := range zoneLayout.Aisles {
			writef(&body, `  <text x="%d" y="%d" font-family="monospace" font-size="11" fill="#3e4c59">aisle %s (seq %d, %s)</text>`+"\n",
				svgMarginX+10, rowY+12, escape(aisleLayout.Aisle.AisleCode()),
				aisleLayout.Aisle.SequenceHint(), escape(string(aisleLayout.Aisle.Direction())))

			slotY := rowY + svgAisleHeader
			for i, s := range aisleLayout.Slots {
				slotX := svgMarginX + svgLabelWidth + i*(svgSlotWidth+svgSlotGap)
				writef(&body, `  <rect x="%d" y="%d" width="%d" height="%d" fill="%s" stroke="%s" stroke-width="0.5" rx="2"><title>%s</title></rect>`+"\n",
					slotX, slotY, svgSlotWidth, svgSlotHeight, slotColour(s.Status()), roleStroke(s.Role()),
					escape(fmt.Sprintf("%s (%s, %s, %s)", s.Code().String(), s.LocationType(), s.Role(), s.Status())))
				// ADR-0016: a per-role monogram distinguishes functional
				// locations (Dock, WorkCenter, …) from plain Storage at a
				// glance, without a legend an operator has to keep open. Storage
				// stays unmarked — it is the overwhelming majority and marking
				// every slot would be noise, not signal.
				if mono := roleMonogram(s.Role()); mono != "" {
					writef(&body, `  <text x="%d" y="%d" font-family="monospace" font-size="9" font-weight="bold" fill="#1f2933">%s</text>`+"\n",
						slotX+4, slotY+svgSlotHeight-4, mono)
				}
			}
			rowY = slotY + svgSlotHeight + svgAisleGap
		}

		y += zoneHeight + svgZoneGap
	}

	width := svgMarginX*2 + svgLabelWidth + maxSlots*(svgSlotWidth+svgSlotGap) + 40
	if width < svgMinWidth {
		width = svgMinWidth
	}
	height := y + 20

	var out strings.Builder
	writef(&out, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`+"\n", width, height, width, height)
	writef(&out, `  <title>%s facility layout</title>`+"\n", escape(layout.Site.Code()))
	writef(&out, `  <rect x="0" y="0" width="%d" height="%d" fill="#ffffff"/>`+"\n", width, height)
	writef(&out, `  <text x="%d" y="26" font-family="monospace" font-size="16" font-weight="bold" fill="#1f2933">%s — %s</text>`+"\n",
		svgMarginX, escape(layout.Site.Code()), escape(layout.Site.Name()))
	out.WriteString(strings.ReplaceAll(body.String(), "%WIDTH%", fmt.Sprintf("%d", width-svgMarginX*2)))
	out.WriteString("</svg>\n")
	return out.String()
}

// writef appends a formatted fragment. Writing to a strings.Builder cannot
// fail, so the error is deliberately discarded.
func writef(b *strings.Builder, format string, args ...any) {
	_, _ = fmt.Fprintf(b, format, args...)
}

// zoneColours picks a (fill, stroke) pair for a zone band: temperature
// class drives the hue, hazmat overrides it with a warning colour.
func zoneColours(temperatureClass shared.TemperatureClass, hazmat bool) (string, string) {
	if hazmat {
		return "#fff3c4", "#b44d12"
	}
	switch temperatureClass {
	case shared.Frozen:
		return "#e0f0ff", "#0b69a3"
	case shared.Chilled:
		return "#e3f9e5", "#207561"
	default:
		return "#f5f7fa", "#7b8794"
	}
}

// slotColour distinguishes an active slot from one out of service.
func slotColour(status shared.Status) string {
	switch status {
	case shared.Decommissioned:
		return "#e12d39"
	case shared.UnderMaintenance:
		return "#f7c948"
	default:
		return "#9fb3c8"
	}
}

// roleMonogram returns the two-letter monogram drawn inside a functional
// (non-Storage) slot's rect (ADR-0016), or "" for Storage. Deliberately
// minimal — the SVG floor plan is an operator overview, not a data grid;
// the full role is in the rect's <title> tooltip and the JSON layout/grid.
func roleMonogram(role placement.LocationRole) string {
	switch role {
	case placement.Dock:
		return "DK"
	case placement.Yard:
		return "YD"
	case placement.WorkCenter:
		return "WC"
	case placement.Drop:
		return "DP"
	case placement.Staging:
		return "ST"
	case placement.QC:
		return "QC"
	case placement.Consolidation:
		return "CO"
	case placement.Shipping:
		return "SH"
	default:
		return ""
	}
}

// roleStroke outlines a slot by its functional role (ADR-0016): Storage
// keeps the neutral default outline; every functional location gets a
// darker, visible outline so a Dock door or WorkCenter stands out on the
// plan even before the monogram is read.
func roleStroke(role placement.LocationRole) string {
	if roleMonogram(role) == "" {
		return "#52606d"
	}
	return "#1f2933"
}

func hazmatSuffix(hazmat bool) string {
	if hazmat {
		return ", HAZMAT"
	}
	return ""
}

// escape XML-escapes a value before it is interpolated into the document.
func escape(value string) string {
	var buf strings.Builder
	if err := xml.EscapeText(&buf, []byte(value)); err != nil {
		return ""
	}
	return buf.String()
}
