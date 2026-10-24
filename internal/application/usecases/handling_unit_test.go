package usecases_test

import (
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/slot"
)

const huCode = "WH1-STOR-AMB-A07-03-02-B"

func TestDeclareLocationHandlingUnit(t *testing.T) {
	t.Run("sets the unit on an existing slot and publishes LocationSlotHandlingUnitDeclared", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		h.mustRegisterSlot(huCode, "PalletRack")

		s, err := h.declareHandlingUnit.Execute(h.ctx(), mustCode(t, huCode), "CASE")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.HandlingUnit() != shared.HandlingUnitCase {
			t.Fatalf("HandlingUnit = %q", s.HandlingUnit())
		}
		h.assertPublished("LocationSlotHandlingUnitDeclared")
		stored, _ := h.slots.FindByCode(h.ctx(), mustCode(t, huCode))
		if stored.HandlingUnit() != shared.HandlingUnitCase {
			t.Fatalf("not persisted: %q", stored.HandlingUnit())
		}
	})

	t.Run("a changed value is announced again", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		h.mustRegisterSlot(huCode, "PalletRack")
		for _, unit := range []string{"EACH", "PALLET"} {
			if _, err := h.declareHandlingUnit.Execute(h.ctx(), mustCode(t, huCode), unit); err != nil {
				t.Fatalf("%s: %v", unit, err)
			}
		}
		count := 0
		for _, n := range h.publishedEventNames() {
			if n == "LocationSlotHandlingUnitDeclared" {
				count++
			}
		}
		if count != 2 {
			t.Fatalf("declared events = %d, want 2", count)
		}
	})

	t.Run("rejects an unknown unit before touching the slot", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		h.mustRegisterSlot(huCode, "PalletRack")
		for _, bad := range []string{"", "case", "TRUCK"} {
			_, err := h.declareHandlingUnit.Execute(h.ctx(), mustCode(t, huCode), bad)
			assertErrorIs(t, err, shared.ErrUnknownHandlingUnit)
		}
		h.assertNotPublished("LocationSlotHandlingUnitDeclared")
	})

	t.Run("rejects an unknown slot", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.declareHandlingUnit.Execute(h.ctx(), mustCode(t, huCode), "CASE")
		assertErrorIs(t, err, usecases.ErrLocationSlotNotFound)
	})

	t.Run("rejects a decommissioned slot", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		h.mustRegisterSlot(huCode, "PalletRack")
		if err := h.decommissionSlot.Execute(h.ctx(), mustCode(t, huCode)); err != nil {
			t.Fatal(err)
		}
		_, err := h.declareHandlingUnit.Execute(h.ctx(), mustCode(t, huCode), "CASE")
		assertErrorIs(t, err, slot.ErrSlotDecommissioned)
		h.assertNotPublished("LocationSlotHandlingUnitDeclared")
	})
}

func TestRegisterLocationSlotWithHandlingUnit(t *testing.T) {
	t.Run("records the unit and carries it on LocationSlotRegistered", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		s, err := h.registerSlot.ExecuteWithHandlingUnit(h.ctx(), mustCode(t, huCode), "PalletRack", shared.Capacity{}, "", nil, "PALLET")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.HandlingUnit() != shared.HandlingUnitPallet {
			t.Fatalf("HandlingUnit = %q", s.HandlingUnit())
		}
		var got shared.LocationSlotRegistered
		for _, e := range h.publisher.Events() {
			if r, ok := e.(shared.LocationSlotRegistered); ok {
				got = r
			}
		}
		if got.HandlingUnit != shared.HandlingUnitPallet {
			t.Fatalf("event handling unit = %q", got.HandlingUnit)
		}
	})

	t.Run("absent means none recorded and the event omits it", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		h.mustRegisterSlot(huCode, "PalletRack")
		stored, _ := h.slots.FindByCode(h.ctx(), mustCode(t, huCode))
		if stored.HandlingUnit() != "" {
			t.Fatalf("HandlingUnit = %q, want none", stored.HandlingUnit())
		}
		for _, e := range h.publisher.Events() {
			if r, ok := e.(shared.LocationSlotRegistered); ok && r.HandlingUnit != "" {
				t.Fatalf("event handling unit = %q, want none", r.HandlingUnit)
			}
		}
	})

	t.Run("an unknown unit refuses the registration and registers nothing", func(t *testing.T) {
		h := newHarness(t)
		h.seedAmbientAisle()
		_, err := h.registerSlot.ExecuteWithHandlingUnit(h.ctx(), mustCode(t, huCode), "PalletRack", shared.Capacity{}, "", nil, "TRUCK")
		if !errors.Is(err, shared.ErrUnknownHandlingUnit) {
			t.Fatalf("err = %v", err)
		}
		if stored, _ := h.slots.FindByCode(h.ctx(), mustCode(t, huCode)); stored != nil {
			t.Fatal("slot must not have been saved")
		}
		h.assertNotPublished("LocationSlotRegistered")
	})
}
