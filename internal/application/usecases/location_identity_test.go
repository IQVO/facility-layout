package usecases_test

import (
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
	"github.com/claudioed/facility-layout/internal/domain/slot"
)

// Known-valid GS1 GLNs (computed with the mod-10 rule, see gln_test.go).
const (
	glnA = "4006381333931"
	glnB = "5412345000013"
	glnC = "4012345000009"
	// glnBadCheck has glnA's body and a wrong check digit.
	glnBadCheck = "4006381333932"

	yardSpot = "WH1-YARD-NORTH-Y01-01-01-A"
	dockDoor = "WH1-YARD-NORTH-Y01-01-01-B"
)

// seedYard registers a yard zone with a Yard and a Dock location type.
func (h *harness) seedYard() {
	h.t.Helper()
	h.mustRegisterSite("WH1", "Fulfilment Centre One")
	h.mustRegisterZone("WH1", "YARD", "NORTH", shared.Ambient, false)
	h.mustRegisterAisle("WH1-YARD-NORTH", "Y01", 1, shared.TwoWay)
	for name, role := range map[string]placement.LocationRole{"YardSpot": placement.Yard, "DockDoor": placement.Dock} {
		if _, err := h.registerLocationType.Execute(h.ctx(), name, role, shared.Capacity{}); err != nil {
			h.t.Fatalf("seeding location type %s: %v", name, err)
		}
	}
}

func (h *harness) registerYardSlot(code, locationType, dockFlow string, opts usecases.SlotOptions) (*slot.LocationSlot, error) {
	h.t.Helper()
	return h.registerSlot.ExecuteWithOptions(h.ctx(), mustCode(h.t, code), locationType, shared.Capacity{}, dockFlow, nil, opts)
}

