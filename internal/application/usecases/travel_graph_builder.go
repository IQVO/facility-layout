package usecases

import (
	"context"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/travel"
)

// buildZoneGraph assembles the pure-domain travel.Graph for one zone: its
// aisles (bays derived from the zone's registered slots, in walk order),
// each aisle's real centreline when it has one, and its active
// cross-aisles. It is the single place that turns persisted structural
// facts into the travel package's plain description, shared by
// GetZoneTravelGraph and EstimateTravelDistance so the two use cases can
// never disagree about how a zone's graph is built.
func buildZoneGraph(ctx context.Context, zones ports.ZoneRepo, aisles ports.AisleRepo, slots ports.SlotRepo, crossAisles ports.CrossAisleRepo, zoneID string) (travel.Graph, error) {
	z, err := zones.FindByID(ctx, zoneID)
	if err != nil {
		return travel.Graph{}, err
	}
	if z == nil {
		return travel.Graph{}, ErrZoneNotFound
	}

	zoneAisles, err := aisles.ListByZone(ctx, zoneID)
	if err != nil {
		return travel.Graph{}, err
	}

	zoneSlots, err := slots.ListByZone(ctx, zoneID)
	if err != nil {
		return travel.Graph{}, err
	}
	baysByAisle := make(map[string]map[string]struct{}, len(zoneAisles))
	for _, s := range zoneSlots {
		code := s.Code()
		if _, ok := baysByAisle[code.Aisle()]; !ok {
			baysByAisle[code.Aisle()] = make(map[string]struct{})
		}
		baysByAisle[code.Aisle()][code.Bay()] = struct{}{}
	}

	aisleGeoms := make([]travel.AisleGeom, 0, len(zoneAisles))
	for _, a := range zoneAisles {
		bays := sortedKeys(baysByAisle[a.AisleCode()])
		centrelineM := 0.0
		if !a.Centreline().IsZero() {
			centrelineM = a.Centreline().LengthM()
		}
		aisleGeoms = append(aisleGeoms, travel.AisleGeom{
			AisleID:     a.ID(),
			Bays:        bays,
			OneWay:      a.Direction() == shared.OneWay,
			CentrelineM: centrelineM,
		})
	}

	var crossRefs []travel.CrossAisleRef
	if crossAisles != nil {
		active, err := crossAisles.ListByZone(ctx, zoneID)
		if err != nil {
			return travel.Graph{}, err
		}
		for _, c := range active {
			if !c.IsActive() {
				continue
			}
			crossRefs = append(crossRefs, travel.CrossAisleRef{
				FromAisleID: zoneID + "-" + c.FromAisle(),
				ToAisleID:   zoneID + "-" + c.ToAisle(),
				AtBay:       c.AtBay(),
			})
		}
	}

	return travel.Build(aisleGeoms, crossRefs, travel.Pitch{BayPitchM: z.BayPitchM(), LevelPitchM: z.LevelPitchM()}), nil
}
