package usecases

import (
	"context"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// RegisterAisle adds a physical corridor inside a Zone. An aisle cannot be
// registered against an unknown or non-Active zone.
type RegisterAisle struct {
	Zones  ports.ZoneRepo
	Aisles ports.AisleRepo
	Events ports.EventPublisher
	Clock  ports.Clock
	// UnitOfWork brackets Save + Publish atomically (ADR-0018, optional).
	UnitOfWork ports.UnitOfWork
}

// Execute registers the aisle and publishes AisleRegistered.
func (uc *RegisterAisle) Execute(ctx context.Context, zoneID, aisleCode string, sequenceHint int, direction shared.Direction) (*aisle.Aisle, error) {
	parent, err := uc.Zones.FindByID(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, ErrZoneNotFound
	}
	if !parent.IsActive() {
		return nil, ErrZoneNotActive
	}

	a, err := aisle.NewAisle(zoneID, aisleCode, sequenceHint, direction)
	if err != nil {
		return nil, err
	}

	existing, err := uc.Aisles.FindByID(ctx, a.ID())
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrDuplicateAisle
	}

	err = atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
		if err := uc.Aisles.Save(ctx, a); err != nil {
			return err
		}
		event := shared.NewAisleRegistered(uc.Clock.Now(), a.ID(), a.ZoneID(), a.AisleCode(), a.SequenceHint(), a.Direction())
		return uc.Events.Publish(ctx, event)
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

// ListAisles reads every Aisle in a Zone.
type ListAisles struct {
	Zones  ports.ZoneRepo
	Aisles ports.AisleRepo
}

// Execute returns the zone's aisles, or ErrZoneNotFound if the zone is unknown.
func (uc *ListAisles) Execute(ctx context.Context, zoneID string) ([]*aisle.Aisle, error) {
	parent, err := uc.Zones.FindByID(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, ErrZoneNotFound
	}
	return uc.Aisles.ListByZone(ctx, zoneID)
}
