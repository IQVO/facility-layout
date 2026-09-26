package usecases_test

import (
	"strings"
	"testing"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/slot"
)

func TestRegisterLocationSlot(t *testing.T) {
	t.Run("registers a slot whose whole chain of custody resolves", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()

		s, err := h.registerSlot.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"), placement.PalletRack, shared.Capacity{}, "", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.Code().String() != "WH1-STOR-AMB-A07-03-02-B" || !s.IsActive() {
			t.Fatalf("unexpected slot %+v", s)
		}
		if s.Capacity().MaxWeightKg() != 1200 {
			t.Fatalf("expected the location type's default envelope, got %v", s.Capacity())
		}
		h.assertPublished("LocationSlotRegistered")
	})

	t.Run("honours a capacity override", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		s, err := h.registerSlot.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"), placement.PalletRack, mustCapacity(t, 400, 0.9), "", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.Capacity().MaxWeightKg() != 400 || s.Capacity().MaxVolumeM3() != 0.9 {
			t.Fatalf("expected the override envelope, got %v", s.Capacity())
		}
	})

	t.Run("rejects a duplicate location code", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		h.mustRegisterSlot("WH1-STOR-AMB-A07-03-02-B", placement.PalletRack)
		_, err := h.registerSlot.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"), placement.PalletRack, shared.Capacity{}, "", nil)
		assertErrorIs(t, err, usecases.ErrDuplicateLocationCode)
	})

	t.Run("rejects re-registering a decommissioned code", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		h.mustRegisterSlot("WH1-STOR-AMB-A07-03-02-B", placement.PalletRack)
		if err := h.decommissionSlot.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, err := h.registerSlot.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"), placement.PalletRack, shared.Capacity{}, "", nil)
		assertErrorIs(t, err, usecases.ErrDuplicateLocationCode)
	})

	t.Run("rejects an unknown location type", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		_, err := h.registerSlot.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"), "Hovercraft", shared.Capacity{}, "", nil)
		assertErrorIs(t, err, usecases.ErrLocationTypeNotFound)
	})
}

func TestRegisterLocationSlotChainOfCustody(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, h *harness)
		code    string
		wantErr error
	}{
		{
			name:    "unknown site",
			setup:   func(_ *testing.T, h *harness) { h.seedAmbientAisle() },
			code:    "WH9-STOR-AMB-A07-03-02-B",
			wantErr: usecases.ErrSiteNotFound,
		},
		{
			name: "decommissioned site",
			setup: func(t *testing.T, h *harness) {
				h.seedAmbientAisle()
				decommissionSite(t, h, "WH1")
			},
			code:    "WH1-STOR-AMB-A07-03-02-B",
			wantErr: usecases.ErrSiteNotActive,
		},
		{
			name:    "unknown zone",
			setup:   func(_ *testing.T, h *harness) { h.seedAmbientAisle() },
			code:    "WH1-STOR-FRZ-A07-03-02-B",
			wantErr: usecases.ErrZoneNotFound,
		},
		{
			name: "decommissioned zone",
			setup: func(t *testing.T, h *harness) {
				h.seedAmbientAisle()
				decommissionZone(t, h, "WH1-STOR-AMB")
			},
			code:    "WH1-STOR-AMB-A07-03-02-B",
			wantErr: usecases.ErrZoneNotActive,
		},
		{
			name:    "unknown aisle",
			setup:   func(_ *testing.T, h *harness) { h.seedAmbientAisle() },
			code:    "WH1-STOR-AMB-A99-03-02-B",
			wantErr: usecases.ErrAisleNotFound,
		},
		{
			name: "decommissioned aisle",
			setup: func(t *testing.T, h *harness) {
				h.seedAmbientAisle()
				decommissionAisle(t, h, "WH1-STOR-AMB-A07")
			},
			code:    "WH1-STOR-AMB-A07-03-02-B",
			wantErr: usecases.ErrAisleNotActive,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.setup(t, h)

			_, err := h.registerSlot.Execute(h.ctx(), mustCode(t, tc.code), placement.PalletRack, shared.Capacity{}, "", nil)
			assertErrorIs(t, err, tc.wantErr)
			h.assertNotPublished("LocationSlotRegistered")
		})
	}
}

