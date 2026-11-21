//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
	"github.com/claudioed/facility-layout/internal/domain/slot"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

// Known-valid GS1 GLNs (see internal/domain/shared/gln_test.go).
const (
	pgGLNSite = "4006381333931"
	pgGLNZone = "5412345000013"
	pgGLNSlot = "4012345000009"
)

// TestPostgresLocationIdentityRoundTrip proves migration 0013 (ADR-0045): the
// optional gln columns on sites, zones and slots and the optional spot_kind on
// slots round-trip, an absent value reads back as none, and the database
// itself refuses a malformed GLN, an unknown spot kind and a spot kind on a
// slot that is not a Yard slot.
func TestPostgresLocationIdentityRoundTrip(t *testing.T) {
	ctx, pool := newPool(t)
	sites, zones := postgres.NewSiteRepo(pool), postgres.NewZoneRepo(pool)
	aisles, types, slots := postgres.NewAisleRepo(pool), postgres.NewLocationTypeRepo(pool), postgres.NewSlotRepo(pool)

	s, _ := site.NewSite("WH1", "Fulfilment Centre One")
	if err := s.AssignGLN(pgGLNSite); err != nil {
		t.Fatal(err)
	}
	plain, _ := site.NewSite("WH2", "No identity")
	z, _ := zone.NewZone("WH1", "YARD", "NORTH", shared.Ambient, false)
	if err := z.AssignGLN(pgGLNZone); err != nil {
		t.Fatal(err)
	}
	for _, save := range []func() error{
		func() error { return sites.Save(ctx, s) },
		func() error { return sites.Save(ctx, plain) },
		func() error { return zones.Save(ctx, z) },
	} {
		if err := save(); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := aisle.NewAisle(z.ID(), "Y01", 1, shared.TwoWay)
	if err := aisles.Save(ctx, a); err != nil {
		t.Fatal(err)
	}
	yardType, err := placement.NewLocationType("YardSpot", placement.Yard, shared.Capacity{})
	if err != nil {
		t.Fatal(err)
	}
	if err := types.Save(ctx, yardType); err != nil {
		t.Fatal(err)
	}
	attrs := placement.ZoneAttributes{ZoneID: z.ID(), ZoneCode: z.ZoneCode(), TemperatureClass: z.TemperatureClass()}
	newYardSlot := func(raw string) *slot.LocationSlot {
		built, err := slot.NewLocationSlot(mustCode(t, raw), yardType, shared.Capacity{}, slot.FunctionalAttributes{}, attrs, nil)
		if err != nil {
			t.Fatal(err)
		}
		return built
	}
	withIdentity := newYardSlot("WH1-YARD-NORTH-Y01-01-01-A")
	if err := withIdentity.AssignGLN(pgGLNSlot); err != nil {
		t.Fatal(err)
	}
	if err := withIdentity.AssignSpotKind(shared.SpotKindReeferPlug); err != nil {
		t.Fatal(err)
	}
	without := newYardSlot("WH1-YARD-NORTH-Y01-01-02-A")
	for _, built := range []*slot.LocationSlot{withIdentity, without} {
		if err := slots.Save(ctx, built); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("site", func(t *testing.T) {
		got, _ := sites.FindByCode(ctx, "WH1")
		none, _ := sites.FindByCode(ctx, "WH2")
		if got.GLN() != pgGLNSite || !none.GLN().IsZero() {
			t.Fatalf("GLN = %q / %q", got.GLN(), none.GLN())
		}
		list, err := sites.List(ctx)
		if err != nil || list[0].GLN() != pgGLNSite {
			t.Fatalf("List: %v %v", err, list)
		}
	})
	t.Run("zone", func(t *testing.T) {
		got, err := zones.FindByID(ctx, z.ID())
		if err != nil || got.GLN() != pgGLNZone {
			t.Fatalf("FindByID gln = %q, err %v", got.GLN(), err)
		}
		list, err := zones.ListBySite(ctx, "WH1")
		if err != nil || len(list) != 1 || list[0].GLN() != pgGLNZone {
			t.Fatalf("ListBySite: %v %v", err, list)
		}
	})
	t.Run("slot", func(t *testing.T) {
		got, _ := slots.FindByCode(ctx, withIdentity.Code())
		none, _ := slots.FindByCode(ctx, without.Code())
		if got.GLN() != pgGLNSlot || got.SpotKind() != shared.SpotKindReeferPlug {
			t.Fatalf("identity = %q / %q", got.GLN(), got.SpotKind())
		}
		if !none.GLN().IsZero() || none.SpotKind() != "" {
			t.Fatalf("a slot without identity reads %q / %q", none.GLN(), none.SpotKind())
		}
		// A later save (version bump) must keep the identity.
		if err := slots.Save(ctx, got); err != nil {
			t.Fatal(err)
		}
		again, _ := slots.FindByCode(ctx, withIdentity.Code())
		if again.GLN() != pgGLNSlot || again.SpotKind() != shared.SpotKindReeferPlug {
			t.Fatalf("identity lost on re-save: %q / %q", again.GLN(), again.SpotKind())
		}
	})
	t.Run("CHECK constraints", func(t *testing.T) {
		code := withIdentity.Code().String()
		for name, stmt := range map[string]string{
			"malformed gln on a slot":      `UPDATE location_slots SET gln = '12345' WHERE code = $1`,
			"unknown spot kind":            `UPDATE location_slots SET spot_kind = 'DOCK' WHERE code = $1`,
			"spot kind on a non-yard slot": `UPDATE location_slots SET role = 'Dock', dock_flow = 'Inbound' WHERE code = $1`,
		} {
			if _, err := pool.Exec(ctx, stmt, code); err == nil {
				t.Errorf("%s: expected the database to refuse it", name)
			}
		}
		if _, err := pool.Exec(ctx, `UPDATE sites SET gln = 'ABCDEFGHIJKLM' WHERE code = 'WH1'`); err == nil {
			t.Error("malformed gln on a site: expected the database to refuse it")
		}
		if _, err := pool.Exec(ctx, `UPDATE zones SET gln = '1' WHERE id = $1`, z.ID()); err == nil {
			t.Error("malformed gln on a zone: expected the database to refuse it")
		}
	})
}

// TestPostgresGLNRegistry proves the uniqueness mechanism of ADR-0045 against a
// live database: one GLN, one owner; a repeat claim by the owner is a no-op;
// a rolled-back unit of work releases its claim; and two concurrent claims of
// the same number cannot both win.
func TestPostgresGLNRegistry(t *testing.T) {
	ctx, pool := newPool(t)
	registry := postgres.NewGLNRegistry(pool)
	uow := postgres.NewUnitOfWork(pool)

	t.Run("a different owner cannot take a claimed GLN, the same owner can repeat it", func(t *testing.T) {
		if ok, err := registry.Claim(ctx, pgGLNSite, "site:WH1"); err != nil || !ok {
			t.Fatalf("first claim = %v, %v", ok, err)
		}
		if ok, err := registry.Claim(ctx, pgGLNSite, "slot:WH1-A"); err != nil || ok {
			t.Fatalf("a second owner claimed it: %v, %v", ok, err)
		}
		if ok, err := registry.Claim(ctx, pgGLNSite, "site:WH1"); err != nil || !ok {
			t.Fatalf("the owner's repeat claim = %v, %v", ok, err)
		}
	})

	t.Run("a location holds at most one GLN", func(t *testing.T) {
		if _, err := registry.Claim(ctx, pgGLNZone, "site:WH1"); err == nil {
			t.Fatal("expected the unique owner constraint to refuse a second GLN for the same owner")
		}
	})

	t.Run("a rolled-back unit of work releases the claim", func(t *testing.T) {
		boom := errors.New("boom")
		err := uow.Execute(ctx, func(ctx context.Context) error {
			if ok, err := registry.Claim(ctx, pgGLNSlot, "slot:WH1-B"); err != nil || !ok {
				t.Fatalf("claim inside the unit of work = %v, %v", ok, err)
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
		if ok, err := registry.Claim(ctx, pgGLNSlot, "slot:WH1-C"); err != nil || !ok {
			t.Fatalf("the released GLN could not be claimed: %v, %v", ok, err)
		}
	})

	t.Run("two concurrent claims of one GLN: exactly one wins", func(t *testing.T) {
		const gln = shared.GLN("9780201379624")
		var wg sync.WaitGroup
		results := make([]bool, 8)
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				owner := "slot:RACE-" + string(rune('A'+i))
				err := uow.Execute(ctx, func(ctx context.Context) error {
					ok, err := registry.Claim(ctx, gln, owner)
					results[i] = ok
					return err
				})
				if err != nil {
					t.Errorf("claimer %d: %v", i, err)
				}
			}()
		}
		wg.Wait()
		wins := 0
		for _, won := range results {
			if won {
				wins++
			}
		}
		if wins != 1 {
			t.Fatalf("%d claimers won, want exactly 1 (%v)", wins, results)
		}
	})
}

// TestRegisterSiteWithGLNThroughTheOutboxOnPostgres runs the real use case over
// the real adapters: the GLN is stored, announced on SiteRegistered through the
// outbox, and a second site claiming the same GLN is refused with nothing
// saved and nothing queued (the unit of work rolled back, claim included).
func TestRegisterSiteWithGLNThroughTheOutboxOnPostgres(t *testing.T) {
	pool := outboxDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	newID := func() string { return uniqueID() }
	sites := postgres.NewSiteRepo(pool)
	uc := &usecases.RegisterSite{
		Sites:      sites,
		Events:     postgres.NewOutboxPublisher(pool, newID, &outboundkafka.Publisher{NewId: newID}),
		Clock:      fixedClock{t: now},
		UnitOfWork: postgres.NewUnitOfWork(pool),
		GLNs:       postgres.NewGLNRegistry(pool),
	}

	if _, err := uc.ExecuteWithGLN(ctx, "WH1", "One", site.RoleUnspecified, pgGLNSite); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if _, err := uc.ExecuteWithGLN(ctx, "WH2", "Two", site.RoleUnspecified, pgGLNSite); !errors.Is(err, usecases.ErrGLNAlreadyAssigned) {
		t.Fatalf("second registration err = %v, want ErrGLNAlreadyAssigned", err)
	}
	if found, _ := sites.FindByCode(ctx, "WH2"); found != nil {
		t.Fatal("WH2 was saved despite the GLN conflict")
	}
	if got := countOutbox(t, pool, "TRUE"); got != 1 {
		t.Fatalf("outbox rows = %d, want 1 (the refused registration must queue nothing)", got)
	}
	// A site without a GLN is as valid as before and queues the old shape.
	if _, err := uc.Execute(ctx, "WH3", "Three", site.RoleUnspecified); err != nil {
		t.Fatalf("registration without a GLN: %v", err)
	}

	sink := &recordingSink{}
	if _, err := postgres.NewOutboxRelay(pool, sink, slog.Default()).RelayOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sink.sent) != 2 {
		t.Fatalf("relayed %d events, want 2", len(sink.sent))
	}
	var withGLN, withoutGLN struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(sink.sent[0].Value, &withGLN); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(sink.sent[1].Value, &withoutGLN); err != nil {
		t.Fatal(err)
	}
	if withGLN.Data["gln"] != pgGLNSite {
		t.Fatalf("SiteRegistered gln = %v", withGLN.Data["gln"])
	}
	if _, present := withoutGLN.Data["gln"]; present {
		t.Fatalf("a site without a GLN must omit it on the wire: %v", withoutGLN.Data)
	}
}
