package features_test

// Step definitions for features/automated_storage.feature (ADR-0048): one
// AS/RS is one logical location. The scenarios run over the same world
// features_test.go builds (the real router over the in-memory adapters), so
// a refusal is read from the problem response the API answers.

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/cucumber/godog"
)

// asrsTypeCapacity is the default envelope every location type of the
// feature registers with: Storage and Drop require one (ADR-0016).
var asrsTypeCapacity = map[string]any{"maxWeightKg": 50000, "maxVolumeM3": 400}

var quotedName = regexp.MustCompile(`"([^"]*)"`)

// aSiteWithZoneAndAisles seeds Site siteCode, its Ambient Zone
// areaCode/zoneCode and every quoted aisle code in aisles, in order.
func (w *world) aSiteWithZoneAndAisles(ctx context.Context, siteCode, areaCode, zoneCode, aisles string) error {
	if err := w.aRegisteredSite(ctx, siteCode); err != nil {
		return err
	}
	if err := w.aRegisteredZone(ctx, areaCode, zoneCode, siteCode, "Ambient"); err != nil {
		return err
	}
	zoneID := siteCode + "-" + areaCode + "-" + zoneCode
	for i, m := range quotedName.FindAllStringSubmatch(aisles, -1) {
		if err := w.aRegisteredAisle(ctx, m[1], zoneID, i+1); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) theLocationTypeWithRole(ctx context.Context, name, role string) error {
	return w.seed(ctx, http.MethodPost, "/location-types", map[string]any{
		"name": name, "role": role, "defaultCapacity": asrsTypeCapacity,
	}, http.StatusCreated)
}

func (w *world) iRegisterTheLocationTypeWithRole(ctx context.Context, name, role string) error {
	return w.record(ctx, http.MethodPost, "/location-types", map[string]any{
		"name": name, "role": role, "defaultCapacity": asrsTypeCapacity,
	})
}

func (w *world) iRegisterAs(ctx context.Context, locationCode, locationType string) error {
	return w.record(ctx, http.MethodPost, "/locations", map[string]any{
		"locationCode": locationCode, "locationType": locationType,
	})
}

// listingRoleReturnsExactly lists the site's slots of role and compares their
// codes, as a set, with the comma-separated expected list.
func (w *world) listingRoleReturnsExactly(ctx context.Context, role, siteCode, expected string) error {
	if err := w.record(ctx, http.MethodGet, "/sites/"+siteCode+"/locations?role="+role, nil); err != nil {
		return err
	}
	if w.status != http.StatusOK {
		return fmt.Errorf("listing role %s: status %d: %s", role, w.status, string(w.body))
	}
	var items []struct {
		LocationCode string `json:"locationCode"`
	}
	if err := w.decodeItems(&items); err != nil {
		return err
	}
	got := make([]string, 0, len(items))
	for _, item := range items {
		got = append(got, item.LocationCode)
	}
	want := strings.Split(expected, ", ")
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return fmt.Errorf("role %s at %s lists %v, want exactly %v", role, siteCode, got, want)
	}
	return nil
}

// theRegistrationIsRefusedWith asserts the last response is the problem
// status/slug pair.
func (w *world) theRegistrationIsRefusedWith(status int, slug string) error {
	if err := w.theResponseStatusIs(status); err != nil {
		return err
	}
	return w.theProblemDetailTypeIs(slug)
}

func (w *world) registerAutomatedStorageSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a site "([^"]*)" with zone "([^"]*)" "([^"]*)" and aisles (.+)$`, w.aSiteWithZoneAndAisles)
	sc.Step(`^the location type "([^"]*)" with role "([^"]*)"$`, w.theLocationTypeWithRole)
	sc.Step(`^I register the location type "([^"]*)" with role "([^"]*)"$`, w.iRegisterTheLocationTypeWithRole)
	sc.Step(`^I register "([^"]*)" as "([^"]*)"$`, w.iRegisterAs)
	sc.Step(`^listing role "([^"]*)" for site "([^"]*)" returns exactly "([^"]*)"$`, w.listingRoleReturnsExactly)
	sc.Step(`^the registration is refused with (\d+) "([^"]*)"$`, w.theRegistrationIsRefusedWith)
}
