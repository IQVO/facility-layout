package features_test

// Wave-3 step definitions: documentation-derived rules (invariants, state
// transitions, policies and failure modes of docs/contexts/facility-layout)
// that no earlier scenario pinned. The seeds below reach states no REST
// command produces in v1 (a Decommissioned site or zone, an UnderMaintenance
// slot or aisle: "reachable only by rehydrating a persisted row"), the
// recorded slot-registration metric, and the /readyz drain gate.

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/cucumber/godog"

	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/slot"
)

// recordingMetrics is the ports.LocationMetrics test double: it keeps every
// outcome a slot registration attempt was counted under.
type recordingMetrics struct {
	mu       sync.Mutex
	outcomes []string
}

func (m *recordingMetrics) LocationSlotRegistered(_ context.Context, outcome string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outcomes = append(m.outcomes, outcome)
}

func (m *recordingMetrics) count(outcome string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, got := range m.outcomes {
		if got == outcome {
			n++
		}
	}
	return n
}

func (m *recordingMetrics) total() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.outcomes)
}

// downDependencies lets a scenario make a named /readyz dependency fail and
// recover, the way a broker or database outage would.
type downDependencies struct {
	mu   sync.Mutex
	down map[string]bool
}

func (d *downDependencies) check(name string) func(context.Context) error {
	return func(context.Context) error {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.down[name] {
			return errors.New(name + " unreachable")
		}
		return nil
	}
}

func (d *downDependencies) set(name string, down bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.down[name] = down
}

// ------------------------------------------------------------ lifecycle ----

func (w *world) theSiteHasBeenDecommissioned(ctx context.Context, code string) error {
	s, err := w.sites.FindByCode(ctx, code)
	if err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("site %q does not exist", code)
	}
	if err := s.Decommission(); err != nil {
		return err
	}
	return w.sites.Save(ctx, s)
}

func (w *world) theZoneHasBeenDecommissioned(ctx context.Context, id string) error {
	z, err := w.zones.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if z == nil {
		return fmt.Errorf("zone %q does not exist", id)
	}
	if err := z.Decommission(); err != nil {
		return err
	}
	return w.zones.Save(ctx, z)
}

// theSlotIsUnderMaintenance rehydrates a registered slot with the legal
// persisted status no use case sets, as an external facility-management
// system would have loaded it.
func (w *world) theSlotIsUnderMaintenance(ctx context.Context, locationCode string) error {
	code, err := shared.ParseLocationCode(locationCode)
	if err != nil {
		return err
	}
	s, err := w.slots.FindByCode(ctx, code)
	if err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("slot %q does not exist", locationCode)
	}
	return w.slots.Save(ctx, slot.RehydrateLocationSlot(
		s.Code(), s.LocationType(), s.Role(), s.Functional(), s.Capacity(),
		shared.UnderMaintenance, s.Position(), s.Dimensions(), s.PickSequence(), s.Version(),
	))
}

func (w *world) theAisleIsUnderMaintenance(ctx context.Context, aisleID string) error {
	a, err := w.aisles.FindByID(ctx, aisleID)
	if err != nil {
		return err
	}
	if a == nil {
		return fmt.Errorf("aisle %q does not exist", aisleID)
	}
	return w.aisles.Save(ctx, aisle.RehydrateAisle(
		a.ZoneID(), a.AisleCode(), a.SequenceHint(), a.Direction(), shared.UnderMaintenance, a.Centreline(),
	))
}

func (w *world) theHeartbeatIsDisabled() error {
	w.heartbeat.Interval = 0
	return nil
}

// --------------------------------------------------------------- metrics ----

func (w *world) theSlotRegistrationCounterShows(n int, outcome string) error {
	if got := w.metrics.count(outcome); got != n {
		return fmt.Errorf("slot registrations counted as %q: got %d, want %d (all outcomes: %v)", outcome, got, n, w.metrics.outcomes)
	}
	return nil
}