func TestRegisterLocationSlotEnforcesPlacementRules(t *testing.T) {
	newHazmatHarness := func(t *testing.T) *harness {
		t.Helper()
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")
		h.mustRegisterZone("WH1", "STOR", "HAZ", shared.Ambient, true)
		h.mustRegisterAisle("WH1-STOR-HAZ", "A01", 1, shared.OneWay)
		h.mustRegisterZone("WH1", "STOR", "FRZ", shared.Frozen, false)
		h.mustRegisterAisle("WH1-STOR-FRZ", "A02", 2, shared.TwoWay)
		h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
		h.mustRegisterLocationType(placement.Shelf, 60, 0.4)
		mustDefineRule(t, h, "RULE-HAZ-ONLY-RACK", placement.PalletRack, placement.Allow, mustPredicate(t, "HAZ", "", nil))
		mustDefineRule(t, h, "RULE-FRZ-NO-SHELF", placement.Shelf, placement.Deny, mustPredicate(t, "", shared.Frozen, nil))
		return h
	}

	t.Run("a placement satisfying every rule is accepted", func(t *testing.T) {
		h := newHazmatHarness(t)
		if _, err := h.registerSlot.Execute(h.ctx(), mustCode(t, "WH1-STOR-HAZ-A01-01-01-A"), placement.PalletRack, shared.Capacity{}, "", nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("a type outside the zone's allow-list is rejected naming the rule", func(t *testing.T) {
		h := newHazmatHarness(t)
		_, err := h.registerSlot.Execute(h.ctx(), mustCode(t, "WH1-STOR-HAZ-A01-01-01-A"), placement.Shelf, shared.Capacity{}, "", nil)
		assertErrorIs(t, err, placement.ErrPlacementRuleViolated)
		if !strings.Contains(err.Error(), "RULE-HAZ-ONLY-RACK") {
			t.Fatalf("expected the violated rule to be named, got %q", err.Error())
		}
		h.assertNotPublished("LocationSlotRegistered")
	})

	t.Run("a denied type in a frozen zone is rejected naming the rule", func(t *testing.T) {
		h := newHazmatHarness(t)
		_, err := h.registerSlot.Execute(h.ctx(), mustCode(t, "WH1-STOR-FRZ-A02-01-01-A"), placement.Shelf, shared.Capacity{}, "", nil)
		assertErrorIs(t, err, placement.ErrPlacementRuleViolated)
		if !strings.Contains(err.Error(), "RULE-FRZ-NO-SHELF") {
			t.Fatalf("expected the violated rule to be named, got %q", err.Error())
		}
	})
}

// seedFunctionalRoles registers a Dock-role and a WorkCenter-role location
// type over the canonical ambient aisle, the setup every ADR-0016
// functional-attributes case needs.
func seedFunctionalRoles(t *testing.T, h *harness) {
	t.Helper()
	h.seedAmbientAisle()
	if _, err := h.registerLocationType.Execute(h.ctx(), "DockDoor", placement.Dock, shared.Capacity{}); err != nil {
		t.Fatalf("seeding DockDoor: %v", err)
	}
	if _, err := h.registerLocationType.Execute(h.ctx(), "PackBenches", placement.WorkCenter, shared.Capacity{}); err != nil {
		t.Fatalf("seeding PackBenches: %v", err)
	}
}

func TestRegisterLocationSlotFunctionalAttributes(t *testing.T) {
	tests := []struct {
		name        string
		locationTyp string
		dockFlow    string
		activities  []string
		wantErr     error
		verify      func(t *testing.T, s *slot.LocationSlot)
	}{
		{
			name:        "a dock slot with a valid flow carries it",
			locationTyp: "DockDoor",
			dockFlow:    "Inbound",
			verify: func(t *testing.T, s *slot.LocationSlot) {
				t.Helper()
				if s.Functional().DockFlow() != slot.Inbound {
					t.Fatalf("expected dockFlow Inbound, got %q", s.Functional().DockFlow())
				}
			},
		},
		{
			name:        "a dock slot with an unknown flow is rejected",
			locationTyp: "DockDoor",
			dockFlow:    "Sideways",
			wantErr:     slot.ErrUnknownDockFlow,
		},
		{
			name:        "a dock slot without a flow is rejected",
			locationTyp: "DockDoor",
			wantErr:     slot.ErrDockFlowRequired,
		},
		{
			name:        "a dock slot with activities is rejected",
			locationTyp: "DockDoor",
			dockFlow:    "Both",
			activities:  []string{"Pack"},
			wantErr:     slot.ErrFunctionalAttributesNotAllowed,
		},
		{
			name:        "a work center slot with valid activities carries them sorted",
			locationTyp: "PackBenches",
			activities:  []string{"VAS", "Pack"},
			verify: func(t *testing.T, s *slot.LocationSlot) {
				t.Helper()
				got := s.Functional().Activities()
				if len(got) != 2 || got[0] != slot.Pack || got[1] != slot.VAS {
					t.Fatalf("expected sorted [Pack VAS], got %v", got)
				}
			},
		},
		{
			name:        "a work center slot with an unknown activity is rejected",
			locationTyp: "PackBenches",
			activities:  []string{"Pack", "Juggle"},
			wantErr:     slot.ErrUnknownActivity,
		},
		{
			name:        "a work center slot without activities is rejected",
			locationTyp: "PackBenches",
			wantErr:     slot.ErrWorkCenterActivitiesRequired,
		},
		{
			name:        "a work center slot with a dock flow is rejected",
			locationTyp: "PackBenches",
			dockFlow:    "Inbound",
			activities:  []string{"Pack"},
			wantErr:     slot.ErrFunctionalAttributesNotAllowed,
		},
		{
			name:        "a storage slot with activities is rejected",
			locationTyp: placement.PalletRack,
			activities:  []string{"Pack"},
			wantErr:     slot.ErrFunctionalAttributesNotAllowed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			seedFunctionalRoles(t, h)
			h.metrics.outcomes = nil

			s, err := h.registerSlot.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"), tc.locationTyp, shared.Capacity{}, tc.dockFlow, tc.activities)
			if tc.wantErr != nil {
				assertErrorIs(t, err, tc.wantErr)
				h.assertNotPublished("LocationSlotRegistered")
				if len(h.metrics.outcomes) != 1 || h.metrics.outcomes[0] != usecases.OutcomeRejected {
					t.Fatalf("expected one rejected outcome, got %v", h.metrics.outcomes)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			h.assertPublished("LocationSlotRegistered")
			if len(h.metrics.outcomes) != 1 || h.metrics.outcomes[0] != usecases.OutcomeAccepted {
				t.Fatalf("expected one accepted outcome, got %v", h.metrics.outcomes)
			}
			tc.verify(t, s)
		})
	}
}

func TestGetLocationSlot(t *testing.T) {
	h := newHarness(t)
	h.seedAmbientAisle()
	h.mustRegisterSlot("WH1-STOR-AMB-A07-03-02-B", placement.PalletRack)

	got, err := h.getSlot.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.LocationType() != placement.PalletRack {
		t.Fatalf("unexpected slot %+v", got)
	}

	_, err = h.getSlot.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-99-99-Z"))
	assertErrorIs(t, err, usecases.ErrLocationSlotNotFound)
}

func TestGetLocationClassification(t *testing.T) {
	t.Run("resolves the slot's zone attributes", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "Fulfilment Centre One")
		h.mustRegisterZone("WH1", "STOR", "HAZ", shared.Frozen, true)
		h.mustRegisterAisle("WH1-STOR-HAZ", "A01", 1, shared.TwoWay)
		h.mustRegisterLocationType(placement.PalletRack, 1200, 2.4)
		h.mustRegisterSlot("WH1-STOR-HAZ-A01-01-01-A", placement.PalletRack)

		z, err := h.getLocationClassification.Execute(h.ctx(), mustCode(t, "WH1-STOR-HAZ-A01-01-01-A"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !z.Hazmat() || z.TemperatureClass() != shared.Frozen {
			t.Fatalf("unexpected zone attributes: hazmat=%v temperatureClass=%v", z.Hazmat(), z.TemperatureClass())
		}
	})

	t.Run("rejects an unknown location code with ErrLocationSlotNotFound", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()

		_, err := h.getLocationClassification.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-99-99-Z"))
		assertErrorIs(t, err, usecases.ErrLocationSlotNotFound)
	})

	t.Run("publishes nothing: it is a pure read model", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		h.mustRegisterSlot("WH1-STOR-AMB-A07-03-02-B", placement.PalletRack)

		before := len(h.publishedEventNames())
		if _, err := h.getLocationClassification.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if after := len(h.publishedEventNames()); after != before {
			t.Fatalf("a read model must publish nothing: %d -> %d events", before, after)
		}
	})
}

func TestDecommissionLocationSlot(t *testing.T) {
	t.Run("decommissions an active slot", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		h.mustRegisterSlot("WH1-STOR-AMB-A07-03-02-B", placement.PalletRack)

		if err := h.decommissionSlot.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		h.assertPublished("LocationSlotDecommissioned")

		stored, err := h.slots.FindByCode(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if stored.IsActive() {
			t.Fatal("expected the persisted slot to be Decommissioned")
		}
	})

	t.Run("rejects an unknown slot", func(t *testing.T) {
		h := newHarness(t)
		err := h.decommissionSlot.Execute(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"))
		assertErrorIs(t, err, usecases.ErrLocationSlotNotFound)
	})

	t.Run("decommission is one-way", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		h.mustRegisterSlot("WH1-STOR-AMB-A07-03-02-B", placement.PalletRack)
		code := mustCode(t, "WH1-STOR-AMB-A07-03-02-B")
		if err := h.decommissionSlot.Execute(h.ctx(), code); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		err := h.decommissionSlot.Execute(h.ctx(), code)
		assertErrorIs(t, err, slot.ErrAlreadyDecommissioned)
	})
}
