package usecases

import (
	"context"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

// RegisterZone adds a behavioral zone inside a Site's area. A zone cannot
// be registered against an unknown or non-Active site.
//
// bayPitchM/levelPitchM (ADR-0017) are optional overrides for the travel
// graph's estimated-distance fallback: nil (or both nil) keeps the zone on
// DefaultBayPitchM/DefaultLevelPitchM; supplying exactly one of the two is
// rejected (zone.ErrInvalidPitch → 422) — a half-specified pitch would
// silently mix an override with a default.
type RegisterZone struct {
	Sites  ports.SiteRepo
	Zones  ports.ZoneRepo
	Events ports.EventPublisher
	Clock  ports.Clock
	// UnitOfWork brackets Save + Publish atomically (ADR-0018, optional).
	UnitOfWork ports.UnitOfWork
}

// Execute registers the zone and publishes ZoneRegistered.
func (uc *RegisterZone) Execute(ctx context.Context, siteCode, areaCode, zoneCode string, temperatureClass shared.TemperatureClass, hazmat bool, bayPitchM, levelPitchM *float64) (*zone.Zone, error) {
	parent, err := uc.Sites.FindByCode(ctx, siteCode)
	if err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, ErrSiteNotFound
	}
	if !parent.IsActive() {
		return nil, ErrSiteNotActive
	}

	z, err := zone.NewZone(siteCode, areaCode, zoneCode, temperatureClass, hazmat)
	if err != nil {
		return nil, err
	}
	if bayPitchM != nil || levelPitchM != nil {
		var bay, level float64
		if bayPitchM != nil {
			bay = *bayPitchM
		}
		if levelPitchM != nil {
			level = *levelPitchM
		}
		if err := z.SetPitch(bay, level); err != nil {
			return nil, err
		}
	}

	existing, err := uc.Zones.FindByID(ctx, z.ID())
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrDuplicateZone
	}

	err = atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
		if err := uc.Zones.Save(ctx, z); err != nil {
			return err
		}
		event := shared.NewZoneRegistered(uc.Clock.Now(), z.ID(), z.SiteCode(), z.AreaCode(), z.ZoneCode(), z.TemperatureClass(), z.Hazmat())
		return uc.Events.Publish(ctx, event)
	})
	if err != nil {
		return nil, err
	}
	return z, nil
}

// ListZones reads every Zone in a Site.
type ListZones struct {
	Sites ports.SiteRepo
	Zones ports.ZoneRepo
}

// Execute returns the site's zones, or ErrSiteNotFound if the site is unknown.
func (uc *ListZones) Execute(ctx context.Context, siteCode string) ([]*zone.Zone, error) {
	parent, err := uc.Sites.FindByCode(ctx, siteCode)
	if err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, ErrSiteNotFound
	}
	return uc.Zones.ListBySite(ctx, siteCode)
}

// ListCrossAisles reads every cross-aisle in a zone (ADR-0017): the
// zone-scoped connections between its aisles, ordered from-aisle, to-aisle,
// bay — the same order the repo returns. A read model: no writes, no
// events. POST /zones/{zoneId}/cross-aisles already existed; this closes
// the read half so a consumer can discover the declared connections
// without deriving them from the travel graph's edges.
type ListCrossAisles struct {
	Zones       ports.ZoneRepo
	CrossAisles ports.CrossAisleRepo
}

// Execute returns the zone's cross-aisles, or ErrZoneNotFound.
func (uc *ListCrossAisles) Execute(ctx context.Context, zoneID string) ([]*aisle.CrossAisle, error) {
	z, err := uc.Zones.FindByID(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	if z == nil {
		return nil, ErrZoneNotFound
	}
	return uc.CrossAisles.ListByZone(ctx, zoneID)
}