func TestRegisterSiteWithGLN(t *testing.T) {
	t.Run("records the GLN and publishes it on SiteRegistered", func(t *testing.T) {
		h := newHarness(t)
		s, err := h.registerSite.ExecuteWithGLN(h.ctx(), "WH1", "One", site.RoleUnspecified, glnA)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.GLN() != glnA {
			t.Fatalf("GLN = %q", s.GLN())
		}
		event := h.publisher.Events()[0].(shared.SiteRegistered)
		if event.GLN != glnA {
			t.Fatalf("event GLN = %q", event.GLN)
		}
	})
	t.Run("a site without a GLN stays valid and publishes none", func(t *testing.T) {
		h := newHarness(t)
		s, err := h.registerSite.Execute(h.ctx(), "WH1", "One", site.RoleUnspecified)
		if err != nil || !s.GLN().IsZero() {
			t.Fatalf("err=%v gln=%q", err, s.GLN())
		}
		if event := h.publisher.Events()[0].(shared.SiteRegistered); event.GLN != "" {
			t.Fatalf("event GLN = %q, want none", event.GLN)
		}
	})
	t.Run("rejects an invalid GLN before saving anything", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.registerSite.ExecuteWithGLN(h.ctx(), "WH1", "One", site.RoleUnspecified, glnBadCheck)
		assertErrorIs(t, err, shared.ErrInvalidGLN)
		if found, _ := h.sites.FindByCode(h.ctx(), "WH1"); found != nil {
			t.Fatal("site was saved despite an invalid GLN")
		}
		h.assertNotPublished("SiteRegistered")
	})
	t.Run("a second site cannot take the same GLN", func(t *testing.T) {
		h := newHarness(t)
		if _, err := h.registerSite.ExecuteWithGLN(h.ctx(), "WH1", "One", site.RoleUnspecified, glnA); err != nil {
			t.Fatal(err)
		}
		_, err := h.registerSite.ExecuteWithGLN(h.ctx(), "WH2", "Two", site.RoleUnspecified, glnA)
		assertErrorIs(t, err, usecases.ErrGLNAlreadyAssigned)
	})
	t.Run("a GLN without a registry is a wiring error, not a caller error", func(t *testing.T) {
		h := newHarness(t)
		uc := &usecases.RegisterSite{Sites: h.sites, Events: h.publisher, Clock: h.clock}
		_, err := uc.ExecuteWithGLN(h.ctx(), "WH1", "One", site.RoleUnspecified, glnA)
		if err == nil || errors.Is(err, usecases.ErrGLNAlreadyAssigned) || errors.Is(err, shared.ErrInvalidGLN) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRegisterZoneWithGLN(t *testing.T) {
	t.Run("records the GLN and publishes it on ZoneRegistered", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "One")
		z, err := h.registerZone.ExecuteWithGLN(h.ctx(), "WH1", "YARD", "NORTH", shared.Ambient, false, nil, nil, glnB)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if z.GLN() != glnB {
			t.Fatalf("GLN = %q", z.GLN())
		}
		var got shared.GLN
		for _, e := range h.publisher.Events() {
			if zr, ok := e.(shared.ZoneRegistered); ok {
				got = zr.GLN
			}
		}
		if got != glnB {
			t.Fatalf("event GLN = %q", got)
		}
	})
	t.Run("rejects an invalid GLN", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "One")
		_, err := h.registerZone.ExecuteWithGLN(h.ctx(), "WH1", "YARD", "NORTH", shared.Ambient, false, nil, nil, "123")
		assertErrorIs(t, err, shared.ErrInvalidGLN)
	})
	t.Run("a zone cannot reuse the GLN of a site", func(t *testing.T) {
		h := newHarness(t)
		if _, err := h.registerSite.ExecuteWithGLN(h.ctx(), "WH1", "One", site.RoleUnspecified, glnA); err != nil {
			t.Fatal(err)
		}
		_, err := h.registerZone.ExecuteWithGLN(h.ctx(), "WH1", "YARD", "NORTH", shared.Ambient, false, nil, nil, glnA)
		assertErrorIs(t, err, usecases.ErrGLNAlreadyAssigned)
		if found, _ := h.zones.FindByID(h.ctx(), "WH1-YARD-NORTH"); found != nil {
			t.Fatal("zone was saved despite the GLN conflict")
		}
	})
}

