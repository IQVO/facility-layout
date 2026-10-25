package usecases_test

import (
	"context"
	"testing"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

type capabilityFixture struct {
	sites     *memory.SiteRepo
	publisher *events.BufferedPublisher
	change    *usecases.ChangeSiteTransferCapability
}

func newCapabilityFixture(t *testing.T) capabilityFixture {
	t.Helper()
	f := capabilityFixture{sites: memory.NewSiteRepo(), publisher: events.NewBufferedPublisher()}
	f.change = &usecases.ChangeSiteTransferCapability{Sites: f.sites, Events: f.publisher, Clock: memory.NewFixedClock(fixedNow)}
	s, err := site.NewSite("WH1", "Fulfilment Centre One")
	if err != nil {
		t.Fatalf("seed site: %v", err)
	}
	if err := f.sites.Save(context.Background(), s); err != nil {
		t.Fatalf("seed site: %v", err)
	}
	return f
}

func (f capabilityFixture) capabilityEvents() []shared.SiteCapabilityChanged {
	out := []shared.SiteCapabilityChanged{}
	for _, e := range f.publisher.Events() {
		if c, ok := e.(shared.SiteCapabilityChanged); ok {
			out = append(out, c)
		}
	}
	return out
}

func TestChangeSiteTransferCapabilityPublishesTheFullSnapshotWithItsRevision(t *testing.T) {
	f := newCapabilityFixture(t)

	got, err := f.change.Execute(context.Background(), "WH1", site.NewTransferCapability(true, false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.CapabilityRevision() != 1 || !got.TransferCapability().OriginEnabled() || got.TransferCapability().DestinationEnabled() {
		t.Fatalf("returned site: revision=%d capability=%+v", got.CapabilityRevision(), got.TransferCapability())
	}
	evts := f.capabilityEvents()
	if len(evts) != 1 {
		t.Fatalf("expected exactly one SiteCapabilityChanged, got %d", len(evts))
	}
	want := shared.NewSiteCapabilityChanged(fixedNow, "WH1", true, false, 1)
	if evts[0] != want {
		t.Fatalf("event = %+v, want %+v", evts[0], want)
	}
	if evts[0].EventType() != "com.warehouse.wms.facility-layout.site.SiteCapabilityChanged" {
		t.Fatalf("event type = %q", evts[0].EventType())
	}

	stored, err := f.sites.FindByCode(context.Background(), "WH1")
	if err != nil || stored.CapabilityRevision() != 1 {
		t.Fatalf("persisted revision: %v err=%v", stored, err)
	}
}

func TestChangeSiteTransferCapabilityRepeatIsAnIdempotentNoOp(t *testing.T) {
	f := newCapabilityFixture(t)
	ctx := context.Background()

	if _, err := f.change.Execute(ctx, "WH1", site.NewTransferCapability(true, true)); err != nil {
		t.Fatalf("first change: %v", err)
	}
	again, err := f.change.Execute(ctx, "WH1", site.NewTransferCapability(true, true))
	if err != nil {
		t.Fatalf("repeat: %v", err)
	}
	if again.CapabilityRevision() != 1 {
		t.Fatalf("a repeated snapshot must not advance the revision, got %d", again.CapabilityRevision())
	}
	if n := len(f.capabilityEvents()); n != 1 {
		t.Fatalf("a repeated snapshot must not publish again, got %d events", n)
	}

	next, err := f.change.Execute(ctx, "WH1", site.NewTransferCapability(false, true))
	if err != nil {
		t.Fatalf("second change: %v", err)
	}
	evts := f.capabilityEvents()
	if next.CapabilityRevision() != 2 || len(evts) != 2 || evts[1].CapabilityRevision != 2 || evts[1].TransferOriginEnabled {
		t.Fatalf("second change: revision=%d events=%+v", next.CapabilityRevision(), evts)
	}
}

func TestChangeSiteTransferCapabilityRejectsUnknownAndRetiredSites(t *testing.T) {
	f := newCapabilityFixture(t)
	ctx := context.Background()

	if _, err := f.change.Execute(ctx, "WH9", site.NewTransferCapability(true, true)); !isErr(err, usecases.ErrSiteNotFound) {
		t.Fatalf("unknown site: expected ErrSiteNotFound, got %v", err)
	}

	retired := site.RehydrateSite("WH2", "Two", site.RoleUnspecified, shared.Decommissioned, site.TransferCapability{}, 0, 1)
	if err := f.sites.Save(ctx, retired); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := f.change.Execute(ctx, "WH2", site.NewTransferCapability(true, true)); !isErr(err, site.ErrSiteDecommissioned) {
		t.Fatalf("retired site: expected ErrSiteDecommissioned, got %v", err)
	}
	if n := len(f.capabilityEvents()); n != 0 {
		t.Fatalf("a rejected change must publish nothing, got %d", n)
	}
}

func TestChangeSiteTransferCapabilityPropagatesInfrastructureFailures(t *testing.T) {
	ctx := context.Background()
	for name, repo := range map[string]*faultySiteRepo{
		"find": {SiteRepo: memory.NewSiteRepo(), failFind: true},
		"save": {SiteRepo: memory.NewSiteRepo(), failSave: false},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := site.NewSite("WH1", "One")
			_ = repo.SiteRepo.Save(ctx, s)
			if name == "save" {
				repo.failSave = true
			}
			uc := &usecases.ChangeSiteTransferCapability{Sites: repo, Events: events.NewBufferedPublisher(), Clock: memory.NewFixedClock(fixedNow)}
			if _, err := uc.Execute(ctx, "WH1", site.NewTransferCapability(true, false)); !isErr(err, errBoom) {
				t.Fatalf("expected errBoom, got %v", err)
			}
		})
	}
	t.Run("publish", func(t *testing.T) {
		sites := memory.NewSiteRepo()
		s, _ := site.NewSite("WH1", "One")
		_ = sites.Save(ctx, s)
		uc := &usecases.ChangeSiteTransferCapability{Sites: sites, Events: faultyPublisher{}, Clock: memory.NewFixedClock(fixedNow)}
		if _, err := uc.Execute(ctx, "WH1", site.NewTransferCapability(true, false)); !isErr(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}
