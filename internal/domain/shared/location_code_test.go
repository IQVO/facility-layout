package shared_test

import (
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// locationCodeCase is one row of the NewLocationCode table: either a valid
// seven-segment code or an invalid one (wantErr set).
type locationCodeCase struct {
	name                                          string
	site, area, zone, aisle, bay, level, position string
	wantErr                                       error
}

func TestNewLocationCode(t *testing.T) {
	tests := []locationCodeCase{
		{name: "the canonical industry-standard code", site: "WH1", area: "STOR", zone: "AMB", aisle: "A07", bay: "03", level: "02", position: "B"},
		{name: "digits only everywhere", site: "1", area: "2", zone: "3", aisle: "4", bay: "5", level: "6", position: "7"},
		{name: "empty site", site: "", area: "STOR", zone: "AMB", aisle: "A07", bay: "03", level: "02", position: "B", wantErr: shared.ErrEmptyLocationSegment},
		{name: "empty area", site: "WH1", area: "", zone: "AMB", aisle: "A07", bay: "03", level: "02", position: "B", wantErr: shared.ErrEmptyLocationSegment},
		{name: "empty zone", site: "WH1", area: "STOR", zone: "", aisle: "A07", bay: "03", level: "02", position: "B", wantErr: shared.ErrEmptyLocationSegment},
		{name: "empty aisle", site: "WH1", area: "STOR", zone: "AMB", aisle: "", bay: "03", level: "02", position: "B", wantErr: shared.ErrEmptyLocationSegment},
		{name: "empty bay", site: "WH1", area: "STOR", zone: "AMB", aisle: "A07", bay: "", level: "02", position: "B", wantErr: shared.ErrEmptyLocationSegment},
		{name: "empty level", site: "WH1", area: "STOR", zone: "AMB", aisle: "A07", bay: "03", level: "", position: "B", wantErr: shared.ErrEmptyLocationSegment},
		{name: "empty position", site: "WH1", area: "STOR", zone: "AMB", aisle: "A07", bay: "03", level: "02", position: "", wantErr: shared.ErrEmptyLocationSegment},
		{name: "lowercase is rejected", site: "wh1", area: "STOR", zone: "AMB", aisle: "A07", bay: "03", level: "02", position: "B", wantErr: shared.ErrInvalidLocationSegment},
		{name: "hyphen inside a segment is rejected", site: "WH1", area: "ST-OR", zone: "AMB", aisle: "A07", bay: "03", level: "02", position: "B", wantErr: shared.ErrInvalidLocationSegment},
		{name: "underscore is rejected", site: "WH1", area: "STOR", zone: "AM_B", aisle: "A07", bay: "03", level: "02", position: "B", wantErr: shared.ErrInvalidLocationSegment},
		{name: "space is rejected", site: "WH1", area: "STOR", zone: "AMB", aisle: "A 7", bay: "03", level: "02", position: "B", wantErr: shared.ErrInvalidLocationSegment},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertNewLocationCode(t, tc)
		})
	}
}

// assertNewLocationCode exercises one table row: the error arm checks the
// typed sentinel and the zero value, the success arm checks that all seven
// segments round-trip.
func assertNewLocationCode(t *testing.T, tc locationCodeCase) {
	t.Helper()
	code, err := shared.NewLocationCode(tc.site, tc.area, tc.zone, tc.aisle, tc.bay, tc.level, tc.position)
	if tc.wantErr != nil {
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("expected error %v, got %v", tc.wantErr, err)
		}
		if !code.IsZero() {
			t.Fatal("expected the zero LocationCode on failure")
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code.Site() != tc.site || code.Area() != tc.area || code.Zone() != tc.zone ||
		code.Aisle() != tc.aisle || code.Bay() != tc.bay || code.Level() != tc.level || code.Position() != tc.position {
		t.Fatalf("segments did not round-trip: %+v", code)
	}
	if code.IsZero() {
		t.Fatal("a valid code must never be the zero value")
	}
}

func TestLocationCodeRoundTrip(t *testing.T) {
	const raw = "WH1-STOR-AMB-A07-03-02-B"

	code, err := shared.ParseLocationCode(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := code.String(); got != raw {
		t.Fatalf("expected round-trip %q, got %q", raw, got)
	}
	if got := code.ZoneID(); got != "WH1-STOR-AMB" {
		t.Fatalf("expected zone id WH1-STOR-AMB, got %q", got)
	}
	if got := code.AisleID(); got != "WH1-STOR-AMB-A07" {
		t.Fatalf("expected aisle id WH1-STOR-AMB-A07, got %q", got)
	}
}

func TestParseLocationCodeRejectsMalformed(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr error
	}{
		{name: "too few segments", raw: "WH1-STOR-AMB-A07", wantErr: shared.ErrMalformedLocationCode},
		{name: "too many segments", raw: "WH1-STOR-AMB-A07-03-02-B-X", wantErr: shared.ErrMalformedLocationCode},
		{name: "empty string", raw: "", wantErr: shared.ErrMalformedLocationCode},
		{name: "right shape but empty segment", raw: "WH1-STOR-AMB-A07-03--B", wantErr: shared.ErrEmptyLocationSegment},
		{name: "right shape but lowercase", raw: "WH1-stor-AMB-A07-03-02-B", wantErr: shared.ErrInvalidLocationSegment},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := shared.ParseLocationCode(tc.raw); !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error %v, got %v", tc.wantErr, err)
			}
		})
	}
}
