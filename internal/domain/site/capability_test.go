package site_test

import (
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

func newActiveSite(t *testing.T) *site.Site {
	t.Helper()
	s, err := site.NewSite("WH1", "Fulfilment Centre One")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return s
}

func TestNewSiteStartsWithNoTransferCapabilityAndNoRevision(t *testing.T) {
	s := newActiveSite(t)
	if c := s.TransferCapability(); c.OriginEnabled() || c.DestinationEnabled() {
		t.Fatalf("a new site must not be transfer-capable, got %+v", c)
	}
	if s.CapabilityRevision() != 0 {
		t.Fatalf("a new site has never published a capability, revision = %d, want 0", s.CapabilityRevision())
	}
	if s.Version() != 1 {
		t.Fatalf("a fresh aggregate starts at version 1, got %d", s.Version())
	}
}

// The first change is always a change, even when it repeats the default
// (both disabled): the site has never published a capability snapshot, so
// consumers have nothing to apply until revision 1 exists.
func TestChangeTransferCapabilityFirstChangeAlwaysAdvancesTheRevision(t *testing.T) {
	s := newActiveSite(t)
	changed, err := s.ChangeTransferCapability(site.NewTransferCapability(false, false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed || s.CapabilityRevision() != 1 {
		t.Fatalf("first change: changed=%v revision=%d, want true/1", changed, s.CapabilityRevision())
	}
}

func TestChangeTransferCapabilityAdvancesTheRevisionByExactlyOnePerChange(t *testing.T) {
	s := newActiveSite(t)
	steps := []struct {
		origin, destination bool
		wantChanged         bool
		wantRevision        int64
	}{
		{true, false, true, 1},
		{true, false, false, 1}, // identical snapshot: idempotent, no new revision
		{true, true, true, 2},
		{false, true, true, 3},
		{false, false, true, 4},
		{false, false, false, 4},
	}
	for i, step := range steps {
		changed, err := s.ChangeTransferCapability(site.NewTransferCapability(step.origin, step.destination))
		if err != nil {
			t.Fatalf("step %d: unexpected error: %v", i, err)
		}
		if changed != step.wantChanged || s.CapabilityRevision() != step.wantRevision {
			t.Fatalf("step %d: changed=%v revision=%d, want %v/%d", i, changed, s.CapabilityRevision(), step.wantChanged, step.wantRevision)
		}
		c := s.TransferCapability()
		if c.OriginEnabled() != step.origin || c.DestinationEnabled() != step.destination {
			t.Fatalf("step %d: capability = %+v, want origin=%v destination=%v", i, c, step.origin, step.destination)
		}
	}
}

func TestChangeTransferCapabilityRejectsADecommissionedSite(t *testing.T) {
	s := site.RehydrateSite("WH2", "Fulfilment Centre Two", shared.Decommissioned, site.NewTransferCapability(true, true), 3, 4)
	changed, err := s.ChangeTransferCapability(site.NewTransferCapability(false, false))
	if !errors.Is(err, site.ErrSiteDecommissioned) {
		t.Fatalf("expected ErrSiteDecommissioned, got %v", err)
	}
	if changed || s.CapabilityRevision() != 3 {
		t.Fatalf("a rejected change must leave the site untouched: changed=%v revision=%d", changed, s.CapabilityRevision())
	}
	if c := s.TransferCapability(); !c.OriginEnabled() || !c.DestinationEnabled() {
		t.Fatalf("a rejected change must not alter the capability, got %+v", c)
	}
}

func TestRehydrateSitePreservesCapabilityRevisionAndVersion(t *testing.T) {
	s := site.RehydrateSite("WH3", "Three", shared.Active, site.NewTransferCapability(true, false), 7, 9)
	c := s.TransferCapability()
	if !c.OriginEnabled() || c.DestinationEnabled() {
		t.Fatalf("capability = %+v, want origin only", c)
	}
	if s.CapabilityRevision() != 7 || s.Version() != 9 {
		t.Fatalf("revision=%d version=%d, want 7/9", s.CapabilityRevision(), s.Version())
	}
}
