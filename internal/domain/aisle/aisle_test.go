package aisle_test

import (
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// newAisleCase is one row of the NewAisle table: either a valid aisle
// (wantID set) or an invalid one (wantErr set).
type newAisleCase struct {
	name         string
	zoneID       string
	aisleCode    string
	sequenceHint int
	direction    shared.Direction
	wantID       string
	wantErr      error
}

func TestNewAisle(t *testing.T) {
	tests := []newAisleCase{
		{name: "a two-way storage aisle", zoneID: "WH1-STOR-AMB", aisleCode: "A07", sequenceHint: 7, direction: shared.TwoWay, wantID: "WH1-STOR-AMB-A07"},
		{name: "a one-way aisle first on the walk path", zoneID: "WH1-STOR-AMB", aisleCode: "A01", sequenceHint: 0, direction: shared.OneWay, wantID: "WH1-STOR-AMB-A01"},
		{name: "missing zone scope", zoneID: "", aisleCode: "A07", sequenceHint: 7, direction: shared.TwoWay, wantErr: aisle.ErrEmptyZoneID},
		{name: "empty aisle code", zoneID: "WH1-STOR-AMB", aisleCode: "", sequenceHint: 7, direction: shared.TwoWay, wantErr: aisle.ErrEmptyAisleCode},
		{name: "lowercase aisle code", zoneID: "WH1-STOR-AMB", aisleCode: "a07", sequenceHint: 7, direction: shared.TwoWay, wantErr: aisle.ErrInvalidAisleCode},
		{name: "negative sequence hint", zoneID: "WH1-STOR-AMB", aisleCode: "A07", sequenceHint: -1, direction: shared.TwoWay, wantErr: aisle.ErrNegativeSequenceHint},
		{name: "unknown direction", zoneID: "WH1-STOR-AMB", aisleCode: "A07", sequenceHint: 7, direction: "Sideways", wantErr: shared.ErrUnknownDirection},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertNewAisle(t, tc)
		})
	}
}

// assertNewAisle exercises one table row: the error arm checks the typed
// sentinel, the success arm checks the aisle's identity, scoping, travel
// metadata, and lifecycle status.
func assertNewAisle(t *testing.T, tc newAisleCase) {
	t.Helper()
	a, err := aisle.NewAisle(tc.zoneID, tc.aisleCode, tc.sequenceHint, tc.direction)
	if tc.wantErr != nil {
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("expected error %v, got %v", tc.wantErr, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.ID() != tc.wantID {
		t.Fatalf("expected aisle id %q, got %q", tc.wantID, a.ID())
	}
	if a.ZoneID() != tc.zoneID || a.AisleCode() != tc.aisleCode {
		t.Fatalf("unexpected scoping: %s/%s", a.ZoneID(), a.AisleCode())
	}
	if a.SequenceHint() != tc.sequenceHint || a.Direction() != tc.direction {
		t.Fatalf("unexpected travel metadata: hint=%d direction=%q", a.SequenceHint(), a.Direction())
	}
	if !a.IsActive() || a.Status() != shared.Active {
		t.Fatalf("a newly registered aisle must be Active, got %q", a.Status())
	}
}

func TestAisleDecommissionIsOneWay(t *testing.T) {
	a, err := aisle.NewAisle("WH1-STOR-AMB", "A07", 7, shared.TwoWay)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := a.Decommission(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.IsActive() {
		t.Fatal("expected the aisle to stop being Active")
	}
	if err := a.Decommission(); !errors.Is(err, aisle.ErrAlreadyDecommissioned) {
		t.Fatalf("expected ErrAlreadyDecommissioned, got %v", err)
	}
}

func TestRehydrateAislePreservesPersistedState(t *testing.T) {
	a := aisle.RehydrateAisle("WH1-STOR-AMB", "A09", 9, shared.OneWay, shared.UnderMaintenance, shared.Segment{})
	if a.ID() != "WH1-STOR-AMB-A09" {
		t.Fatalf("unexpected id %q", a.ID())
	}
	if a.IsActive() {
		t.Fatal("an UnderMaintenance aisle must not report as Active")
	}
	if a.SequenceHint() != 9 || a.Direction() != shared.OneWay {
		t.Fatalf("unexpected travel metadata: %d/%q", a.SequenceHint(), a.Direction())
	}
}
