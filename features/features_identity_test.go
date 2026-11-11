package features_test

// Step definitions for features/location_identity.feature (ADR-0045): the
// published registration events are asserted on their WIRE data (what a
// consumer decodes), so an omitted optional field is checked as omitted.

import (
	"encoding/json"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/claudioed/facility-layout/internal/adapters/kafka/cloudevents"
)

// lastEventData returns the wire `data` of the last published event with the
// given name.
func (w *world) lastEventData(name string) (map[string]any, error) {
	var found map[string]any
	for _, e := range w.publisher.Events() {
		if e.EventName() != name {
			continue
		}
		data, err := cloudevents.WireData(e)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		found = map[string]any{}
		if err := json.Unmarshal(raw, &found); err != nil {
			return nil, err
		}
	}
	if found == nil {
		return nil, fmt.Errorf("no %q domain event was published", name)
	}
	return found, nil
}

func (w *world) theLastEventHasWireField(name, field, want string) error {
	data, err := w.lastEventData(name)
	if err != nil {
		return err
	}
	if got, ok := data[field]; !ok || fmt.Sprint(got) != want {
		return fmt.Errorf("last %s: %q = %v (present=%v), want %q", name, field, got, ok, want)
	}
	return nil
}

func (w *world) theLastEventHasNoWireField(name, field string) error {
	data, err := w.lastEventData(name)
	if err != nil {
		return err
	}
	if got, ok := data[field]; ok {
		return fmt.Errorf("last %s must omit %q, got %v", name, field, got)
	}
	return nil
}

func (w *world) registerIdentitySteps(sc *godog.ScenarioContext) {
	sc.Step(`^the last "([^"]*)" domain event has the field "([^"]*)" equal to "([^"]*)"$`, w.theLastEventHasWireField)
	sc.Step(`^the last "([^"]*)" domain event has no field "([^"]*)"$`, w.theLastEventHasNoWireField)
}
