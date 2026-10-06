package usecases_test

import (
	"testing"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

func TestRegisterSite(t *testing.T) {
	t.Run("registers a site and publishes SiteRegistered", func(t *testing.T) {
		h := newHarness(t)
		s, err := h.registerSite.Execute(h.ctx(), "WH1", "Fulfilment Centre One")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.Code() != "WH1" || !s.IsActive() {
			t.Fatalf("unexpected site %q/%q", s.Code(), s.Status())
		}
		h.assertPublished("SiteRegistered")
	})

	t.Run("rejects a duplicate site code", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")
		_, err := h.registerSite.Execute(h.ctx(), "WH1", "A Different Name")
		assertErrorIs(t, err, usecases.ErrDuplicateSite)
	})

	t.Run("rejects a malformed site code without publishing", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.registerSite.Execute(h.ctx(), "wh1", "Fulfilment Centre One")
		assertErrorIs(t, err, site.ErrInvalidSiteCode)
		h.assertNotPublished("SiteRegistered")
	})
}

func TestGetAndListSites(t *testing.T) {
	h := newHarness(t)
	h.mustRegisterSite("WH2", "Fulfilment Centre Two")
	h.mustRegisterSite("WH1", "Fulfilment Centre One")

	got, err := h.getSite.Execute(h.ctx(), "WH1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name() != "Fulfilment Centre One" {
		t.Fatalf("unexpected site %q", got.Name())
	}

	if _, err := h.getSite.Execute(h.ctx(), "NOPE"); !isErr(err, usecases.ErrSiteNotFound) {
		t.Fatalf("expected ErrSiteNotFound, got %v", err)
	}

	all, err := h.listSites.Execute(h.ctx())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(all) != 2 || all[0].Code() != "WH1" || all[1].Code() != "WH2" {
		t.Fatalf("expected sites ordered by code, got %v", all)
	}
}

func TestRegisterZone(t *testing.T) {
	t.Run("registers a zone under an active site", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")
		z, err := h.registerZone.Execute(h.ctx(), "WH1", "STOR", "FRZ", shared.Frozen, false, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if z.ID() != "WH1-STOR-FRZ" || z.TemperatureClass() != shared.Frozen {
			t.Fatalf("unexpected zone %+v", z)
		}
		h.assertPublished("ZoneRegistered")
	})

	t.Run("rejects an unknown parent site", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.registerZone.Execute(h.ctx(), "NOPE", "STOR", "AMB", shared.Ambient, false, nil, nil)
		assertErrorIs(t, err, usecases.ErrSiteNotFound)
		h.assertNotPublished("ZoneRegistered")
	})

	t.Run("rejects a decommissioned parent site", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")
		decommissionSite(t, h, "WH1")
		_, err := h.registerZone.Execute(h.ctx(), "WH1", "STOR", "AMB", shared.Ambient, false, nil, nil)
		assertErrorIs(t, err, usecases.ErrSiteNotActive)
	})

	t.Run("rejects a duplicate zone", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")
		h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
		_, err := h.registerZone.Execute(h.ctx(), "WH1", "STOR", "AMB", shared.Chilled, true, nil, nil)
		assertErrorIs(t, err, usecases.ErrDuplicateZone)
	})

	t.Run("rejects an invalid zone definition", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")
		_, err := h.registerZone.Execute(h.ctx(), "WH1", "STOR", "AMB", "Tepid", false, nil, nil)
		assertErrorIs(t, err, shared.ErrUnknownTemperatureClass)
	})
}

func TestListZones(t *testing.T) {
	h := newHarness(t)
	h.mustRegisterSite("WH1", "Fulfilment Centre One")
	h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
	h.mustRegisterZone("WH1", "RCV", "AMB", shared.Ambient, false)

	zones, err := h.listZones.Execute(h.ctx(), "WH1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(zones) != 2 || zones[0].ID() != "WH1-RCV-AMB" {
		t.Fatalf("expected zones ordered by id, got %v", zones)
	}

	if _, err := h.listZones.Execute(h.ctx(), "NOPE"); !isErr(err, usecases.ErrSiteNotFound) {
		t.Fatalf("expected ErrSiteNotFound, got %v", err)
	}
}

func TestRegisterAisle(t *testing.T) {
	t.Run("registers an aisle under an active zone", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")
		h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
		a, err := h.registerAisle.Execute(h.ctx(), "WH1-STOR-AMB", "A07", 7, shared.TwoWay)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.ID() != "WH1-STOR-AMB-A07" || a.SequenceHint() != 7 {
			t.Fatalf("unexpected aisle %+v", a)
		}
		h.assertPublished("AisleRegistered")
	})

	t.Run("rejects an unknown parent zone", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.registerAisle.Execute(h.ctx(), "WH1-STOR-NOPE", "A07", 7, shared.TwoWay)
		assertErrorIs(t, err, usecases.ErrZoneNotFound)
	})

	t.Run("rejects a decommissioned parent zone", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")
		h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
		decommissionZone(t, h, "WH1-STOR-AMB")
		_, err := h.registerAisle.Execute(h.ctx(), "WH1-STOR-AMB", "A07", 7, shared.TwoWay)
		assertErrorIs(t, err, usecases.ErrZoneNotActive)
	})

	t.Run("rejects a duplicate aisle", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")
		h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
		h.mustRegisterAisle("WH1-STOR-AMB", "A07", 7, shared.TwoWay)
		_, err := h.registerAisle.Execute(h.ctx(), "WH1-STOR-AMB", "A07", 8, shared.OneWay)
		assertErrorIs(t, err, usecases.ErrDuplicateAisle)
	})

	t.Run("rejects an invalid aisle definition", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")
		h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
		_, err := h.registerAisle.Execute(h.ctx(), "WH1-STOR-AMB", "A07", -1, shared.TwoWay)
		assertErrorIs(t, err, aisle.ErrNegativeSequenceHint)

		_, err = h.registerAisle.Execute(h.ctx(), "WH1-STOR-AMB", "A07", 7, "Sideways")
		assertErrorIs(t, err, shared.ErrUnknownDirection)
	})
}

