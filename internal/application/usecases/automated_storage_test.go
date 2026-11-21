package usecases_test

import (
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/slot"
)

const (
	asrsStore  = "WH1-AUTO-ASRS-ASRS1-00-00-S"
	asrsIn     = "WH1-AUTO-ASRS-ASRS1-01-00-IN1"
	asrs2Store = "WH1-AUTO-ASRS-ASRS2-00-00-S"
)

// seedASRS registers site WH1, zone WH1-AUTO-ASRS with aisles ASRS1, ASRS2
// and ST01, and the WCS location types (ADR-0048).
func (h *harness) seedASRS() {
	h.t.Helper()
	h.mustRegisterSite("WH1", "Fulfilment Centre One")
	h.mustRegisterZone("WH1", "AUTO", "ASRS", shared.Ambient, false)
	for i, a := range []string{"ASRS1", "ASRS2", "ST01"} {
		h.mustRegisterAisle("WH1-AUTO-ASRS", a, i+1, shared.TwoWay)
	}
	h.mustRegisterTypedLocation(placement.AutomatedStorage, placement.Storage, mustCapacity(h.t, 50000, 400))
	h.mustRegisterTypedLocation(placement.AsrsPort, placement.Drop, mustCapacity(h.t, 30, 0.1))
	h.mustRegisterTypedLocation("GtpStation", placement.WorkCenter, shared.Capacity{})
}

func (h *harness) mustRegisterTypedLocation(name string, role placement.LocationRole, c shared.Capacity) {
	h.t.Helper()
	if _, err := h.registerLocationType.Execute(h.ctx(), name, role, c); err != nil {
		h.t.Fatalf("seeding location type %q: %v", name, err)
	}
}

func (h *harness) registerAt(raw, locationType string, activities ...string) error {
	_, err := h.registerSlot.Execute(h.ctx(), mustCode(h.t, raw), locationType, shared.Capacity{}, "", activities)
	return err
}

func TestAnASRSIsOneAutomatedStorageSlotPerAisle(t *testing.T) {
	h := newHarness(t)
	h.seedASRS()
	if err := h.registerAt(asrsStore, placement.AutomatedStorage); err != nil {
		t.Fatal(err)
	}
	h.assertPublished("LocationSlotRegistered")
	stored, _ := h.slots.FindByCode(h.ctx(), mustCode(t, asrsStore))
	if stored.Role() != placement.Storage || stored.LocationType() != placement.AutomatedStorage {
		t.Fatalf("stored = %s %s", stored.LocationType(), stored.Role())
	}
	if err := h.registerAt("WH1-AUTO-ASRS-ASRS1-00-00-T", placement.AutomatedStorage); !errors.Is(err, usecases.ErrAutomatedStorageAlreadyInAisle) {
		t.Fatalf("second in the aisle: err = %v", err)
	}
	if err := h.registerAt(asrs2Store, placement.AutomatedStorage); err != nil {
		t.Fatalf("another aisle is another AS/RS: %v", err)
	}
}

func TestADecommissionedASRSFreesItsAisle(t *testing.T) {
	h := newHarness(t)
	h.seedASRS()
	_ = h.registerAt(asrsStore, placement.AutomatedStorage)
	if err := h.decommissionSlot.Execute(h.ctx(), mustCode(t, asrsStore)); err != nil {
		t.Fatal(err)
	}
	if err := h.registerAt("WH1-AUTO-ASRS-ASRS1-00-00-T", placement.AutomatedStorage); err != nil {
		t.Fatalf("a decommissioned AS/RS must not block its aisle: %v", err)
	}
}

func TestAPortNeedsAnASRSInItsAisle(t *testing.T) {
	h := newHarness(t)
	h.seedASRS()
	if err := h.registerAt(asrsIn, placement.AsrsPort); !errors.Is(err, usecases.ErrAsrsPortWithoutAutomatedStorage) {
		t.Fatalf("port before the AS/RS: err = %v", err)
	}
	_ = h.registerAt(asrsStore, placement.AutomatedStorage)
	if err := h.registerAt(asrsIn, placement.AsrsPort); err != nil {
		t.Fatalf("port beside its AS/RS: %v", err)
	}
	if err := h.registerAt("WH1-AUTO-ASRS-ASRS2-01-00-IN1", placement.AsrsPort); !errors.Is(err, usecases.ErrAsrsPortWithoutAutomatedStorage) {
		t.Fatalf("port in an aisle with no AS/RS: err = %v", err)
	}
}

func TestAPortAfterItsASRSWasDecommissionedIsRefused(t *testing.T) {
	h := newHarness(t)
	h.seedASRS()
	_ = h.registerAt(asrsStore, placement.AutomatedStorage)
	_ = h.decommissionSlot.Execute(h.ctx(), mustCode(t, asrsStore))
	if err := h.registerAt(asrsIn, placement.AsrsPort); !errors.Is(err, usecases.ErrAsrsPortWithoutAutomatedStorage) {
		t.Fatalf("err = %v", err)
	}
}

func TestAGoodsToPersonStationIsAWorkCenterThatPicks(t *testing.T) {
	h := newHarness(t)
	h.seedASRS()
	if err := h.registerAt("WH1-AUTO-ASRS-ST01-01-00-GTP1", "GtpStation", "Pick"); err != nil {
		t.Fatal(err)
	}
	stored, _ := h.slots.FindByCode(h.ctx(), mustCode(t, "WH1-AUTO-ASRS-ST01-01-00-GTP1"))
	if got := stored.Functional().Activities(); len(got) != 1 || got[0] != slot.Pick {
		t.Fatalf("activities = %v", got)
	}
}

func TestAWellKnownTypeWithTheWrongRoleIsRefused(t *testing.T) {
	h := newHarness(t)
	if _, err := h.registerLocationType.Execute(h.ctx(), placement.AutomatedStorage, placement.Dock, shared.Capacity{}); !errors.Is(err, placement.ErrLocationTypeRoleMismatch) {
		t.Fatalf("err = %v", err)
	}
}

func TestTheRuleAppliesToALayoutImport(t *testing.T) {
	h := newHarness(t)
	h.seedASRS()
	_ = h.registerAt(asrsStore, placement.AutomatedStorage)
	h.importRowsExpectingRefusal(t, "WH1-AUTO-ASRS-ASRS1-00-00-T", placement.AutomatedStorage, "automated-storage-already-in-aisle")
}

// importRowsExpectingRefusal imports one row (zone WH1-AUTO-ASRS, the code
// raw) and asserts the import reports it refused with the slug's sentinel.
func (h *harness) importRowsExpectingRefusal(t *testing.T, raw, locationType, slug string) {
	t.Helper()
	want := map[string]error{
		"automated-storage-already-in-aisle":  usecases.ErrAutomatedStorageAlreadyInAisle,
		"asrs-port-without-automated-storage": usecases.ErrAsrsPortWithoutAutomatedStorage,
	}[slug]
	code := mustCode(t, raw)
	r := row("AUTO", "ASRS", shared.Ambient, false, code.Aisle(), 1, code.Bay(), code.Level(), code.Position(), locationType)
	report, err := h.importLayout.Execute(h.ctx(), []usecases.ImportRow{r})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if want == nil || report.RowsRejected != 1 || report.Results[0].Error != want.Error() {
		t.Fatalf("want the row refused %s, got %+v", slug, report.Results)
	}
}
