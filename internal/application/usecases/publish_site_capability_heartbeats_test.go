package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

const heartbeatEvery = 5 * time.Minute

type heartbeatFixture struct {
	sites     *memory.SiteRepo
	publisher *events.BufferedPublisher
	clock     *memory.FixedClock
	change    *usecases.ChangeSiteTransferCapability
	beat      *usecases.PublishSiteCapabilityHeartbeats
}

func newHeartbeatFixture(t *testing.T) heartbeatFixture {
	t.Helper()
	f := heartbeatFixture{sites: memory.NewSiteRepo(), publisher: events.NewBufferedPublisher(), clock: memory.NewFixedClock(fixedNow)}
	f.change = &usecases.ChangeSiteTransferCapability{Sites: f.sites, Events: f.publisher, Clock: f.clock}
	f.beat = &usecases.PublishSiteCapabilityHeartbeats{
		Sites: f.sites, Due: f.sites, Events: f.publisher, Clock: f.clock, Interval: heartbeatEvery,
	}
	return f
}

func (f heartbeatFixture) declare(t *testing.T, code string, origin, destination bool) {
	t.Helper()
	s, err := site.NewSite(code, "Centre "+code)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := f.sites.Save(context.Background(), s); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := f.change.Execute(context.Background(), code, site.NewTransferCapability(origin, destination)); err != nil {
		t.Fatalf("declare: %v", err)
	}
}

func (f heartbeatFixture) capabilityEvents() []shared.SiteCapabilityChanged {
	out := []shared.SiteCapabilityChanged{}
	for _, e := range f.publisher.Events() {
		if c, ok := e.(shared.SiteCapabilityChanged); ok {
			out = append(out, c)
		}
	}
	return out
}

func TestHeartbeatRepublishesTheCurrentValuesWithAFreshTimeAndRevision(t *testing.T) {
	f := newHeartbeatFixture(t)
	f.declare(t, "WH1", true, false)
	f.declare(t, "WH2", false, true)
	f.clock.Advance(7 * time.Minute)

	res, err := f.beat.Execute(context.Background())
	if err != nil || res.Published != 2 || res.Skipped != 0 {
		t.Fatalf("result = %+v err=%v, want 2 published", res, err)
	}
	evts := f.capabilityEvents()
	if len(evts) != 4 {
		t.Fatalf("want 2 declarations + 2 heartbeats, got %d events", len(evts))
	}
	want1 := shared.NewSiteCapabilityChanged(fixedNow.Add(7*time.Minute), "WH1", true, false, 2)
	want2 := shared.NewSiteCapabilityChanged(fixedNow.Add(7*time.Minute), "WH2", false, true, 2)
	if evts[2] != want1 || evts[3] != want2 {
		t.Fatalf("heartbeats = %+v / %+v, want %+v / %+v", evts[2], evts[3], want1, want2)
	}
}

func TestHeartbeatIsDueOncePerIntervalAtTheBoundary(t *testing.T) {
	f := newHeartbeatFixture(t)
	f.declare(t, "WH1", true, true)
	ctx := context.Background()

	first, _ := f.beat.Execute(ctx)
	f.clock.Advance(heartbeatEvery - time.Nanosecond)
	early, _ := f.beat.Execute(ctx)
	f.clock.Advance(time.Nanosecond)
	onTime, _ := f.beat.Execute(ctx)

	if first.Published != 1 || early.Published != 0 || onTime.Published != 1 {
		t.Fatalf("published first=%d early=%d onTime=%d, want 1/0/1", first.Published, early.Published, onTime.Published)
	}
	if n := len(f.capabilityEvents()); n != 3 {
		t.Fatalf("declaration + 2 heartbeats = 3 events, got %d", n)
	}
}

