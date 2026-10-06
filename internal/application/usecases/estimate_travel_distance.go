package usecases

import (
	"context"
	"math"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/slot"
	"github.com/claudioed/facility-layout/internal/domain/travel"
)

// EstimateTravelDistance computes the shortest travel distance between two
// coded locations over the pure-domain travel graph (ADR-0017). It is a
// read model: no state is stored, no event is published. This context
// only reports the map's TOPOLOGY — travel TIME, congestion, and route
// choice under load remain wes-work-planning's concern.
type EstimateTravelDistance struct {
	Zones       ports.ZoneRepo
	Aisles      ports.AisleRepo
	Slots       ports.SlotRepo
	CrossAisles ports.CrossAisleRepo
}

// TravelDistance is the outcome of a distance query: the total length in
// metres, whether any leg of the route was estimated rather than measured,
// and the ordered aisle/bay waypoints traversed.
type TravelDistance struct {
	MetresM   float64
	Estimated bool
	Route     []travel.Node
}

// Execute resolves from and to to their zones and slots. Same-zone
// requests route over the zone's travel graph (aisles, bays, cross-aisles).
// Cross-zone requests are honoured ONLY when both endpoints carry real
// position geometry (ADR-0017's "refuse where neither zone has geometry"):
// the answer is the straight-line (beeline) distance between the two
// positions, flagged estimated=true because it is not a routed path — the
// travel graph is per-zone and cross-aisles are zone-scoped, so no honest
// routed cross-zone path exists yet. Without geometry on both endpoints it
// still refuses (ErrNoRouteBetweenZones) rather than inventing a number.
// Both locations must exist (ErrLocationSlotNotFound).
func (uc *EstimateTravelDistance) Execute(ctx context.Context, from, to shared.LocationCode) (*TravelDistance, error) {
	fromSlot, err := uc.Slots.FindByCode(ctx, from)
	if err != nil {
		return nil, err
	}
	if fromSlot == nil {
		return nil, ErrLocationSlotNotFound
	}
	toSlot, err := uc.Slots.FindByCode(ctx, to)
	if err != nil {
		return nil, err
	}
	if toSlot == nil {
		return nil, ErrLocationSlotNotFound
	}

	if from.ZoneID() != to.ZoneID() {
		return crossZoneBeeline(fromSlot, toSlot)
	}

	graph, err := buildZoneGraph(ctx, uc.Zones, uc.Aisles, uc.Slots, uc.CrossAisles, from.ZoneID())
	if err != nil {
		return nil, err
	}

	fromNode := travel.Node{AisleID: from.AisleID(), Bay: from.Bay()}
	toNode := travel.Node{AisleID: to.AisleID(), Bay: to.Bay()}
	route, err := graph.Distance(fromNode, toNode)
	if err != nil {
		return nil, err
	}
	return &TravelDistance{MetresM: route.MetresM, Estimated: route.Estimated, Route: route.Nodes}, nil
}

// crossZoneBeeline is the geometry-backed cross-zone estimate ADR-0017
// allows: the 3D straight-line distance between the two slots' recorded
// positions, flagged estimated (it ignores aisles, racks and walls — it is
// a lower bound a consumer can reason about, not a routed path). It
// refuses with ErrNoRouteBetweenZones when either endpoint has no recorded
// position: with the graph per-zone, a pitch-based guess across a zone
// boundary is exactly the false precision the ADR refuses to publish.
func crossZoneBeeline(fromSlot, toSlot *slot.LocationSlot) (*TravelDistance, error) {
	fromPos, toPos := fromSlot.Position(), toSlot.Position()
	if fromPos.IsZero() || toPos.IsZero() {
		return nil, ErrNoRouteBetweenZones
	}
	dx := fromPos.XM() - toPos.XM()
	dy := fromPos.YM() - toPos.YM()
	dz := fromPos.ZM() - toPos.ZM()
	metres := math.Sqrt(dx*dx + dy*dy + dz*dz)
	from, to := fromSlot.Code(), toSlot.Code()
	return &TravelDistance{
		MetresM:   metres,
		Estimated: true,
		Route: []travel.Node{
			{AisleID: from.AisleID(), Bay: from.Bay()},
			{AisleID: to.AisleID(), Bay: to.Bay()},
		},
	}, nil
}
