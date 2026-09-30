package placement_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

func mustPredicate(t *testing.T, zoneCode string, temperatureClass shared.TemperatureClass, hazmat *bool) placement.ZonePredicate {
	t.Helper()
	p, err := placement.NewZonePredicate(zoneCode, temperatureClass, hazmat)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return p
}

func mustRule(t *testing.T, id, locationType string, effect placement.Effect, predicate placement.ZonePredicate) placement.PlacementRule {
	t.Helper()
	rule, err := placement.NewPlacementRule(id, locationType, effect, predicate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return rule
}

func TestRuleSetCheck(t *testing.T) {
	hazZone := placement.ZoneAttributes{ZoneID: "WH1-STOR-HAZ", ZoneCode: "HAZ", TemperatureClass: shared.Ambient, Hazmat: true}
	frozenZone := placement.ZoneAttributes{ZoneID: "WH1-STOR-FRZ", ZoneCode: "FRZ", TemperatureClass: shared.Frozen}
	ambientZone := placement.ZoneAttributes{ZoneID: "WH1-STOR-AMB", ZoneCode: "AMB", TemperatureClass: shared.Ambient}

	// Only a PalletRack may be placed in the HAZ zone; nothing that is not
	// rated for cold may be placed anywhere Frozen.
	onlyPalletRackInHazmat := mustRule(t, "RULE-HAZ-ONLY-RACK", placement.PalletRack, placement.Allow, mustPredicate(t, "HAZ", "", nil))
	noShelvingInFrozen := mustRule(t, "RULE-FRZ-NO-SHELF", placement.Shelf, placement.Deny, mustPredicate(t, "", shared.Frozen, nil))
	rules := placement.RuleSet{onlyPalletRackInHazmat, noShelvingInFrozen}

	tests := []struct {
		name         string
		rules        placement.RuleSet
		locationType string
		zone         placement.ZoneAttributes
		wantErr      bool
		wantMentions string
	}{
		{name: "allow-listed type in an allow-listed zone", rules: rules, locationType: placement.PalletRack, zone: hazZone},
		{name: "non-allow-listed type in an allow-listed zone", rules: rules, locationType: placement.Shelf, zone: hazZone, wantErr: true, wantMentions: "RULE-HAZ-ONLY-RACK"},
		{name: "explicitly denied type", rules: rules, locationType: placement.Shelf, zone: frozenZone, wantErr: true, wantMentions: "RULE-FRZ-NO-SHELF"},
		{name: "a type with no rule touching it in a frozen zone", rules: rules, locationType: placement.PalletRack, zone: frozenZone},
		{name: "an unconstrained zone permits anything", rules: rules, locationType: placement.BulkFloor, zone: ambientZone},
		{name: "an empty rule set permits anything", rules: nil, locationType: placement.Amnesty, zone: hazZone},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.rules.Check(tc.locationType, tc.zone)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, placement.ErrPlacementRuleViolated) {
				t.Fatalf("expected ErrPlacementRuleViolated, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantMentions) {
				t.Fatalf("the error must name the violated rule %q, got %q", tc.wantMentions, err.Error())
			}
			if !strings.Contains(err.Error(), tc.zone.ZoneID) {
				t.Fatalf("the error must name the zone %q, got %q", tc.zone.ZoneID, err.Error())
			}
		})
	}
}

func TestRuleSetDenyWinsOverAllow(t *testing.T) {
	frozenZone := placement.ZoneAttributes{ZoneID: "WH1-STOR-FRZ", ZoneCode: "FRZ", TemperatureClass: shared.Frozen}

	rules := placement.RuleSet{
		mustRule(t, "RULE-ALLOW-SHELF", placement.Shelf, placement.Allow, mustPredicate(t, "FRZ", "", nil)),
		mustRule(t, "RULE-DENY-SHELF", placement.Shelf, placement.Deny, mustPredicate(t, "", shared.Frozen, nil)),
	}

	err := rules.Check(placement.Shelf, frozenZone)
	if !errors.Is(err, placement.ErrPlacementRuleViolated) {
		t.Fatalf("Deny must win over Allow, got %v", err)
	}
	if !strings.Contains(err.Error(), "RULE-DENY-SHELF") {
		t.Fatalf("expected the Deny rule to be named, got %q", err.Error())
	}
}

func TestRuleSetIgnoresRulesForOtherZones(t *testing.T) {
	ambientZone := placement.ZoneAttributes{ZoneID: "WH1-STOR-AMB", ZoneCode: "AMB", TemperatureClass: shared.Ambient}

	rules := placement.RuleSet{
		mustRule(t, "RULE-HAZ-ONLY-RACK", placement.PalletRack, placement.Allow, mustPredicate(t, "HAZ", "", nil)),
		mustRule(t, "RULE-FRZ-NO-SHELF", placement.Shelf, placement.Deny, mustPredicate(t, "", shared.Frozen, nil)),
	}

	if err := rules.Check(placement.Shelf, ambientZone); err != nil {
		t.Fatalf("rules for other zones must not apply: %v", err)
	}
}

func TestRuleSetAllowListMessageNamesEveryMatchingAllowRule(t *testing.T) {
	hazZone := placement.ZoneAttributes{ZoneID: "WH1-STOR-HAZ", ZoneCode: "HAZ", TemperatureClass: shared.Ambient, Hazmat: true}

	rules := placement.RuleSet{
		mustRule(t, "RULE-HAZ-ONLY-RACK", placement.PalletRack, placement.Allow, mustPredicate(t, "HAZ", "", nil)),
		mustRule(t, "RULE-HAZ-ONLY-SHELF", placement.Shelf, placement.Allow, mustPredicate(t, "", shared.Ambient, nil)),
	}

	err := rules.Check(placement.BulkFloor, hazZone)
	if !errors.Is(err, placement.ErrPlacementRuleViolated) {
		t.Fatalf("expected ErrPlacementRuleViolated, got %v", err)
	}
	// The rejection must spell out the whole allow-list: both matching Allow
	// rules, joined so a reader can tell them apart.
	if !strings.Contains(err.Error(), "RULE-HAZ-ONLY-RACK") || !strings.Contains(err.Error(), "RULE-HAZ-ONLY-SHELF") {
		t.Fatalf("expected both allow rules named, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "]; [") {
		t.Fatalf("expected the allow rules joined by \"; \", got %q", err.Error())
	}
}
