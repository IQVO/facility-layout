package site_test

import (
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

var heartbeatNow = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

const heartbeatInterval = 5 * time.Minute

// declaredSite is an active site that has published revision 3 of
// (origin true, destination false), last heartbeated at last (zero: never).
func declaredSite(revision int64, last time.Time) *site.Site {
	return site.RehydrateSite("WH1", "One", site.RoleUnspecified, shared.Active,
		site.NewTransferCapability(true, false), revision, 4, site.CapabilityHeartbeatAt(last))
}

func TestHeartbeatDueBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		s        *site.Site
		interval time.Duration
		want     bool
	}{
		{"never heartbeated", declaredSite(3, time.Time{}), heartbeatInterval, true},
		{"one nanosecond short of the interval", declaredSite(3, heartbeatNow.Add(-heartbeatInterval+time.Nanosecond)), heartbeatInterval, false},
		{"exactly one interval old", declaredSite(3, heartbeatNow.Add(-heartbeatInterval)), heartbeatInterval, true},
		{"one nanosecond past the interval", declaredSite(3, heartbeatNow.Add(-heartbeatInterval-time.Nanosecond)), heartbeatInterval, true},
		{"just heartbeated", declaredSite(3, heartbeatNow), heartbeatInterval, false},
		{"revision 1 is declared", declaredSite(1, time.Time{}), heartbeatInterval, true},
		{"revision 0 was never declared", declaredSite(0, time.Time{}), heartbeatInterval, false},
		{"zero interval disables", declaredSite(3, time.Time{}), 0, false},
		{"negative interval disables", declaredSite(3, time.Time{}), -time.Minute, false},
		{"one nanosecond interval is enabled", declaredSite(3, time.Time{}), time.Nanosecond, true},
		{"decommissioned site", site.RehydrateSite("WH1", "One", site.RoleUnspecified, shared.Decommissioned,
			site.NewTransferCapability(true, true), 3, 4), heartbeatInterval, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.HeartbeatDue(heartbeatNow, tc.interval); got != tc.want {
				t.Fatalf("HeartbeatDue = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHeartbeatTransferCapabilityAdvancesTheRevisionAndKeepsTheValues(t *testing.T) {
	s := declaredSite(3, time.Time{})

	if !s.HeartbeatTransferCapability(heartbeatNow, heartbeatInterval) {
		t.Fatal("a due site must be heartbeated")
	}
	if s.CapabilityRevision() != 4 {
		t.Fatalf("revision = %d, want 4 (+1 per heartbeat)", s.CapabilityRevision())
	}
	if c := s.TransferCapability(); !c.OriginEnabled() || c.DestinationEnabled() {
		t.Fatalf("a heartbeat must keep the current values, got %+v", c)
	}
	if !s.CapabilityHeartbeatAt().Equal(heartbeatNow) {
		t.Fatalf("heartbeat time = %v, want %v", s.CapabilityHeartbeatAt(), heartbeatNow)
	}
	if s.Version() != 4 {
		t.Fatalf("version is owned by the repository, got %d", s.Version())
	}
}

func TestHeartbeatTransferCapabilityIsANoOpWhenNotDue(t *testing.T) {
	last := heartbeatNow.Add(-heartbeatInterval + time.Nanosecond)
	s := declaredSite(3, last)

	if s.HeartbeatTransferCapability(heartbeatNow, heartbeatInterval) {
		t.Fatal("a site heartbeated less than an interval ago is not due")
	}
	if s.CapabilityRevision() != 3 || !s.CapabilityHeartbeatAt().Equal(last) {
		t.Fatalf("a refused heartbeat must change nothing: revision=%d at=%v", s.CapabilityRevision(), s.CapabilityHeartbeatAt())
	}
	if declaredSite(0, time.Time{}).HeartbeatTransferCapability(heartbeatNow, heartbeatInterval) {
		t.Fatal("a site that never declared a capability has nothing to re-assert")
	}
}

// The heartbeat must not weaken ADR-0035: a PUT of the value the site holds
// stays an idempotent no-op, before and after heartbeats.
func TestAHeartbeatDoesNotChangeThePutNoOpSemantics(t *testing.T) {
	s := declaredSite(3, time.Time{})
	s.HeartbeatTransferCapability(heartbeatNow, heartbeatInterval)

	changed, err := s.ChangeTransferCapability(site.NewTransferCapability(true, false))
	if err != nil || changed {
		t.Fatalf("restating the current value must stay a no-op, changed=%v err=%v", changed, err)
	}
	if s.CapabilityRevision() != 4 {
		t.Fatalf("a no-op PUT must not move the revision, got %d", s.CapabilityRevision())
	}
	changed, err = s.ChangeTransferCapability(site.NewTransferCapability(true, true))
	if err != nil || !changed || s.CapabilityRevision() != 5 {
		t.Fatalf("a real change advances from the heartbeat revision: changed=%v err=%v revision=%d", changed, err, s.CapabilityRevision())
	}
}

func TestNewSiteHasNoHeartbeat(t *testing.T) {
	if !newActiveSite(t).CapabilityHeartbeatAt().IsZero() {
		t.Fatal("a new site has never heartbeated")
	}
}
