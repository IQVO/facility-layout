package usecases

import (
	"context"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// checkAutomatedStorage enforces ADR-0048 for a slot about to be registered:
// an aisle holds at most one active AutomatedStorage slot (one AS/RS is one
// logical location), and an AsrsPort slot only goes in an aisle that holds
// one. Every other location type passes untouched.
func checkAutomatedStorage(ctx context.Context, slots ports.SlotRepo, code shared.LocationCode, locationType string) error {
	if locationType != placement.AutomatedStorage && locationType != placement.AsrsPort {
		return nil
	}
	has, err := aisleHoldsAutomatedStorage(ctx, slots, code.AisleID())
	if err != nil {
		return err
	}
	switch {
	case locationType == placement.AutomatedStorage && has:
		return ErrAutomatedStorageAlreadyInAisle
	case locationType == placement.AsrsPort && !has:
		return ErrAsrsPortWithoutAutomatedStorage
	}
	return nil
}

func aisleHoldsAutomatedStorage(ctx context.Context, slots ports.SlotRepo, aisleID string) (bool, error) {
	inAisle, err := slots.ListByAisle(ctx, aisleID)
	if err != nil {
		return false, err
	}
	for _, s := range inAisle {
		if s.LocationType() == placement.AutomatedStorage && s.Status() != shared.Decommissioned {
			return true, nil
		}
	}
	return false, nil
}
