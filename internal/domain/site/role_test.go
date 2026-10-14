package site_test

import (
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

// wantDefaults is the transfer-capability table of the final-gaps decision E2
// (ADR-0037), written out by hand so a change to the domain table must be a
// conscious change here too.
var wantDefaults = []struct {
	role        site.Role
	origin      bool
	destination bool
}{
	{site.RoleRoboticsSortableFC, true, true},
	{site.RoleNonSortableFC, true, true},
	{site.RoleTraditionalSortableFC, true, true},
	{site.RoleSubSameDayFC, true, true},
	{site.RoleFreshGroceryFC, false, false},
	{site.RoleXLFC, true, true},
	{site.RoleSoftLinesSortableFC, true, true},
	{site.RoleMicroFC, false, true},
	{site.RoleSimulatedFC, true, true},
	{site.RoleInboundCrossDock, true, false},
	{site.RoleNationalInboundCrossDock, true, false},
	{site.RoleReturnsCenter, true, false},
	{site.RoleSortationCenter, false, false},
	{site.RoleDeliveryStation, false, false},
	{site.RoleAirHubGateway, false, false},
}

func TestTransferDefaultsIsTheDecisionTableInOrder(t *testing.T) {
	got := site.TransferDefaults()
	if len(got) != len(wantDefaults) {
		t.Fatalf("got %d defaults, want %d", len(got), len(wantDefaults))
	}
	for i, w := range wantDefaults {
		g := got[i]
		if g.Role != w.role || g.Capability.OriginEnabled() != w.origin || g.Capability.DestinationEnabled() != w.destination {
			t.Errorf("row %d: got %s origin=%v destination=%v, want %s origin=%v destination=%v",
				i, g.Role, g.Capability.OriginEnabled(), g.Capability.DestinationEnabled(), w.role, w.origin, w.destination)
		}
	}
}

func TestTransferDefaultsReturnsACopy(t *testing.T) {
	first := site.TransferDefaults()
	first[0].Role = site.RoleDeliveryStation
	if site.TransferDefaults()[0].Role != site.RoleRoboticsSortableFC {
		t.Fatal("mutating the returned slice changed the domain table")
	}
}

func TestDefaultTransferCapabilityPerRole(t *testing.T) {
	for _, w := range wantDefaults {
		c, ok := w.role.DefaultTransferCapability()
		if !ok || c.OriginEnabled() != w.origin || c.DestinationEnabled() != w.destination {
			t.Errorf("%s: got %+v ok=%v", w.role, c, ok)
		}
	}
	if c, ok := site.RoleUnspecified.DefaultTransferCapability(); ok || c != (site.TransferCapability{}) {
		t.Errorf("UNSPECIFIED has no default: got %+v ok=%v", c, ok)
	}
}

func TestParseRoleAcceptsEveryKnownRoleAndUnspecified(t *testing.T) {
	for _, w := range wantDefaults {
		got, err := site.ParseRole(string(w.role))
		if err != nil || got != w.role {
			t.Errorf("ParseRole(%q) = %q, %v", w.role, got, err)
		}
	}
	got, err := site.ParseRole("UNSPECIFIED")
	if err != nil || got != site.RoleUnspecified {
		t.Errorf("ParseRole(UNSPECIFIED) = %q, %v", got, err)
	}
}

func TestParseRoleRejectsAnythingElse(t *testing.T) {
	for _, raw := range []string{"", "non_sortable_fc", "Non-Sortable Fulfillment Center", "FC", " NON_SORTABLE_FC"} {
		if _, err := site.ParseRole(raw); !errors.Is(err, site.ErrInvalidSiteRole) {
			t.Errorf("ParseRole(%q): expected ErrInvalidSiteRole, got %v", raw, err)
		}
	}
}

func TestRolesListsUnspecifiedThenTheTableRoles(t *testing.T) {
	roles := site.Roles()
	if len(roles) != len(wantDefaults)+1 || roles[0] != site.RoleUnspecified {
		t.Fatalf("Roles() = %v", roles)
	}
	for i, w := range wantDefaults {
		if roles[i+1] != w.role {
			t.Errorf("Roles()[%d] = %s, want %s", i+1, roles[i+1], w.role)
		}
	}
}

func TestNewSiteDefaultsToUnspecifiedRole(t *testing.T) {
	s, err := site.NewSite("WH1", "One")
	if err != nil {
		t.Fatalf("NewSite: %v", err)
	}
	if s.Role() != site.RoleUnspecified {
		t.Fatalf("role = %q, want UNSPECIFIED", s.Role())
	}
}

func TestNewSiteWithRole(t *testing.T) {
	s, err := site.NewSiteWithRole("ABE8", "ABE8 - Non-Sortable Fulfillment Center (Breinigsville, PA)", site.RoleNonSortableFC)
	if err != nil || s.Role() != site.RoleNonSortableFC {
		t.Fatalf("NewSiteWithRole: %v role=%q", err, s.Role())
	}
	if _, err := site.NewSiteWithRole("WH1", "One", site.Role("WAREHOUSE")); !errors.Is(err, site.ErrInvalidSiteRole) {
		t.Fatalf("unknown role: expected ErrInvalidSiteRole, got %v", err)
	}
	if _, err := site.NewSiteWithRole("wh1", "One", site.RoleXLFC); !errors.Is(err, site.ErrInvalidSiteCode) {
		t.Fatalf("bad code: expected ErrInvalidSiteCode, got %v", err)
	}
	if _, err := site.NewSiteWithRole("WH1", "", site.RoleXLFC); !errors.Is(err, site.ErrEmptySiteName) {
		t.Fatalf("empty name: expected ErrEmptySiteName, got %v", err)
	}
}

func TestChangeRoleReportsThePreviousRole(t *testing.T) {
	s, _ := site.NewSite("WH1", "One")
	previous, changed, err := s.ChangeRole(site.RoleSortationCenter)
	if err != nil || !changed || previous != site.RoleUnspecified || s.Role() != site.RoleSortationCenter {
		t.Fatalf("first change: previous=%q changed=%v err=%v role=%q", previous, changed, err, s.Role())
	}
	previous, changed, err = s.ChangeRole(site.RoleDeliveryStation)
	if err != nil || !changed || previous != site.RoleSortationCenter || s.Role() != site.RoleDeliveryStation {
		t.Fatalf("second change: previous=%q changed=%v err=%v role=%q", previous, changed, err, s.Role())
	}
}

func TestChangeRoleToTheCurrentRoleIsANoOp(t *testing.T) {
	s, _ := site.NewSiteWithRole("WH1", "One", site.RoleXLFC)
	previous, changed, err := s.ChangeRole(site.RoleXLFC)
	if err != nil || changed || previous != site.RoleXLFC {
		t.Fatalf("repeat: previous=%q changed=%v err=%v", previous, changed, err)
	}
}

func TestChangeRoleNeverTouchesTheTransferCapability(t *testing.T) {
	s, _ := site.NewSite("WH1", "One")
	if _, err := s.ChangeTransferCapability(site.NewTransferCapability(false, true)); err != nil {
		t.Fatalf("capability: %v", err)
	}
	if _, _, err := s.ChangeRole(site.RoleRoboticsSortableFC); err != nil {
		t.Fatalf("role: %v", err)
	}
	c := s.TransferCapability()
	if c.OriginEnabled() || !c.DestinationEnabled() || s.CapabilityRevision() != 1 {
		t.Fatalf("a role change flipped capability: %+v revision=%d", c, s.CapabilityRevision())
	}
}

func TestChangeRoleRejectsUnspecifiedAndUnknownRoles(t *testing.T) {
	s, _ := site.NewSiteWithRole("WH1", "One", site.RoleXLFC)
	for _, r := range []site.Role{site.RoleUnspecified, site.Role("WAREHOUSE"), site.Role("")} {
		if _, changed, err := s.ChangeRole(r); !errors.Is(err, site.ErrInvalidSiteRole) || changed {
			t.Errorf("ChangeRole(%q): changed=%v err=%v", r, changed, err)
		}
	}
	if s.Role() != site.RoleXLFC {
		t.Fatalf("a rejected change altered the role: %q", s.Role())
	}
}

func TestChangeRoleRejectsADecommissionedSite(t *testing.T) {
	s := site.RehydrateSite("WH2", "Two", site.RoleXLFC, shared.Decommissioned, site.TransferCapability{}, 0, 1)
	if _, changed, err := s.ChangeRole(site.RoleMicroFC); !errors.Is(err, site.ErrSiteDecommissioned) || changed {
		t.Fatalf("retired site: changed=%v err=%v", changed, err)
	}
	if s.Role() != site.RoleXLFC {
		t.Fatalf("role = %q", s.Role())
	}
}

func TestRehydrateSitePreservesRole(t *testing.T) {
	s := site.RehydrateSite("WH3", "Three", site.RoleAirHubGateway, shared.Active, site.TransferCapability{}, 0, 2)
	if s.Role() != site.RoleAirHubGateway {
		t.Fatalf("role = %q", s.Role())
	}
}
