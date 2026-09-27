package usecases

import (
	"context"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/slot"
)

// SetLocationGeometry records a coded slot's physical position, footprint,
// and (optionally) an explicit pick-sequence override (ADR-0017). It is an
// update to an existing slot, not a registration: the slot must already
// exist and must not be Decommissioned.
type SetLocationGeometry struct {
	Slots  ports.SlotRepo
	Events ports.EventPublisher
	Clock  ports.Clock
	// UnitOfWork brackets Save + Publish atomically (ADR-0018, optional).
	UnitOfWork ports.UnitOfWork
}

// Execute sets the slot's geometry and publishes LocationGeometryUpdated.
// pickSequence may be nil, meaning "leave any existing override unchanged"
// — only position/dimensions are set by this call in that case.
func (uc *SetLocationGeometry) Execute(ctx context.Context, code shared.LocationCode, position shared.Point3D, dimensions shared.Dimensions, pickSequence *int) (*slot.LocationSlot, error) {
	s, err := uc.Slots.FindByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrLocationSlotNotFound
	}
	if err := s.SetGeometry(position, dimensions); err != nil {
		return nil, err
	}
	if pickSequence != nil {
		if err := s.SetPickSequence(*pickSequence); err != nil {
			return nil, err
		}
	}
	err = atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
		if err := uc.Slots.Save(ctx, s); err != nil {
			return err
		}
		event := shared.NewLocationGeometryUpdated(uc.Clock.Now(), s.Code(), s.Position(), s.Dimensions(), s.PickSequence())
		return uc.Events.Publish(ctx, event)
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}
