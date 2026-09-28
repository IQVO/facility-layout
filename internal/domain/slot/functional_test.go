package slot_test

import (
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/slot"
)

func TestParseDockFlow(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    slot.DockFlow
		wantErr error
	}{
		{name: "inbound", raw: "Inbound", want: slot.Inbound},
		{name: "outbound", raw: "Outbound", want: slot.Outbound},
		{name: "both", raw: "Both", want: slot.Both},
		{name: "unknown", raw: "Sideways", wantErr: slot.ErrUnknownDockFlow},
		{name: "empty", raw: "", wantErr: slot.ErrUnknownDockFlow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := slot.ParseDockFlow(tc.raw)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestParseActivity(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    slot.Activity
		wantErr error
	}{
		{name: "pack", raw: "Pack", want: slot.Pack},
		{name: "sort", raw: "Sort", want: slot.Sort},
		{name: "qc", raw: "QC", want: slot.ActivityQC},
		{name: "vas", raw: "VAS", want: slot.VAS},
		{name: "deconsolidate", raw: "Deconsolidate", want: slot.Deconsolidate},
		{name: "receive", raw: "Receive", want: slot.Receive},
		{name: "kit", raw: "Kit", want: slot.Kit},
		{name: "unknown", raw: "Juggle", wantErr: slot.ErrUnknownActivity},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := slot.ParseActivity(tc.raw)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestNewFunctionalAttributes(t *testing.T) {
	t.Run("dock requires a dock flow", testFunctionalDockRequiresFlow)
	t.Run("dock rejects activities", testFunctionalDockRejectsActivities)
	t.Run("dock with a valid flow succeeds", testFunctionalDockValidFlow)
	t.Run("work center requires at least one activity", testFunctionalWorkCenterRequiresActivities)
	t.Run("work center rejects a dock flow", testFunctionalWorkCenterRejectsDockFlow)
	t.Run("work center dedupes and sorts activities", testFunctionalWorkCenterSortsActivities)
	t.Run("an unknown activity is rejected", testFunctionalUnknownActivityRejected)
	t.Run("storage rejects both dock flow and activities", testFunctionalStorageRejectsBoth)
	t.Run("storage with neither is the zero value", testFunctionalStorageZero)
}

func testFunctionalDockRequiresFlow(t *testing.T) {
	t.Helper()
	_, err := slot.NewFunctionalAttributes(placement.Dock, "", nil)
	if !errors.Is(err, slot.ErrDockFlowRequired) {
		t.Fatalf("expected ErrDockFlowRequired, got %v", err)
	}
}

func testFunctionalDockRejectsActivities(t *testing.T) {
	t.Helper()
	_, err := slot.NewFunctionalAttributes(placement.Dock, slot.Inbound, []slot.Activity{slot.Pack})
	if !errors.Is(err, slot.ErrFunctionalAttributesNotAllowed) {
		t.Fatalf("expected ErrFunctionalAttributesNotAllowed, got %v", err)
	}
}

func testFunctionalDockValidFlow(t *testing.T) {
	t.Helper()
	f, err := slot.NewFunctionalAttributes(placement.Dock, slot.Both, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.DockFlow() != slot.Both || f.Activities() != nil {
		t.Fatalf("unexpected functional attributes: %+v", f)
	}
}

func testFunctionalWorkCenterRequiresActivities(t *testing.T) {
	t.Helper()
	_, err := slot.NewFunctionalAttributes(placement.WorkCenter, "", nil)
	if !errors.Is(err, slot.ErrWorkCenterActivitiesRequired) {
		t.Fatalf("expected ErrWorkCenterActivitiesRequired, got %v", err)
	}
}

func testFunctionalWorkCenterRejectsDockFlow(t *testing.T) {
	t.Helper()
	_, err := slot.NewFunctionalAttributes(placement.WorkCenter, slot.Inbound, []slot.Activity{slot.Pack})
	if !errors.Is(err, slot.ErrFunctionalAttributesNotAllowed) {
		t.Fatalf("expected ErrFunctionalAttributesNotAllowed, got %v", err)
	}
}

func testFunctionalWorkCenterSortsActivities(t *testing.T) {
	t.Helper()
	f, err := slot.NewFunctionalAttributes(placement.WorkCenter, "", []slot.Activity{slot.VAS, slot.Pack, slot.VAS})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	activities := f.Activities()
	if len(activities) != 2 || activities[0] != slot.Pack || activities[1] != slot.VAS {
		t.Fatalf("expected deduped, sorted [Pack VAS], got %v", activities)
	}
}

func testFunctionalUnknownActivityRejected(t *testing.T) {
	t.Helper()
	_, err := slot.NewFunctionalAttributes(placement.WorkCenter, "", []slot.Activity{"Juggle"})
	if !errors.Is(err, slot.ErrUnknownActivity) {
		t.Fatalf("expected ErrUnknownActivity, got %v", err)
	}
}

func testFunctionalStorageRejectsBoth(t *testing.T) {
	t.Helper()
	if _, err := slot.NewFunctionalAttributes(placement.Storage, slot.Inbound, nil); !errors.Is(err, slot.ErrFunctionalAttributesNotAllowed) {
		t.Fatalf("expected ErrFunctionalAttributesNotAllowed for a dock flow on Storage, got %v", err)
	}
	if _, err := slot.NewFunctionalAttributes(placement.Storage, "", []slot.Activity{slot.Pack}); !errors.Is(err, slot.ErrFunctionalAttributesNotAllowed) {
		t.Fatalf("expected ErrFunctionalAttributesNotAllowed for activities on Storage, got %v", err)
	}
}

func testFunctionalStorageZero(t *testing.T) {
	t.Helper()
	f, err := slot.NewFunctionalAttributes(placement.Storage, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.IsZero() {
		t.Fatalf("expected the zero FunctionalAttributes, got %+v", f)
	}
}
