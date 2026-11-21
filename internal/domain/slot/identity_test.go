package slot_test

import (
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/slot"
)

func newSlotWithRole(t *testing.T, role placement.LocationRole, functional slot.FunctionalAttributes) *slot.LocationSlot {
	t.Helper()
	code := mustCode(t, "WH1-YARD-NORTH-Y01-01-01-A")
	attrs := placement.ZoneAttributes{ZoneID: code.ZoneID(), ZoneCode: "NORTH", TemperatureClass: shared.Ambient}
	s, err := slot.NewLocationSlot(code, mustLocationTypeWithRole(t, "Type"+string(role), role, 0, 0), shared.Capacity{}, functional, attrs, nil)
	if err != nil {
		t.Fatalf("NewLocationSlot(%s): %v", role, err)
	}
	return s
}

func TestNewSlotHasNoIdentity(t *testing.T) {
	s := newActiveSlot(t)
	if !s.GLN().IsZero() || s.SpotKind() != "" {
		t.Fatalf("a new slot must have no GLN and no spot kind, got %q / %q", s.GLN(), s.SpotKind())
	}
}

func TestAssignGLN(t *testing.T) {
	tests := []struct {
		name    string
		gln     shared.GLN
		wantErr error
	}{
		{"valid", "4006381333931", nil},
		{"wrong check digit", "4006381333932", shared.ErrInvalidGLN},
		{"too short", "400638", shared.ErrInvalidGLN},
		{"empty", "", shared.ErrInvalidGLN},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newActiveSlot(t)
			err := s.AssignGLN(tc.gln)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			want := shared.GLN("")
			if tc.wantErr == nil {
				want = tc.gln
			}
			if s.GLN() != want {
				t.Fatalf("GLN = %q, want %q", s.GLN(), want)
			}
		})
	}
}

func TestAssignSpotKindOnlyOnYardSlots(t *testing.T) {
	dock, err := slot.NewFunctionalAttributes(placement.Dock, slot.Inbound, nil)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		slot    *slot.LocationSlot
		kind    shared.SpotKind
		wantErr error
	}{
		{"yard parking", newSlotWithRole(t, placement.Yard, slot.FunctionalAttributes{}), shared.SpotKindParking, nil},
		{"yard hazmat", newSlotWithRole(t, placement.Yard, slot.FunctionalAttributes{}), shared.SpotKindHazmat, nil},
		{"yard unknown kind", newSlotWithRole(t, placement.Yard, slot.FunctionalAttributes{}), "DOCK", shared.ErrUnknownSpotKind},
		{"yard empty kind", newSlotWithRole(t, placement.Yard, slot.FunctionalAttributes{}), "", shared.ErrUnknownSpotKind},
		{"dock door", newSlotWithRole(t, placement.Dock, dock), shared.SpotKindParking, slot.ErrSpotKindNotAllowed},
		{"storage bin", newActiveSlot(t), shared.SpotKindHolding, slot.ErrSpotKindNotAllowed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.slot.AssignSpotKind(tc.kind)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			want := shared.SpotKind("")
			if tc.wantErr == nil {
				want = tc.kind
			}
			if tc.slot.SpotKind() != want {
				t.Fatalf("SpotKind = %q, want %q", tc.slot.SpotKind(), want)
			}
		})
	}
}

func TestIdentityIsFrozenOnADecommissionedSlot(t *testing.T) {
	s := newSlotWithRole(t, placement.Yard, slot.FunctionalAttributes{})
	if err := s.Decommission(); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignGLN("4006381333931"); !errors.Is(err, slot.ErrSlotDecommissioned) {
		t.Fatalf("AssignGLN err = %v", err)
	}
	if err := s.AssignSpotKind(shared.SpotKindParking); !errors.Is(err, slot.ErrSlotDecommissioned) {
		t.Fatalf("AssignSpotKind err = %v", err)
	}
	// The decommission check wins over a value error: a retired slot never
	// reports a shape problem for a request it could not honour anyway.
	if err := s.AssignSpotKind("DOCK"); !errors.Is(err, slot.ErrSlotDecommissioned) {
		t.Fatalf("AssignSpotKind(bad) err = %v", err)
	}
}

func TestWithPersistedIdentityRestoresWithoutValidating(t *testing.T) {
	s := newActiveSlot(t).WithPersistedIdentity("4006381333931", shared.SpotKindHazmat)
	if s.GLN() != "4006381333931" || s.SpotKind() != shared.SpotKindHazmat {
		t.Fatalf("restored %q / %q", s.GLN(), s.SpotKind())
	}
}
