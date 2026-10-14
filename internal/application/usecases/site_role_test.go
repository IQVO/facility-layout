package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

type roleFixture struct {
	sites     *memory.SiteRepo
	publisher *events.BufferedPublisher
	register  *usecases.RegisterSite
	change    *usecases.ChangeSiteRole
}

func newRoleFixture() roleFixture {
	f := roleFixture{sites: memory.NewSiteRepo(), publisher: events.NewBufferedPublisher()}
	clock := memory.NewFixedClock(fixedNow)
	f.register = &usecases.RegisterSite{Sites: f.sites, Events: f.publisher, Clock: clock}
	f.change = &usecases.ChangeSiteRole{Sites: f.sites, Events: f.publisher, Clock: clock}
	return f
}

func (f roleFixture) roleEvents() []shared.SiteRoleChanged {
	out := []shared.SiteRoleChanged{}
	for _, e := range f.publisher.Events() {
		if c, ok := e.(shared.SiteRoleChanged); ok {
			out = append(out, c)
		}
	}
	return out
}

func TestRegisterSiteCarriesTheRoleOnSiteRegistered(t *testing.T) {
	f := newRoleFixture()
	got, err := f.register.Execute(context.Background(), "ABE8", "ABE8 - Non-Sortable Fulfillment Center (Breinigsville, PA)", site.RoleNonSortableFC)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if got.Role() != site.RoleNonSortableFC {
		t.Fatalf("role = %q", got.Role())
	}
	evts := f.publisher.Events()
	want := shared.NewSiteRegistered(fixedNow, "ABE8", "ABE8 - Non-Sortable Fulfillment Center (Breinigsville, PA)", "NON_SORTABLE_FC")
	if len(evts) != 1 || evts[0] != want {
		t.Fatalf("events = %+v, want [%+v]", evts, want)
	}
}

func TestRegisterSiteRejectsAnUnknownRole(t *testing.T) {
	f := newRoleFixture()
	if _, err := f.register.Execute(context.Background(), "WH1", "One", site.Role("WAREHOUSE")); !errors.Is(err, site.ErrInvalidSiteRole) {
		t.Fatalf("expected ErrInvalidSiteRole, got %v", err)
	}
	if n := len(f.publisher.Events()); n != 0 {
		t.Fatalf("a rejected registration published %d events", n)
	}
}

func TestChangeSiteRolePublishesSiteRoleChangedWithThePreviousRole(t *testing.T) {
	f := newRoleFixture()
	ctx := context.Background()
	if _, err := f.register.Execute(ctx, "WH1", "One", site.RoleUnspecified); err != nil {
		t.Fatalf("register: %v", err)
	}

	got, err := f.change.Execute(ctx, "WH1", site.RoleXLFC)
	if err != nil || got.Role() != site.RoleXLFC {
		t.Fatalf("change: role=%v err=%v", got, err)
	}
	evts := f.roleEvents()
	want := shared.NewSiteRoleChanged(fixedNow, "WH1", "XL_FC", "UNSPECIFIED")
	if len(evts) != 1 || evts[0] != want {
		t.Fatalf("events = %+v, want [%+v]", evts, want)
	}
	if evts[0].EventType() != "com.warehouse.wms.facility-layout.site.SiteRoleChanged" {
		t.Fatalf("event type = %q", evts[0].EventType())
	}
	stored, _ := f.sites.FindByCode(ctx, "WH1")
	if stored.Role() != site.RoleXLFC {
		t.Fatalf("persisted role = %q", stored.Role())
	}
}

