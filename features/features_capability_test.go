package features_test

// Step definitions for features/site_transfer_capability.feature (ADR-0035):
// the published SiteCapabilityChanged snapshot is asserted from the domain
// events the real HTTP server raised, never from the repository.

import (
	"context"
	"fmt"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

func (w *world) theLastSiteCapabilityChangedIs(siteCode, origin, destination string, revision int64) error {
	var last *shared.SiteCapabilityChanged
	for _, e := range w.publisher.Events() {
		if c, ok := e.(shared.SiteCapabilityChanged); ok && c.SiteCode == siteCode {
			last = &c
		}
	}
	if last == nil {
		return fmt.Errorf("no SiteCapabilityChanged was published for site %q", siteCode)
	}
	got := fmt.Sprintf("origin %v, destination %v at revision %d", last.TransferOriginEnabled, last.TransferDestinationEnabled, last.CapabilityRevision)
	want := fmt.Sprintf("origin %s, destination %s at revision %d", origin, destination, revision)
	if got != want {
		return fmt.Errorf("last SiteCapabilityChanged for %s: got %q, want %q", siteCode, got, want)
	}
	return nil
}

func (w *world) minutesPass(minutes int) error {
	w.clock.Advance(time.Duration(minutes) * time.Minute)
	return nil
}

func (w *world) theCapabilityHeartbeatRuns() error {
	_, err := w.heartbeat.Execute(context.Background())
	return err
}

func (w *world) capabilityHeartbeatsAreEmittedSoFar(siteCode string, n int) error {
	var got int
	for _, e := range w.publisher.Events() {
		if c, ok := e.(shared.SiteCapabilityChanged); ok && c.SiteCode == siteCode {
			got++
		}
	}
	if got != n {
		return fmt.Errorf("SiteCapabilityChanged events for %s: got %d, want %d", siteCode, got, n)
	}
	return nil
}

func (w *world) theLastSiteCapabilityChangedWasEmittedMinutesAfterStart(minutes int) error {
	var last *shared.SiteCapabilityChanged
	for _, e := range w.publisher.Events() {
		if c, ok := e.(shared.SiteCapabilityChanged); ok {
			last = &c
		}
	}
	if last == nil {
		return fmt.Errorf("no SiteCapabilityChanged was published")
	}
	want := time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC).Add(time.Duration(minutes) * time.Minute)
	if !last.OccurredAt().Equal(want) {
		return fmt.Errorf("last SiteCapabilityChanged time = %v, want %v (a fresh as_of)", last.OccurredAt(), want)
	}
	return nil
}

func (w *world) registerCapabilitySteps(sc *godog.ScenarioContext) {
	sc.Step(`^(\d+) minutes? pass(?:es)?$`, w.minutesPass)
	sc.Step(`^the site capability heartbeat runs$`, w.theCapabilityHeartbeatRuns)
	sc.Step(`^exactly (\d+) SiteCapabilityChanged events? (?:has|have) been emitted for Site "([^"]*)"$`, func(n int, code string) error {
		return w.capabilityHeartbeatsAreEmittedSoFar(code, n)
	})
	sc.Step(`^the last SiteCapabilityChanged carries a time (\d+) minutes after the start$`, w.theLastSiteCapabilityChangedWasEmittedMinutesAfterStart)
	sc.Step(`^the last SiteCapabilityChanged for Site "([^"]*)" is origin (true|false), destination (true|false) at revision (\d+)$`, w.theLastSiteCapabilityChangedIs)
}