func TestRegisterSlotWithGLNAndSpotKind(t *testing.T) {
	t.Run("a yard spot keeps its GLN and spot kind and publishes both", func(t *testing.T) {
		h := newHarness(t)
		h.seedYard()
		s, err := h.registerYardSlot(yardSpot, "YardSpot", "", usecases.SlotOptions{GLN: glnC, SpotKind: "REEFER_PLUG"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.GLN() != glnC || s.SpotKind() != shared.SpotKindReeferPlug {
			t.Fatalf("slot = %q / %q", s.GLN(), s.SpotKind())
		}
		var event shared.LocationSlotRegistered
		for _, e := range h.publisher.Events() {
			if r, ok := e.(shared.LocationSlotRegistered); ok {
				event = r
			}
		}
		if event.GLN != glnC || event.SpotKind != shared.SpotKindReeferPlug {
			t.Fatalf("event = %q / %q", event.GLN, event.SpotKind)
		}
	})
	t.Run("every spot kind is accepted on a yard slot", func(t *testing.T) {
		h := newHarness(t)
		h.seedYard()
		for i, kind := range []string{"PARKING", "HOLDING", "REEFER_PLUG", "HAZMAT", "STAGING_LANE"} {
			code := "WH1-YARD-NORTH-Y01-01-0" + string(rune('1'+i)) + "-A"
			if _, err := h.registerYardSlot(code, "YardSpot", "", usecases.SlotOptions{SpotKind: kind}); err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
		}
	})
}

func TestRegisterSlotSpotKindRefusals(t *testing.T) {
	t.Run("a dock door may carry a GLN but never a spot kind", func(t *testing.T) {
		h := newHarness(t)
		h.seedYard()
		s, err := h.registerYardSlot(dockDoor, "DockDoor", "Inbound", usecases.SlotOptions{GLN: glnA})
		if err != nil || s.GLN() != glnA || s.SpotKind() != "" {
			t.Fatalf("err=%v slot=%q/%q", err, s.GLN(), s.SpotKind())
		}
		_, err = h.registerYardSlot("WH1-YARD-NORTH-Y01-01-02-B", "DockDoor", "Inbound", usecases.SlotOptions{SpotKind: "PARKING"})
		assertErrorIs(t, err, slot.ErrSpotKindNotAllowed)
	})
	t.Run("a storage bin refuses a spot kind", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		_, err := h.registerSlot.ExecuteWithOptions(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"), "PalletRack", shared.Capacity{}, "", nil, usecases.SlotOptions{SpotKind: "HOLDING"})
		assertErrorIs(t, err, slot.ErrSpotKindNotAllowed)
		if got := h.metrics.outcomes[len(h.metrics.outcomes)-1]; got != usecases.OutcomeRejected {
			t.Fatalf("metric outcome = %q", got)
		}
		h.assertNotPublished("LocationSlotRegistered")
	})
	t.Run("rejects an unknown spot kind and an invalid GLN", func(t *testing.T) {
		h := newHarness(t)
		h.seedYard()
		_, err := h.registerYardSlot(yardSpot, "YardSpot", "", usecases.SlotOptions{SpotKind: "parking"})
		assertErrorIs(t, err, shared.ErrUnknownSpotKind)
		_, err = h.registerYardSlot(yardSpot, "YardSpot", "", usecases.SlotOptions{GLN: glnBadCheck})
		assertErrorIs(t, err, shared.ErrInvalidGLN)
		_, err = h.registerYardSlot(yardSpot, "YardSpot", "", usecases.SlotOptions{HandlingUnit: "TRUCK"})
		assertErrorIs(t, err, shared.ErrUnknownHandlingUnit)
	})
}

func TestRegisterSlotGLNUniqueness(t *testing.T) {
	t.Run("a slot cannot reuse a GLN held by a zone or another slot", func(t *testing.T) {
		h := newHarness(t)
		h.mustRegisterSite("WH1", "One")
		if _, err := h.registerZone.ExecuteWithGLN(h.ctx(), "WH1", "YARD", "NORTH", shared.Ambient, false, nil, nil, glnB); err != nil {
			t.Fatal(err)
		}
		h.mustRegisterAisle("WH1-YARD-NORTH", "Y01", 1, shared.TwoWay)
		if _, err := h.registerLocationType.Execute(h.ctx(), "YardSpot", placement.Yard, shared.Capacity{}); err != nil {
			t.Fatal(err)
		}
		_, err := h.registerYardSlot(yardSpot, "YardSpot", "", usecases.SlotOptions{GLN: glnB})
		assertErrorIs(t, err, usecases.ErrGLNAlreadyAssigned)
		if _, err := h.registerYardSlot(yardSpot, "YardSpot", "", usecases.SlotOptions{GLN: glnC}); err != nil {
			t.Fatal(err)
		}
		_, err = h.registerYardSlot("WH1-YARD-NORTH-Y01-01-02-A", "YardSpot", "", usecases.SlotOptions{GLN: glnC})
		assertErrorIs(t, err, usecases.ErrGLNAlreadyAssigned)
	})
	t.Run("the old call shapes keep working without the new fields", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		h.mustRegisterSlot("WH1-STOR-AMB-A07-03-02-B", "PalletRack")
		s, _ := h.slots.FindByCode(h.ctx(), mustCode(t, "WH1-STOR-AMB-A07-03-02-B"))
		if !s.GLN().IsZero() || s.SpotKind() != "" {
			t.Fatalf("unexpected identity %q / %q", s.GLN(), s.SpotKind())
		}
	})
}
