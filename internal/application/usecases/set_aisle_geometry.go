package usecases

import (
	"context"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// SetAisleGeometry records an aisle's straight-line travel centreline
// (ADR-0017), used by the travel graph as this aisle's walkable path. It
// is an update to an existing aisle, not a registration: the aisle must
// already exist and must not be Decommissioned.
type SetAisleGeometry struct {
	Aisles ports.AisleRepo
	Events ports.EventPublisher
	Clock  ports.Clock
	// UnitOfWork brackets Save + Publish atomically (ADR-0018, optional).
	UnitOfWork ports.UnitOfWork
}

// Execute sets the aisle's centreline and publishes AisleGeometryUpdated.
func (uc *SetAisleGeometry) Execute(ctx context.Context, aisleID string, centreline shared.Segment) (*aisle.Aisle, error) {
	a, err := uc.Aisles.FindByID(ctx, aisleID)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, ErrAisleNotFound
	}
	if err := a.SetCentreline(centreline); err != nil {
		return nil, err
	}
	err = atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
		if err := uc.Aisles.Save(ctx, a); err != nil {
			return err
		}
		event := shared.NewAisleGeometryUpdated(uc.Clock.Now(), a.ID(), a.Centreline())
		return uc.Events.Publish(ctx, event)
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}