func TestChangeSiteRoleRepeatIsANoOpAndNeverFlipsCapability(t *testing.T) {
	f := newRoleFixture()
	ctx := context.Background()
	if _, err := f.register.Execute(ctx, "WH1", "One", site.RoleMicroFC); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, err := f.change.Execute(ctx, "WH1", site.RoleMicroFC)
	if err != nil || got.Role() != site.RoleMicroFC {
		t.Fatalf("repeat: %v %v", got, err)
	}
	if n := len(f.roleEvents()); n != 0 {
		t.Fatalf("a repeat published %d SiteRoleChanged", n)
	}
	if _, err := f.change.Execute(ctx, "WH1", site.RoleRoboticsSortableFC); err != nil {
		t.Fatalf("change: %v", err)
	}
	for _, e := range f.publisher.Events() {
		if _, ok := e.(shared.SiteCapabilityChanged); ok {
			t.Fatal("a role change published SiteCapabilityChanged")
		}
	}
	stored, _ := f.sites.FindByCode(ctx, "WH1")
	if stored.TransferCapability() != (site.TransferCapability{}) || stored.CapabilityRevision() != 0 {
		t.Fatalf("a role change flipped capability: %+v revision=%d", stored.TransferCapability(), stored.CapabilityRevision())
	}
}

func TestChangeSiteRoleRejectsUnknownSitesRetiredSitesAndUnassignableRoles(t *testing.T) {
	f := newRoleFixture()
	ctx := context.Background()
	if _, err := f.change.Execute(ctx, "WH9", site.RoleXLFC); !errors.Is(err, usecases.ErrSiteNotFound) {
		t.Fatalf("unknown site: %v", err)
	}
	retired := site.RehydrateSite("WH2", "Two", site.RoleXLFC, shared.Decommissioned, site.TransferCapability{}, 0, 1)
	_ = f.sites.Save(ctx, retired)
	if _, err := f.change.Execute(ctx, "WH2", site.RoleMicroFC); !errors.Is(err, site.ErrSiteDecommissioned) {
		t.Fatalf("retired site: %v", err)
	}
	if _, err := f.register.Execute(ctx, "WH1", "One", site.RoleXLFC); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := f.change.Execute(ctx, "WH1", site.RoleUnspecified); !errors.Is(err, site.ErrInvalidSiteRole) {
		t.Fatalf("UNSPECIFIED: %v", err)
	}
	if n := len(f.roleEvents()); n != 0 {
		t.Fatalf("a rejected change published %d events", n)
	}
}

// failingSaveRepo fails every Save, standing in for a lost optimistic
// concurrency race (ports.ErrConcurrentModification) or a database error.
type failingSaveRepo struct {
	*memory.SiteRepo
	err error
}

func (r failingSaveRepo) Save(context.Context, *site.Site) error { return r.err }

type failingFindRepo struct {
	*memory.SiteRepo
	err error
}

func (r failingFindRepo) FindByCode(context.Context, string) (*site.Site, error) { return nil, r.err }

func TestChangeSiteRolePropagatesRepositoryFailuresAndPublishesNothing(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	seeded := memory.NewSiteRepo()
	s, _ := site.NewSite("WH1", "One")
	_ = seeded.Save(ctx, s)

	for name, tc := range map[string]struct {
		repo ports.SiteRepo
		want error
	}{
		"concurrent modification on save": {failingSaveRepo{seeded, ports.ErrConcurrentModification}, ports.ErrConcurrentModification},
		"find fails":                      {failingFindRepo{seeded, boom}, boom},
	} {
		t.Run(name, func(t *testing.T) {
			publisher := events.NewBufferedPublisher()
			uc := &usecases.ChangeSiteRole{Sites: tc.repo, Events: publisher, Clock: memory.NewFixedClock(fixedNow)}
			if _, err := uc.Execute(ctx, "WH1", site.RoleXLFC); !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
			if n := len(publisher.Events()); n != 0 {
				t.Fatalf("published %d events", n)
			}
		})
	}
}

func TestListSiteRoleTransferDefaultsReturnsTheDomainTable(t *testing.T) {
	got := (&usecases.ListSiteRoleTransferDefaults{}).Execute(context.Background())
	want := site.TransferDefaults()
	if len(got) != len(want) || len(got) != 15 {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}
