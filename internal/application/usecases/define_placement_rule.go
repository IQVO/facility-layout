package usecases

import (
	"context"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// DefinePlacementRule declares which LocationTypes are legal in which
// Zones. A rule may not reference a LocationType that does not exist.
type DefinePlacementRule struct {
	LocationTypes ports.LocationTypeRepo
	Rules         ports.PlacementRuleRepo
	Events        ports.EventPublisher
	Clock         ports.Clock
	// UnitOfWork brackets Save + Publish atomically (ADR-0018, optional).
	UnitOfWork ports.UnitOfWork
}

// Execute defines the rule and publishes PlacementRuleDefined.
func (uc *DefinePlacementRule) Execute(ctx context.Context, id, locationType string, effect placement.Effect, predicate placement.ZonePredicate) (placement.PlacementRule, error) {
	referenced, err := uc.LocationTypes.FindByName(ctx, locationType)
	if err != nil {
		return placement.PlacementRule{}, err
	}
	if referenced == nil {
		return placement.PlacementRule{}, ErrLocationTypeNotFound
	}

	existing, err := uc.Rules.FindByID(ctx, id)
	if err != nil {
		return placement.PlacementRule{}, err
	}
	if existing != nil {
		return placement.PlacementRule{}, ErrDuplicatePlacementRule
	}

	rule, err := placement.NewPlacementRule(id, locationType, effect, predicate)
	if err != nil {
		return placement.PlacementRule{}, err
	}
	err = atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
		if err := uc.Rules.Save(ctx, rule); err != nil {
			return err
		}
		event := shared.NewPlacementRuleDefined(uc.Clock.Now(), rule.ID(), rule.LocationType(), string(rule.Effect()), rule.Predicate().String())
		return uc.Events.Publish(ctx, event)
	})
	if err != nil {
		return placement.PlacementRule{}, err
	}
	return rule, nil
}

// ListPlacementRules reads every defined PlacementRule.
type ListPlacementRules struct {
	Rules ports.PlacementRuleRepo
}

// Execute returns every placement rule.
func (uc *ListPlacementRules) Execute(ctx context.Context) ([]placement.PlacementRule, error) {
	return uc.Rules.List(ctx)
}