func TestListAisles(t *testing.T) {
	h := newHarness(t)
	h.mustRegisterSite("WH1", "Fulfilment Centre One")
	h.mustRegisterZone("WH1", "STOR", "AMB", shared.Ambient, false)
	h.mustRegisterAisle("WH1-STOR-AMB", "A09", 9, shared.TwoWay)
	h.mustRegisterAisle("WH1-STOR-AMB", "A07", 7, shared.TwoWay)

	aisles, err := h.listAisles.Execute(h.ctx(), "WH1-STOR-AMB")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(aisles) != 2 || aisles[0].AisleCode() != "A07" {
		t.Fatalf("expected aisles in walk order, got %v", aisles)
	}

	if _, err := h.listAisles.Execute(h.ctx(), "NOPE"); !isErr(err, usecases.ErrZoneNotFound) {
		t.Fatalf("expected ErrZoneNotFound, got %v", err)
	}
}

func TestRegisterLocationType(t *testing.T) {
	t.Run("registers a location type", func(t *testing.T) {
		h := newHarness(t)
		lt, err := h.registerLocationType.Execute(h.ctx(), placement.PalletRack, placement.Storage, mustCapacity(t, 1200, 2.4))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lt.Name() != placement.PalletRack {
			t.Fatalf("unexpected location type %q", lt.Name())
		}
		h.assertPublished("LocationTypeRegistered")
	})

	t.Run("rejects a duplicate name", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
		_, err := h.registerLocationType.Execute(h.ctx(), placement.PalletRack, placement.Storage, mustCapacity(t, 900, 2))
		assertErrorIs(t, err, usecases.ErrDuplicateLocationType)
	})

	t.Run("rejects an empty name", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.registerLocationType.Execute(h.ctx(), "", placement.Storage, mustCapacity(t, 1200, 2.4))
		assertErrorIs(t, err, placement.ErrEmptyLocationTypeName)
	})

	t.Run("lists every registered type ordered by name", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterLocationType(placement.Shelf, 60, 0.4)
		h.mustRegisterLocationType(placement.Amnesty, 30, 0.2)
		types, err := h.listLocationTypes.Execute(h.ctx())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(types) != 2 || types[0].Name() != placement.Amnesty {
			t.Fatalf("expected types ordered by name, got %v", types)
		}
	})
}

func TestDefinePlacementRule(t *testing.T) {
	t.Run("defines a rule against an existing location type", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
		rule, err := h.definePlacementRule.Execute(h.ctx(), "RULE-HAZ-ONLY-RACK", placement.PalletRack, placement.Allow, mustPredicate(t, "HAZ", "", nil))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rule.ID() != "RULE-HAZ-ONLY-RACK" {
			t.Fatalf("unexpected rule %+v", rule)
		}
		h.assertPublished("PlacementRuleDefined")
	})

	t.Run("rejects a rule referencing an unknown location type", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.definePlacementRule.Execute(h.ctx(), "RULE-1", "Hovercraft", placement.Allow, mustPredicate(t, "HAZ", "", nil))
		assertErrorIs(t, err, usecases.ErrLocationTypeNotFound)
	})

	t.Run("rejects a duplicate rule id", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
		mustDefineRule(t, h, "RULE-1", placement.PalletRack, placement.Allow, mustPredicate(t, "HAZ", "", nil))
		_, err := h.definePlacementRule.Execute(h.ctx(), "RULE-1", placement.PalletRack, placement.Deny, mustPredicate(t, "AMB", "", nil))
		assertErrorIs(t, err, usecases.ErrDuplicatePlacementRule)
	})

	t.Run("rejects an invalid rule", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
		_, err := h.definePlacementRule.Execute(h.ctx(), "", placement.PalletRack, placement.Allow, mustPredicate(t, "HAZ", "", nil))
		assertErrorIs(t, err, placement.ErrEmptyRuleID)
	})

	t.Run("lists every rule ordered by id", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
		mustDefineRule(t, h, "RULE-B", placement.PalletRack, placement.Allow, mustPredicate(t, "HAZ", "", nil))
		mustDefineRule(t, h, "RULE-A", placement.PalletRack, placement.Deny, mustPredicate(t, "AMB", "", nil))
		rules, err := h.listPlacementRules.Execute(h.ctx())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rules) != 2 || rules[0].ID() != "RULE-A" {
			t.Fatalf("expected rules ordered by id, got %v", rules)
		}
	})
}
