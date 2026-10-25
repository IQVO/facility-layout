//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/facility-layout/internal/adapters/inbound/heartbeat"
	"github.com/claudioed/facility-layout/internal/adapters/kafka/cloudevents"
	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

const beatInterval = 5 * time.Minute

var beatEpoch = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

// beatClock is a settable clock shared by the use cases of one test.
type beatClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *beatClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *beatClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type beatRig struct {
	pool   *pgxpool.Pool
	sites  *postgres.SiteRepo
	clock  *beatClock
	change *usecases.ChangeSiteTransferCapability
}

func (r beatRig) newBeat() *usecases.PublishSiteCapabilityHeartbeats {
	newID := uniqueID
	return &usecases.PublishSiteCapabilityHeartbeats{
		Sites: r.sites, Due: r.sites, Clock: r.clock, Interval: beatInterval,
		Events:     postgres.NewOutboxPublisher(r.pool, newID, &outboundkafka.Publisher{NewId: newID}),
		UnitOfWork: postgres.NewUnitOfWork(r.pool),
	}
}

func newBeatRig(t *testing.T) beatRig {
	t.Helper()
	pool := outboxDB(t)
	sites := postgres.NewSiteRepo(pool)
	clock := &beatClock{t: beatEpoch}
	newID := uniqueID
	return beatRig{pool: pool, sites: sites, clock: clock, change: &usecases.ChangeSiteTransferCapability{
		Sites: sites, Clock: clock, UnitOfWork: postgres.NewUnitOfWork(pool),
		Events: postgres.NewOutboxPublisher(pool, newID, &outboundkafka.Publisher{NewId: newID}),
	}}
}

func (r beatRig) declare(t *testing.T, code string, origin, destination bool) {
	t.Helper()
	registerSiteForCapability(t, context.Background(), r.sites, code)
	if _, err := r.change.Execute(context.Background(), code, site.NewTransferCapability(origin, destination)); err != nil {
		t.Fatalf("declare %s: %v", code, err)
	}
}

// capabilityEvents decodes, oldest first, every SiteCapabilityChanged CloudEvent
// in the integration-topic outbox rows of one site.
func (r beatRig) capabilityEvents(t *testing.T, code string) []ce.Event {
	t.Helper()
	rows, err := r.pool.Query(context.Background(),
		`SELECT value FROM outbox_events WHERE topic = 'warehouse.facility.events' AND event_type LIKE '%SiteCapabilityChanged' AND key = $1 ORDER BY id`, []byte(code))
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()
	var out []ce.Event
	for rows.Next() {
		var value []byte
		if err := rows.Scan(&value); err != nil {
			t.Fatalf("scan: %v", err)
		}
		evt, err := cloudevents.Decode(value)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		out = append(out, evt)
	}
	return out
}

func nipData(t *testing.T, e ce.Event) nipSiteCapabilityChangedData {
	t.Helper()
	var d nipSiteCapabilityChangedData
	if err := json.Unmarshal(e.Data(), &d); err != nil {
		t.Fatalf("NIP cannot decode: %v", err)
	}
	return d
}

// nipFact mirrors network-inventory-planning's site_capability row and its
// SiteCapabilityRepo.Upsert (hand-copied, never imported): a snapshot
// replaces the stored row, as_of included, ONLY when its
// capability_revision is strictly greater; a CloudEvents id seen before is
// skipped by its processed_events claim.
type nipFact struct {
	revision int64
	asOf     time.Time
	seen     map[string]bool
}

func (f *nipFact) apply(t *testing.T, e ce.Event) {
	t.Helper()
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[e.ID()] {
		return
	}
	f.seen[e.ID()] = true
	if d := nipData(t, e); d.CapabilityRevision > f.revision {
		f.revision, f.asOf = d.CapabilityRevision, e.Time().UTC()
	}
}

