package slot_test

import (
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/slot"
)

func TestPickIsAWorkCenterActivity(t *testing.T) {
	a, err := slot.ParseActivity("Pick")
	if err != nil || a != slot.Pick {
		t.Fatalf("ParseActivity(Pick) = %q %v", a, err)
	}
	attrs, err := slot.NewFunctionalAttributes(placement.WorkCenter, "", []slot.Activity{slot.Pick, slot.Pack, slot.Pick})
	if err != nil {
		t.Fatal(err)
	}
	if got := attrs.Activities(); len(got) != 2 || got[0] != slot.Pack || got[1] != slot.Pick {
		t.Fatalf("activities = %v, want [Pack Pick] (deduplicated, sorted)", got)
	}
	if _, err := slot.ParseActivity("pick"); err == nil {
		t.Fatal("activities are case-sensitive")
	}
}

func TestPickIsNotAllowedOutsideAWorkCenter(t *testing.T) {
	if _, err := slot.NewFunctionalAttributes(placement.Drop, "", []slot.Activity{slot.Pick}); err == nil {
		t.Fatal("an activity on a Drop location must be refused")
	}
}
