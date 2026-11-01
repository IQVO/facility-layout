package memory

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/slot"
)

// SlotRepo is an in-memory implementation of ports.SlotRepo.
type SlotRepo struct {
	mu    sync.RWMutex
	slots map[string]*slot.LocationSlot
}

// NewSlotRepo builds an empty SlotRepo.
func NewSlotRepo() *SlotRepo {
	return &SlotRepo{slots: make(map[string]*slot.LocationSlot)}
}

// Save stores the slot under its location code.
func (r *SlotRepo) Save(_ context.Context, s *slot.LocationSlot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.slots[s.Code().String()] = s
	return nil
}

// FindByCode returns the slot, or (nil, nil) when it does not exist.
func (r *SlotRepo) FindByCode(_ context.Context, code shared.LocationCode) (*slot.LocationSlot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.slots[code.String()]
	if !ok {
		return nil, nil
	}
	return s, nil
}

// ListByAisle returns every slot whose code resolves to aisleID.
func (r *SlotRepo) ListByAisle(_ context.Context, aisleID string) ([]*slot.LocationSlot, error) {
	return r.listByPrefix(aisleID), nil
}

// ListByZone returns every slot whose code resolves to zoneID.
func (r *SlotRepo) ListByZone(_ context.Context, zoneID string) ([]*slot.LocationSlot, error) {
	return r.listByPrefix(zoneID), nil
}

func (r *SlotRepo) listByPrefix(prefix string) []*slot.LocationSlot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*slot.LocationSlot, 0)
	for code, s := range r.slots {
		if strings.HasPrefix(code, prefix+"-") {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code().String() < out[j].Code().String() })
	return out
}

// LocationTypeRepo is an in-memory implementation of ports.LocationTypeRepo.
type LocationTypeRepo struct {
	mu    sync.RWMutex
	types map[string]placement.LocationType
}

// NewLocationTypeRepo builds an empty LocationTypeRepo.
func NewLocationTypeRepo() *LocationTypeRepo {
	return &LocationTypeRepo{types: make(map[string]placement.LocationType)}
}

// Save stores the location type under its name.
func (r *LocationTypeRepo) Save(_ context.Context, t placement.LocationType) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.types[t.Name()] = t
	return nil
}

// FindByName returns the location type, or (nil, nil) when it does not exist.
func (r *LocationTypeRepo) FindByName(_ context.Context, name string) (*placement.LocationType, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.types[name]
	if !ok {
		return nil, nil
	}
	return &t, nil
}

// List returns every location type, ordered by name.
func (r *LocationTypeRepo) List(_ context.Context) ([]placement.LocationType, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]placement.LocationType, 0, len(r.types))
	for _, t := range r.types {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

// PlacementRuleRepo is an in-memory implementation of ports.PlacementRuleRepo.
type PlacementRuleRepo struct {
	mu    sync.RWMutex
	rules map[string]placement.PlacementRule
}

// NewPlacementRuleRepo builds an empty PlacementRuleRepo.
func NewPlacementRuleRepo() *PlacementRuleRepo {
	return &PlacementRuleRepo{rules: make(map[string]placement.PlacementRule)}
}

// Save stores the rule under its id.
func (r *PlacementRuleRepo) Save(_ context.Context, rule placement.PlacementRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rules[rule.ID()] = rule
	return nil
}

// FindByID returns the rule, or (nil, nil) when it does not exist.
func (r *PlacementRuleRepo) FindByID(_ context.Context, id string) (*placement.PlacementRule, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rule, ok := r.rules[id]
	if !ok {
		return nil, nil
	}
	return &rule, nil
}

// List returns every rule, ordered by id.
func (r *PlacementRuleRepo) List(_ context.Context) ([]placement.PlacementRule, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]placement.PlacementRule, 0, len(r.rules))
	for _, rule := range r.rules {
		out = append(out, rule)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}
