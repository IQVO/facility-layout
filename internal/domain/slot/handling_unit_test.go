package slot_test

import (
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/slot"
)

func newActiveSlot(t *testing.T) *slot.LocationSlot {
	t.Helper()
	code := mustCode(t, "WH1-STOR-AMB-A07-03-02-B")
	attrs := placement.ZoneAttributes{ZoneID: code.ZoneID(), ZoneCode: "AMB", TemperatureClass: shared.Ambient}
	s, err := slot.NewLocationSlot(code, mustLocationType(t, placement.PalletRack, 1000, 2), shared.Capacity{}, slot.FunctionalAttributes{}, attrs, nil)
	if err != nil {
		t.Fatalf("NewLocationSlot: %v", err)
	}
	return s
}

func TestNewLocationSlotHasNoHandlingUnit(t *testing.T) {
	if got := newActiveSlot(t).HandlingUnit(); got != "" {
		t.Fatalf("a new slot must have no handling unit recorded, got %q", got)
	}
}

func TestDeclareHandlingUnit(t *testing.T) {
	tests := []struct {
		name    string
		unit    shared.HandlingUnit
		wantErr error
	}{
		{"EACH", shared.HandlingUnitEach, nil},
		{"CASE", shared.HandlingUnitCase, nil},
		{"PALLET", shared.HandlingUnitPallet, nil},
		{"empty is not a unit", "", shared.ErrUnknownHandlingUnit},
		{"lowercase is not a unit", "case", shared.ErrUnknownHandlingUnit},
		{"unknown", "TRUCK", shared.ErrUnknownHandlingUnit},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newActiveSlot(t)
			err := s.DeclareHandlingUnit(tc.unit)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			want := tc.unit
			if tc.wantErr != nil {
				want = ""
			}
			if s.HandlingUnit() != want {
				t.Fatalf("HandlingUnit = %q, want %q", s.HandlingUnit(), want)
			}
		})
	}
}

func TestDeclareHandlingUnitCanChangeAnExistingValue(t *testing.T) {
	s := newActiveSlot(t)
	if err := s.DeclareHandlingUnit(shared.HandlingUnitEach); err != nil {
		t.Fatal(err)
	}
	if err := s.DeclareHandlingUnit(shared.HandlingUnitCase); err != nil {
		t.Fatal(err)
	}
	if s.HandlingUnit() != shared.HandlingUnitCase {
		t.Fatalf("got %q", s.HandlingUnit())
	}
}

func TestDeclareHandlingUnitRejectedOnDecommissionedSlot(t *testing.T) {
	s := newActiveSlot(t)
	if err := s.DeclareHandlingUnit(shared.HandlingUnitCase); err != nil {
		t.Fatal(err)
	}
	if err := s.Decommission(); err != nil {
		t.Fatal(err)
	}
	if err := s.DeclareHandlingUnit(shared.HandlingUnitPallet); !errors.Is(err, slot.ErrSlotDecommissioned) {
		t.Fatalf("err = %v, want ErrSlotDecommissioned", err)
	}
	// An invalid value on a retired slot reports the retirement first.
	if err := s.DeclareHandlingUnit("nope"); !errors.Is(err, slot.ErrSlotDecommissioned) {
		t.Fatalf("err = %v, want ErrSlotDecommissioned", err)
	}
	if s.HandlingUnit() != shared.HandlingUnitCase {
		t.Fatalf("a refused declaration must leave the value, got %q", s.HandlingUnit())
	}
}

func TestWithPersistedHandlingUnitRestoresWithoutValidation(t *testing.T) {
	code := mustCode(t, "WH1-RCV-AMB-D01-01-01-A")
	s := slot.RehydrateLocationSlot(code, placement.Staging, placement.Storage, slot.FunctionalAttributes{}, mustCapacity(t, 900, 3), shared.Active, shared.Point3D{}, shared.Dimensions{}, nil, 1)
	if s.HandlingUnit() != "" {
		t.Fatalf("rehydrated slot without a stored unit must report none, got %q", s.HandlingUnit())
	}
	if got := s.WithPersistedHandlingUnit(shared.HandlingUnitPallet); got != s || s.HandlingUnit() != shared.HandlingUnitPallet {
		t.Fatalf("WithPersistedHandlingUnit must set and return the same slot, got %q", s.HandlingUnit())
	}
}
