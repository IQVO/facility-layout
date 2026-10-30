package a2a

import (
	"fmt"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/site"
	"github.com/claudioed/facility-layout/internal/domain/structure"
	"github.com/claudioed/facility-layout/internal/domain/travel"
)

// problemTypeBase namespaces this context's problem types. It is the SAME
// base as the REST adapter's RFC 7807 "type" URIs (problemBaseURI there, which
// carries the trailing slash this constant leaves to each "/<slug>" suffix),
// so a slug means the same thing on every surface.
const problemTypeBase = "https://errors.facility-layout.warehouse-systems.dev"

// problem is the RFC 7807-shaped rejection an agent client receives instead
// of a result: domain rejections and request-shape failures are business
// answers, not transport errors, so they travel in the message envelope.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

// summary renders the one-line form used for the reply's text part.
func (p *problem) summary() string {
	return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail)
}

func problemMalformedRequest(detail string) *problem {
	return &problem{Type: problemTypeBase + "/malformed-skill-request", Title: "Malformed skill request", Detail: detail, Status: 400}
}

func problemUnknownSkill(skill string) *problem {
	return &problem{Type: problemTypeBase + "/unknown-skill", Title: "Unknown skill", Detail: fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", skill), Status: 400}
}

func problemInvalidArgument(err error) *problem {
	return &problem{Type: problemTypeBase + "/invalid-argument", Title: "Invalid skill argument", Detail: err.Error(), Status: 400}
}

// envelope is the structured payload of every skill answer: exactly one of
// Data (a skill DTO) or Problem (why there is no result), plus the skill id
// the answer belongs to.
type envelope struct {
	Skill   string   `json:"skill"`
	Data    skillDTO `json:"data,omitempty"`
	Problem *problem `json:"problem,omitempty"`
}

// skillDTO is the union of the skill result DTOs. A2A parts are
// machine-readable JSON; each skill fills its own field and the summary,
// keeping the envelope flat and versioned as one contract.
type skillDTO struct {
	SiteLayout     *siteLayoutDTO     `json:"siteLayout,omitempty"`
	TravelDistance *travelDistanceDTO `json:"travelDistance,omitempty"`
	summary        string
}

// Summary is the human-readable one-liner for the reply's text part.
func (s skillDTO) Summary() string { return s.summary }

// siteRef is the compact identity of a Site: code, name, role in the
// fulfilment network (ADR-0037) and transfer capability (ADR-0035;
// capabilityRevision 0 = never set, capable of nothing).
type siteRef struct {
	Code                       string `json:"code"`
	Name                       string `json:"name"`
	SiteRole                   string `json:"siteRole"`
	TransferOriginEnabled      bool   `json:"transferOriginEnabled"`
	TransferDestinationEnabled bool   `json:"transferDestinationEnabled"`
	CapabilityRevision         int64  `json:"capabilityRevision"`
}

// siteLayoutDTO is the compact, nested projection of one Site's structure --
// zones -> aisles -> slot codes, plus fixed structures -- mirroring the MCP
// get_site_layout DTO. Each slot is reduced to its code string, so the
// payload answers "what is the shape of this site", not "dump every slot".
type siteLayoutDTO struct {
	Site            siteRef             `json:"site"`
	Zones           []zoneLayoutDTO     `json:"zones"`
	FixedStructures []fixedStructureDTO `json:"fixedStructures,omitempty"`
}

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

type zoneLayoutDTO struct {
	ZoneID           string           `json:"zoneId"`
	AreaCode         string           `json:"areaCode"`
	ZoneCode         string           `json:"zoneCode"`
	TemperatureClass string           `json:"temperatureClass"`
	Hazmat           bool             `json:"hazmat"`
	Aisles           []aisleLayoutDTO `json:"aisles"`
}

type aisleLayoutDTO struct {
	AisleID      string   `json:"aisleId"`
	AisleCode    string   `json:"aisleCode"`
	SequenceHint int      `json:"sequenceHint"`
	Direction    string   `json:"direction"`
	SlotCodes    []string `json:"slotCodes"`
}

// travelNodeDTO is one aisle/bay waypoint on a travel route.
type travelNodeDTO struct {
	AisleID string `json:"aisleId"`
	Bay     string `json:"bay"`
}

// travelDistanceDTO is the outcome of travel_distance: the shortest path's
// total length in metres, whether any leg was estimated rather than
// measured, and the ordered waypoints traversed.
type travelDistanceDTO struct {
	From      string          `json:"from"`
	To        string          `json:"to"`
	MetresM   float64         `json:"metresM"`
	Estimated bool            `json:"estimated"`
	Route     []travelNodeDTO `json:"route"`
}

func toSiteRef(s *site.Site) siteRef {
	c := s.TransferCapability()
	return siteRef{
		Code:                       s.Code(),
		Name:                       s.Name(),
		SiteRole:                   string(s.Role()),
		TransferOriginEnabled:      c.OriginEnabled(),
		TransferDestinationEnabled: c.DestinationEnabled(),
		CapabilityRevision:         s.CapabilityRevision(),
	}
}

// toSiteLayoutDTO maps the GetSiteLayout read model into the bounded, nested
// DTO and its one-line summary.
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
			codes := make([]string, 0, len(al.Slots))
			for _, s := range al.Slots {
				codes = append(codes, s.Code().String())
			}
			z.Aisles = append(z.Aisles, aisleLayoutDTO{
				AisleID:      al.Aisle.ID(),
				AisleCode:    al.Aisle.AisleCode(),
				SequenceHint: al.Aisle.SequenceHint(),
				Direction:    string(al.Aisle.Direction()),
				SlotCodes:    codes,
			})
		}
		out.Zones = append(out.Zones, z)
	}
	return out
}

// summarizeSiteLayout renders the one-line text form of a site layout.
func summarizeSiteLayout(l siteLayoutDTO) string {
	aisles, slots := 0, 0
	for _, z := range l.Zones {
		aisles += len(z.Aisles)
		for _, a := range z.Aisles {
			slots += len(a.SlotCodes)
		}
	}
	return fmt.Sprintf("%s (%s): %d zones, %d aisles, %d slots, %d fixed structures", l.Site.Code, l.Site.Name, len(l.Zones), aisles, slots, len(l.FixedStructures))
}

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

func toTravelNodeDTO(n travel.Node) travelNodeDTO {
	return travelNodeDTO{AisleID: n.AisleID, Bay: n.Bay}
}

// toTravelDistanceDTO maps the EstimateTravelDistance read model into the
// DTO. from and to are echoed so the answer is self-describing.
func toTravelDistanceDTO(from, to string, d *usecases.TravelDistance) travelDistanceDTO {
	route := make([]travelNodeDTO, 0, len(d.Route))
	for _, n := range d.Route {
		route = append(route, toTravelNodeDTO(n))
	}
	return travelDistanceDTO{From: from, To: to, MetresM: d.MetresM, Estimated: d.Estimated, Route: route}
}

// summarizeTravelDistance renders the one-line text form of a distance.
func summarizeTravelDistance(t travelDistanceDTO) string {
	kind := "measured"
	if t.Estimated {
		kind = "estimated"
	}
	return fmt.Sprintf("%s -> %s: %.2f m (%s)", t.From, t.To, t.MetresM, kind)
}
