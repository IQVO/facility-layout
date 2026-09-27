package usecases

import (
	"context"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// RegisterLocationType defines a reusable classification of slot shape/kind
// with a default capacity envelope and a LocationRole (ADR-0016) saying
// what the type is for.
type RegisterLocationType struct {
	LocationTypes ports.LocationTypeRepo
	Events        ports.EventPublisher
	Clock         ports.Clock
	// UnitOfWork brackets Save + Publish atomically (ADR-0018, optional).
	UnitOfWork ports.UnitOfWork
}

// Execute registers the location type and publishes LocationTypeRegistered.
func (uc *RegisterLocationType) Execute(ctx context.Context, name string, role placement.LocationRole, defaultCapacity shared.Capacity) (placement.LocationType, error) {
	existing, err := uc.LocationTypes.FindByName(ctx, name)
	if err != nil {
		return placement.LocationType{}, err
	}
	if existing != nil {
		return placement.LocationType{}, ErrDuplicateLocationType
	}

	lt, err := placement.NewLocationType(name, role, defaultCapacity)
	if err != nil {
		return placement.LocationType{}, err
	}
	err = atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
		if err := uc.LocationTypes.Save(ctx, lt); err != nil {
			return err
		}
		return uc.Events.Publish(ctx, shared.NewLocationTypeRegistered(uc.Clock.Now(), lt.Name(), string(lt.Role()), lt.DefaultCapacity()))
	})
	if err != nil {
		return placement.LocationType{}, err
	}
	return lt, nil
}

// ListLocationTypes reads every registered LocationType.
type ListLocationTypes struct {
	LocationTypes ports.LocationTypeRepo
}

// Execute returns every location type.
func (uc *ListLocationTypes) Execute(ctx context.Context) ([]placement.LocationType, error) {
	return uc.LocationTypes.List(ctx)
}
