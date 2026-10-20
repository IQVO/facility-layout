package features_test

// Step definitions for features/site_role.feature (ADR-0037): the published
// SiteRegistered / SiteRoleChanged facts are asserted from the domain events
// the real HTTP server raised, and the transfer-defaults table from the
// response body.

import (
	"encoding/json"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

func (w *world) theLastSiteRegisteredCarriesRole(siteCode, role string) error {
	var last *shared.SiteRegistered
	for _, e := range w.publisher.Events() {
		if r, ok := e.(shared.SiteRegistered); ok && r.SiteCode == siteCode {
			last = &r
		}
	}
	if last == nil {
		return fmt.Errorf("no SiteRegistered was published for site %q", siteCode)
	}
	if last.SiteRole != role {
		return fmt.Errorf("SiteRegistered for %s carries role %q, want %q", siteCode, last.SiteRole, role)
	}
	return nil
}

func (w *world) theLastSiteRoleChangedIs(siteCode, role, previous string) error {
	var last *shared.SiteRoleChanged
	for _, e := range w.publisher.Events() {
		if c, ok := e.(shared.SiteRoleChanged); ok && c.SiteCode == siteCode {
			last = &c
		}
	}
	if last == nil {
		return fmt.Errorf("no SiteRoleChanged was published for site %q", siteCode)
	}
	if last.SiteRole != role || last.PreviousSiteRole != previous {
		return fmt.Errorf("last SiteRoleChanged for %s: %q replacing %q, want %q replacing %q",
			siteCode, last.SiteRole, last.PreviousSiteRole, role, previous)
	}
	return nil
}

type transferDefaultRow struct {
	SiteRole                   string `json:"siteRole"`
	TransferOriginEnabled      bool   `json:"transferOriginEnabled"`
	TransferDestinationEnabled bool   `json:"transferDestinationEnabled"`
}

func (w *world) theTransferDefaultIs(role, origin, destination string) error {
	var rows []transferDefaultRow
	if err := json.Unmarshal(w.body, &rows); err != nil {
		return fmt.Errorf("decode transfer defaults: %w (body %s)", err, w.body)
	}
	for _, row := range rows {
		if row.SiteRole != role {
			continue
		}
		got := fmt.Sprintf("origin %v, destination %v", row.TransferOriginEnabled, row.TransferDestinationEnabled)
		want := fmt.Sprintf("origin %s, destination %s", origin, destination)
		if got != want {
			return fmt.Errorf("transfer default for %s: got %q, want %q", role, got, want)
		}
		return nil
	}
	return fmt.Errorf("no transfer default for role %q in %s", role, w.body)
}

func (w *world) registerSiteRoleSteps(sc *godog.ScenarioContext) {
	sc.Step(`^the last SiteRegistered for Site "([^"]*)" carries role "([^"]*)"$`, w.theLastSiteRegisteredCarriesRole)
	sc.Step(`^the last SiteRoleChanged for Site "([^"]*)" is "([^"]*)" replacing "([^"]*)"$`, w.theLastSiteRoleChangedIs)
	sc.Step(`^the transfer default for "([^"]*)" is origin (true|false), destination (true|false)$`, w.theTransferDefaultIs)
}
