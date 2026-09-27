package usecases

import (
	"context"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// RegisterCrossAisle adds a zone-scoped connection between two of its
// aisles at a bay ordinal to the travel graph (ADR-0017). Both aisle codes
// must resolve to Aisles that already exist in the named zone — this is
// the chain-of-custody check the constructor cannot perform on its own,
// since CrossAisle has no repository access.
type RegisterCrossAisle struct {
	Zones       ports.ZoneRepo
	Aisles      ports.AisleRepo
	CrossAisles ports.CrossAisleRepo
	Events      ports.EventPublisher
	Clock       ports.Clock
	// UnitOfWork brackets Save + Publish atomically (ADR-0018, optional).
	UnitOfWork ports.UnitOfWork
}

// Execute registers the cross-aisle and publishes CrossAisleRegistered.
func (uc *RegisterCrossAisle) Execute(ctx context.Context, zoneID, fromAisleCode, toAisleCode, atBay string) (*aisle.CrossAisle, error) {
	z, err := uc.Zones.FindByID(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	if z == nil {
		return nil, ErrZoneNotFound
	}

	aisles, err := uc.Aisles.ListByZone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	if !aisleInZone(aisles, fromAisleCode) || !aisleInZone(aisles, toAisleCode) {
		return nil, ErrCrossAisleAisleMismatch
	}

	existing, err := uc.CrossAisles.FindByAisles(ctx, zoneID, fromAisleCode, toAisleCode, atBay)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrDuplicateCrossAisle
	}

	c, err := aisle.NewCrossAisle(zoneID, fromAisleCode, toAisleCode, atBay)
	if err != nil {
		return nil, err
	}
	err = atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
		if err := uc.CrossAisles.Save(ctx, c); err != nil {
			return err
		}
		event := shared.NewCrossAisleRegistered(uc.Clock.Now(), zoneID, fromAisleCode, toAisleCode, atBay)
		return uc.Events.Publish(ctx, event)
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

func aisleInZone(aisles []*aisle.Aisle, code string) bool {
	for _, a := range aisles {
		if a.AisleCode() == code {
			return true
		}
	}
	return false
}
