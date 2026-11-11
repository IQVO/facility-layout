package shared_test

import (
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

func TestParseSpotKind(t *testing.T) {
	tests := []struct {
		in      string
		want    shared.SpotKind
		wantErr error
	}{
		{"PARKING", shared.SpotKindParking, nil},
		{"HOLDING", shared.SpotKindHolding, nil},
		{"REEFER_PLUG", shared.SpotKindReeferPlug, nil},
		{"HAZMAT", shared.SpotKindHazmat, nil},
		{"STAGING_LANE", shared.SpotKindStagingLane, nil},
		{"", "", shared.ErrUnknownSpotKind},
		{"parking", "", shared.ErrUnknownSpotKind},
		{"DOCK", "", shared.ErrUnknownSpotKind},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := shared.ParseSpotKind(tc.in)
			if !errors.Is(err, tc.wantErr) || got != tc.want {
				t.Fatalf("ParseSpotKind(%q) = %q, %v; want %q, %v", tc.in, got, err, tc.want, tc.wantErr)
			}
		})
	}
}
