package usecases

import (
	"context"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/slot"
)

// DeclareLocationHandlingUnit sets or changes the unit of handling
// (EACH/CASE/PALLET) an existing coded slot accepts (ADR-0040). It is an
// update to an existing slot, not a registration: the slot must exist and
// must not be Decommissioned.
type DeclareLocationHandlingUnit struct {
	Slots  ports.SlotRepo
	Events ports.EventPublisher
	Clock  ports.Clock
	// UnitOfWork brackets Save + Publish atomically (ADR-0018, optional).
	UnitOfWork ports.UnitOfWork
}

// Execute validates handlingUnit, records it on the slot and publishes
// LocationSlotHandlingUnitDeclared. An unknown value is
// shared.ErrUnknownHandlingUnit; a missing slot ErrLocationSlotNotFound; a
// retired slot slot.ErrSlotDecommissioned.
func (uc *DeclareLocationHandlingUnit) Execute(ctx context.Context, code shared.LocationCode, handlingUnit string) (*slot.LocationSlot, error) {
	unit, err := shared.ParseHandlingUnit(handlingUnit)
	if err != nil {
		return nil, err
	}
	s, err := uc.Slots.FindByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrLocationSlotNotFound
	}
	if err := s.DeclareHandlingUnit(unit); err != nil {
		return nil, err
	}
	err = atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
		if err := uc.Slots.Save(ctx, s); err != nil {
			return err
		}
		return uc.Events.Publish(ctx, shared.NewLocationSlotHandlingUnitDeclared(uc.Clock.Now(), s.Code(), s.HandlingUnit()))
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}