func TestHeartbeatSkipsSitesWithNothingToAssert(t *testing.T) {
	f := newHeartbeatFixture(t)
	ctx := context.Background()
	undeclared, _ := site.NewSite("WH1", "Never declared")
	if err := f.sites.Save(ctx, undeclared); err != nil {
		t.Fatal(err)
	}
	retired := site.RehydrateSite("WH2", "Retired", site.RoleUnspecified, shared.Decommissioned, site.NewTransferCapability(true, true), 3, 4)
	if err := f.sites.Save(ctx, retired); err != nil {
		t.Fatal(err)
	}

	res, err := f.beat.Execute(ctx)
	if err != nil || res != (usecases.HeartbeatResult{}) || len(f.publisher.Events()) != 0 {
		t.Fatalf("nothing is declared and active: res=%+v err=%v events=%d", res, err, len(f.publisher.Events()))
	}
}

func TestHeartbeatDisabledIntervalPublishesNothing(t *testing.T) {
	f := newHeartbeatFixture(t)
	f.declare(t, "WH1", true, true)
	f.beat.Interval = 0

	res, err := f.beat.Execute(context.Background())
	if err != nil || res.Published != 0 || len(f.capabilityEvents()) != 1 {
		t.Fatalf("a zero interval disables the heartbeat: res=%+v err=%v", res, err)
	}
}

func TestHeartbeatDoesNotChangeThePutNoOp(t *testing.T) {
	f := newHeartbeatFixture(t)
	f.declare(t, "WH1", true, true)
	ctx := context.Background()
	if _, err := f.beat.Execute(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := f.change.Execute(ctx, "WH1", site.NewTransferCapability(true, true))
	if err != nil || got.CapabilityRevision() != 2 || len(f.capabilityEvents()) != 2 {
		t.Fatalf("restating the value after a heartbeat must publish nothing: revision=%d events=%d err=%v",
			got.CapabilityRevision(), len(f.capabilityEvents()), err)
	}
}

// failingSource and conflictingSites inject the two failure shapes of a pass.
type failingSource struct{ err error }

func (s failingSource) ListCapabilityHeartbeatDue(context.Context, time.Time, int) ([]string, error) {
	return nil, s.err
}

type savingFails struct {
	ports.SiteRepo
	err error
}

func (s savingFails) Save(context.Context, *site.Site) error { return s.err }

func TestHeartbeatReportsListingFailure(t *testing.T) {
	f := newHeartbeatFixture(t)
	boom := errors.New("db down")
	f.beat.Due = failingSource{err: boom}

	if _, err := f.beat.Execute(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("want the listing failure, got %v", err)
	}
}

func TestHeartbeatLosingTheVersionGuardIsASkipNotAnError(t *testing.T) {
	f := newHeartbeatFixture(t)
	f.declare(t, "WH1", true, true)
	f.beat.Sites = savingFails{SiteRepo: f.sites, err: ports.ErrConcurrentModification}

	res, err := f.beat.Execute(context.Background())
	if err != nil || res.Published != 0 || res.Skipped != 1 {
		t.Fatalf("res=%+v err=%v, want one skip and no error", res, err)
	}
	if n := len(f.capabilityEvents()); n != 1 {
		t.Fatalf("a lost race publishes nothing, got %d events", n)
	}
}

func TestHeartbeatOneSiteFailingDoesNotStopTheOthers(t *testing.T) {
	f := newHeartbeatFixture(t)
	f.declare(t, "WH1", true, true)
	f.declare(t, "WH2", true, true)
	boom := errors.New("write failed")
	f.beat.Sites = failsFor{SiteRepo: f.sites, code: "WH1", err: boom}

	res, err := f.beat.Execute(context.Background())
	if !errors.Is(err, boom) || res.Published != 1 {
		t.Fatalf("res=%+v err=%v, want WH2 published and WH1's failure reported", res, err)
	}
}

type failsFor struct {
	ports.SiteRepo
	code string
	err  error
}

func (s failsFor) Save(ctx context.Context, x *site.Site) error {
	if x.Code() == s.code {
		return s.err
	}
	return s.SiteRepo.Save(ctx, x)
}