// TestHeartbeat_WritesAFreshEventNIPAcceptsAndThatRefreshesItsWatermark is the
// ADR-0041 proof on a real Postgres: the heartbeat's outbox row is a
// well-formed SiteCapabilityChanged with the CURRENT values, a new CloudEvents
// id, the site as key and subject, a fresh `time`, and a revision NIP's
// last-write-wins rule accepts, so NIP's as_of moves forward.
func TestHeartbeat_WritesAFreshEventNIPAcceptsAndThatRefreshesItsWatermark(t *testing.T) {
	r := newBeatRig(t)
	ctx := context.Background()
	r.declare(t, "WH1", true, false)
	r.clock.Advance(7 * time.Minute)

	res, err := r.newBeat().Execute(ctx)
	if err != nil || res.Published != 1 {
		t.Fatalf("heartbeat: res=%+v err=%v", res, err)
	}

	evts := r.capabilityEvents(t, "WH1")
	if len(evts) != 2 {
		t.Fatalf("want the declaration and one heartbeat, got %d events", len(evts))
	}
	declared, beat := evts[0], evts[1]
	if beat.ID() == declared.ID() || beat.Subject() != "WH1" ||
		beat.Type() != "com.warehouse.wms.facility-layout.site.SiteCapabilityChanged" ||
		beat.DataSchema() != "urn:warehouse:facility-layout:events:SiteCapabilityChanged:v1" ||
		!beat.Time().Equal(beatEpoch.Add(7*time.Minute)) {
		t.Fatalf("heartbeat CloudEvent: %s", beat.String())
	}
	if got := nipData(t, beat); got != (nipSiteCapabilityChangedData{SiteCode: "WH1", TransferOriginEnabled: true, CapabilityRevision: 2}) {
		t.Fatalf("heartbeat payload = %+v", got)
	}

	var nip nipFact
	nip.apply(t, declared)
	nip.apply(t, beat)
	if nip.revision != 2 || !nip.asOf.Equal(beatEpoch.Add(7*time.Minute)) {
		t.Fatalf("NIP did not refresh its watermark: revision=%d as_of=%v", nip.revision, nip.asOf)
	}
}

// TestNIPIgnoresASameRevisionReEmission documents WHY the heartbeat advances
// the revision (ADR-0041): an event identical to the declaration, with a new
// CloudEvents id and a newer time but the SAME capability_revision, is
// claimed by NIP and then dropped by its strictly-greater guard, so as_of
// stays stale.
func TestNIPIgnoresASameRevisionReEmission(t *testing.T) {
	r := newBeatRig(t)
	r.declare(t, "WH1", true, true)
	declared := r.capabilityEvents(t, "WH1")[0]

	replay := declared.Clone()
	replay.SetID("a-new-cloudevents-id")
	replay.SetTime(declared.Time().Add(7 * time.Minute))

	var nip nipFact
	nip.apply(t, declared)
	nip.apply(t, replay)
	if !nip.asOf.Equal(declared.Time().UTC()) {
		t.Fatalf("a same-revision re-emission moved as_of to %v: the premise of ADR-0041 is gone", nip.asOf)
	}
}

func TestHeartbeat_IsDueOncePerIntervalAndPutStaysANoOp(t *testing.T) {
	r := newBeatRig(t)
	ctx := context.Background()
	r.declare(t, "WH1", true, true)
	beat := r.newBeat()

	steps := []struct {
		advance time.Duration
		want    int
	}{{0, 1}, {beatInterval - time.Microsecond, 0}, {time.Microsecond, 1}, {beatInterval - time.Microsecond, 0}}
	for i, s := range steps {
		r.clock.Advance(s.advance)
		res, err := beat.Execute(ctx)
		if err != nil || res.Published != s.want {
			t.Fatalf("step %d: res=%+v err=%v, want %d published", i, res, err, s.want)
		}
	}
	if n := len(r.capabilityEvents(t, "WH1")); n != 3 {
		t.Fatalf("declaration + 2 heartbeats = 3 events, got %d", n)
	}

	got, err := r.change.Execute(ctx, "WH1", site.NewTransferCapability(true, true))
	if err != nil || got.CapabilityRevision() != 3 || len(r.capabilityEvents(t, "WH1")) != 3 {
		t.Fatalf("restating the value must stay a no-op: revision=%d err=%v", got.CapabilityRevision(), err)
	}
	changed, err := r.change.Execute(ctx, "WH1", site.NewTransferCapability(false, true))
	if err != nil || changed.CapabilityRevision() != 4 {
		t.Fatalf("a real change continues after the heartbeats: %v %v", changed, err)
	}
}

