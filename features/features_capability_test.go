package features_test

// Step definitions for features/site_transfer_capability.feature (ADR-0035):
// the published SiteCapabilityChanged snapshot is asserted from the domain
// events the real HTTP server raised, never from the repository.

import (
	"fmt"

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

func (w *world) registerCapabilitySteps(sc *godog.ScenarioContext) {
	sc.Step(`^the last SiteCapabilityChanged for Site "([^"]*)" is origin (true|false), destination (true|false) at revision (\d+)$`, w.theLastSiteCapabilityChangedIs)
}