func (w *world) theSlotRegistrationCounterHasNoOtherOutcome(total int) error {
	if got := w.metrics.total(); got != total {
		return fmt.Errorf("slot registration attempts counted: got %d, want %d (%v)", got, total, w.metrics.outcomes)
	}
	return nil
}

// ------------------------------------------------------------- readiness ----

func (w *world) theServiceIsDraining() error {
	w.readiness.SetNotReady()
	return nil
}

func (w *world) theDependencyIs(name, state string) error {
	w.downDeps.set(name, state == "unreachable")
	return nil
}

// ------------------------------------------------------------- read side ---

func (w *world) theResponseListHasNoDuplicates(path, field string) error {
	got, ok, err := w.jsonAt(path)
	if err != nil {
		return err
	}
	list, isList := got.([]any)
	if !ok || !isList {
		return fmt.Errorf("%q is not a list: %s", path, w.body)
	}
	seen := map[string]bool{}
	for _, item := range list {
		obj, _ := item.(map[string]any)
		key := fmt.Sprint(obj[field])
		if seen[key] {
			return fmt.Errorf("duplicate %s %q in %q", field, key, path)
		}
		seen[key] = true
	}
	return nil
}

func (w *world) theResponseListOmits(path, field, value string) error {
	got, ok, err := w.jsonAt(path)
	if err != nil {
		return err
	}
	list, isList := got.([]any)
	if !ok || !isList {
		return fmt.Errorf("%q is not a list: %s", path, w.body)
	}
	for _, item := range list {
		obj, _ := item.(map[string]any)
		if fmt.Sprint(obj[field]) == value {
			return fmt.Errorf("%q must not list %s %q: %s", path, field, value, w.body)
		}
	}
	return nil
}

func (w *world) theResponseListHasAn(path, field, value string) error {
	got, ok, err := w.jsonAt(path)
	if err != nil {
		return err
	}
	list, isList := got.([]any)
	if !ok || !isList {
		return fmt.Errorf("%q is not a list: %s", path, w.body)
	}
	for _, item := range list {
		obj, _ := item.(map[string]any)
		if fmt.Sprint(obj[field]) == value {
			return nil
		}
	}
	return fmt.Errorf("%q has no item with %s %q: %s", path, field, value, w.body)
}

func (w *world) registerRulesSteps(sc *godog.ScenarioContext) {
	sc.Step(`^the Site "([^"]*)" has been decommissioned$`, w.theSiteHasBeenDecommissioned)
	sc.Step(`^the Zone "([^"]*)" has been decommissioned$`, w.theZoneHasBeenDecommissioned)
	sc.Step(`^the LocationSlot "([^"]*)" is under maintenance$`, w.theSlotIsUnderMaintenance)
	sc.Step(`^the Aisle "([^"]*)" is under maintenance$`, w.theAisleIsUnderMaintenance)
	sc.Step(`^the site capability heartbeat is disabled$`, w.theHeartbeatIsDisabled)

	sc.Step(`^the slot registration counter shows (\d+) "([^"]*)" outcomes?$`, w.theSlotRegistrationCounterShows)
	sc.Step(`^(\d+) slot registration attempts have been counted in all$`, w.theSlotRegistrationCounterHasNoOtherOutcome)

	sc.Step(`^the service is draining$`, w.theServiceIsDraining)
	sc.Step(`^the dependency "([^"]*)" is (unreachable|reachable again)$`, w.theDependencyIs)

	sc.Step(`^the "([^"]*)" of the items in "([^"]*)" are all different$`, func(field, path string) error {
		return w.theResponseListHasNoDuplicates(path, field)
	})
	sc.Step(`^no item in "([^"]*)" has the "([^"]*)" "([^"]*)"$`, w.theResponseListOmits)
	sc.Step(`^an item in "([^"]*)" has the "([^"]*)" "([^"]*)"$`, w.theResponseListHasAn)
}