// TestHeartbeat_ManyReplicasPublishOneEventPerSitePerInterval runs the pass
// from several "replicas" at once: the heartbeat column plus the ADR-0025
// version guard leave exactly one heartbeat per site.
func TestHeartbeat_ManyReplicasPublishOneEventPerSitePerInterval(t *testing.T) {
	r := newBeatRig(t)
	r.declare(t, "WH1", true, true)
	r.declare(t, "WH2", false, true)

	const replicas = 6
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := r.newBeat().Execute(context.Background()); err != nil {
				t.Errorf("replica pass: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	for _, code := range []string{"WH1", "WH2"} {
		if n := len(r.capabilityEvents(t, code)); n != 2 {
			t.Fatalf("%s: want declaration + exactly one heartbeat, got %d events", code, n)
		}
		s, err := r.sites.FindByCode(context.Background(), code)
		if err != nil || s.CapabilityRevision() != 2 {
			t.Fatalf("%s: revision=%v err=%v, want 2", code, s, err)
		}
	}
}

// TestListCapabilityHeartbeatDue_SQLBoundaries pins the SQL filter: declared
// (revision > 0) and ACTIVE only, a heartbeat exactly notAfter is due and one
// microsecond later is not, ordered by code and bounded by the limit.
func TestListCapabilityHeartbeatDue_SQLBoundaries(t *testing.T) {
	r := newBeatRig(t)
	ctx := context.Background()
	registerSiteForCapability(t, ctx, r.sites, "NEVER") // revision 0: nothing declared
	r.declare(t, "AAA", true, true)                     // never heartbeated
	r.declare(t, "AT", true, true)
	r.declare(t, "AFTER", true, true)
	r.declare(t, "RETIRED", true, true)
	cutoff := beatEpoch
	stamp := func(code string, at time.Time) {
		if _, err := r.pool.Exec(ctx, `UPDATE sites SET capability_heartbeat_at = $2 WHERE code = $1`, code, at); err != nil {
			t.Fatalf("stamp %s: %v", code, err)
		}
	}
	stamp("AT", cutoff)
	stamp("AFTER", cutoff.Add(time.Microsecond))
	stamp("RETIRED", cutoff.Add(-time.Hour))
	if _, err := r.pool.Exec(ctx, `UPDATE sites SET status = $2 WHERE code = $1`, "RETIRED", string(shared.Decommissioned)); err != nil {
		t.Fatalf("retire: %v", err)
	}

	got, err := r.sites.ListCapabilityHeartbeatDue(ctx, cutoff, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if want := []string{"AAA", "AT"}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("due = %v, want %v", got, want)
	}
	limited, err := r.sites.ListCapabilityHeartbeatDue(ctx, cutoff, 1)
	if err != nil || len(limited) != 1 || limited[0] != "AAA" {
		t.Fatalf("limit 1 = %v err=%v, want [AAA]", limited, err)
	}
}

// TestHeartbeatTime_RoundTripsAndSurvivesOtherWriters: the heartbeat time is
// persisted, rehydrated, and left intact when another command (a role
// change) re-saves the site.
func TestHeartbeatTime_RoundTripsAndSurvivesOtherWriters(t *testing.T) {
	r := newBeatRig(t)
	ctx := context.Background()
	r.declare(t, "WH1", true, true)
	if res, err := r.newBeat().Execute(ctx); err != nil || res.Published != 1 {
		t.Fatalf("heartbeat: %+v %v", res, err)
	}
	s, err := r.sites.FindByCode(ctx, "WH1")
	if err != nil || !s.CapabilityHeartbeatAt().Equal(beatEpoch) {
		t.Fatalf("persisted heartbeat time = %v err=%v, want %v", s.CapabilityHeartbeatAt(), err, beatEpoch)
	}

	role := &usecases.ChangeSiteRole{Sites: r.sites, Clock: r.clock, UnitOfWork: postgres.NewUnitOfWork(r.pool),
		Events: postgres.NewOutboxPublisher(r.pool, uniqueID, &outboundkafka.Publisher{NewId: uniqueID})}
	if _, err := role.Execute(ctx, "WH1", site.RoleXLFC); err != nil {
		t.Fatalf("role change: %v", err)
	}
	s, _ = r.sites.FindByCode(ctx, "WH1")
	if !s.CapabilityHeartbeatAt().Equal(beatEpoch) || s.CapabilityRevision() != 2 {
		t.Fatalf("a role change must keep the heartbeat state: at=%v revision=%d", s.CapabilityHeartbeatAt(), s.CapabilityRevision())
	}
}

// TestHeartbeatLoop_RepublishesRepeatedlyOnRealPostgres drives the real loop
// (immediate pass, then ticks) over the real use case and outbox.
func TestHeartbeatLoop_RepublishesRepeatedlyOnRealPostgres(t *testing.T) {
	r := newBeatRig(t)
	r.declare(t, "WH1", true, true)
	const interval = 200 * time.Millisecond
	beat := r.newBeat()
	beat.Interval = interval
	beat.Clock = realClock{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = (&heartbeat.Loop{Publisher: beat, Interval: interval}).Run(ctx)
	}()
	deadline := time.Now().Add(20 * time.Second)
	for len(r.capabilityEvents(t, "WH1")) < 4 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done

	evts := r.capabilityEvents(t, "WH1")
	if len(evts) < 4 {
		t.Fatalf("want the declaration and at least 3 heartbeats, got %d", len(evts))
	}
	for i := 1; i < len(evts); i++ {
		if nipData(t, evts[i]).CapabilityRevision != int64(i+1) {
			t.Fatalf("event %d revision = %d, want %d", i, nipData(t, evts[i]).CapabilityRevision, i+1)
		}
	}
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }
