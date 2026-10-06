//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/slot"
)

// seedVersionedSlotRow inserts the parent structure and one Active slot,
// returning the slot loaded through the repo (so it carries the row's
// version, ADR-0025).
func seedVersionedSlotRow(t *testing.T, ctx context.Context, pool *pgxPool) *slot.LocationSlot {
	t.Helper()
	code := mustCode(t, "WH1-STOR-AMB-A07-01-01-A")
	if _, err := pool.Exec(ctx, `INSERT INTO sites (code, name, status) VALUES ($1, $2, $3)`, "WH1", "Fulfilment Centre One", "Active"); err != nil {
		t.Fatalf("seed site: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO zones (id, site_code, area_code, zone_code, temperature_class, hazmat, status) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		"WH1-STOR-AMB", "WH1", "STOR", "AMB", "Ambient", false, "Active"); err != nil {
		t.Fatalf("seed zone: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO aisles (id, zone_id, aisle_code, sequence_hint, direction, status) VALUES ($1,$2,$3,$4,$5,$6)`,
		"WH1-STOR-AMB-A07", "WH1-STOR-AMB", "A07", 7, "TwoWay", "Active"); err != nil {
		t.Fatalf("seed aisle: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO location_types (name, role, default_max_weight_kg, default_max_volume_m3) VALUES ($1,$2,$3,$4)`,
		"PalletRack", "Storage", 1200, 2.4); err != nil {
		t.Fatalf("seed location type: %v", err)
	}

	storageType, err := placement.NewLocationType("PalletRack", placement.Storage, mustCapacity(t, 1200, 2.4))
	if err != nil {
		t.Fatalf("build location type: %v", err)
	}
	fresh, err := slot.NewLocationSlot(code, storageType, shared.Capacity{}, slot.FunctionalAttributes{},
		placement.ZoneAttributes{ZoneID: code.ZoneID(), TemperatureClass: shared.Ambient}, placement.RuleSet{})
	if err != nil {
		t.Fatalf("build slot: %v", err)
	}
	repo := postgres.NewSlotRepo(pool)
	if err := repo.Save(ctx, fresh); err != nil {
		t.Fatalf("seed slot: %v", err)
	}
	loaded, err := repo.FindByCode(ctx, code)
	if err != nil || loaded == nil {
		t.Fatalf("reload seeded slot: %v %v", loaded, err)
	}
	return loaded
}

// TestSlotRepo_Save_StaleVersionFails is the ADR-0025 contract against a
// real Postgres: two sequential loads of the same row, the first Save
// succeeds, the second (stale) Save returns ports.ErrConcurrentModification
// and its change is verified absent on reload — the audit's exact race
// (a concurrent SetLocationGeometry writing status=Active back over a
// decommission) can no longer happen.
func TestSlotRepo_Save_StaleVersionFails(t *testing.T) {
	pool := outboxDB(t)
	ctx := context.Background()
	repo := postgres.NewSlotRepo(pool)

	first := seedVersionedSlotRow(t, ctx, pool)
	stale, err := repo.FindByCode(ctx, first.Code())
	if err != nil {
		t.Fatalf("second load: %v", err)
	}

	// Writer 1 decommissions; writer 2 (holding the stale copy) sets
	// geometry. Writer 1 commits first...
	if err := first.Decommission(); err != nil {
		t.Fatalf("decommission: %v", err)
	}
	if err := repo.Save(ctx, first); err != nil {
		t.Fatalf("first Save must succeed: %v", err)
	}

	position, dim := mustGeometry(t)
	if err := stale.SetGeometry(position, dim); err != nil {
		t.Fatalf("set geometry: %v", err)
	}
	if err := repo.Save(ctx, stale); !errors.Is(err, ports.ErrConcurrentModification) {
		t.Fatalf("stale Save = %v, want ports.ErrConcurrentModification", err)
	}

	// The stale write must NOT have resurrected the slot.
	reloaded, err := repo.FindByCode(ctx, first.Code())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status() != shared.Decommissioned {
		t.Fatalf("status = %q, want Decommissioned — the stale geometry write resurrected the slot", reloaded.Status())
	}
	if !reloaded.Position().IsZero() {
		t.Fatalf("stale geometry must be absent, got %+v", reloaded.Position())
	}
}

// TestSlotRepo_Save_CurrentVersionSucceedsAndIncrements: a Save at the
// current version succeeds and the row's version advances by exactly one.
func TestSlotRepo_Save_CurrentVersionSucceedsAndIncrements(t *testing.T) {
	pool := outboxDB(t)
	ctx := context.Background()
	repo := postgres.NewSlotRepo(pool)

	s := seedVersionedSlotRow(t, ctx, pool)
	if s.Version() != 1 {
		t.Fatalf("seeded version = %d, want 1", s.Version())
	}
	position, dim := mustGeometry(t)
	if err := s.SetGeometry(position, dim); err != nil {
		t.Fatalf("set geometry: %v", err)
	}
	if err := repo.Save(ctx, s); err != nil {
		t.Fatalf("Save at current version: %v", err)
	}
	reloaded, err := repo.FindByCode(ctx, s.Code())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Version() != 2 {
		t.Fatalf("version after one Save = %d, want 2", reloaded.Version())
	}
	if reloaded.Position().IsZero() {
		t.Fatal("geometry must be visible after the successful Save")
	}
}

// TestSlotRepo_ConcurrentSaves_ExactlyOneWinner is the real proof of
// ADR-0025: two goroutines each load the SAME row, mutate independently,
// and Save concurrently (synchronized start via a channel so both genuinely
// race). Exactly one succeeds, the other gets ports.ErrConcurrentModification,
// the row's version advances by exactly one (never two), and exactly one of
// the two mutations is visible on reload — never both, never neither.
func TestSlotRepo_ConcurrentSaves_ExactlyOneWinner(t *testing.T) {
	pool := outboxDB(t)
	ctx := context.Background()
	repo := postgres.NewSlotRepo(pool)

	seed := seedVersionedSlotRow(t, ctx, pool)
	code := seed.Code()
	position, dim := mustGeometry(t)

	first, err := repo.FindByCode(ctx, code)
	if err != nil {
		t.Fatalf("load 1: %v", err)
	}
	second, err := repo.FindByCode(ctx, code)
	if err != nil {
		t.Fatalf("load 2: %v", err)
	}

	if err := first.Decommission(); err != nil {
		t.Fatalf("decommission: %v", err)
	}
	if err := second.SetGeometry(position, dim); err != nil {
		t.Fatalf("set geometry: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var successes, conflicts int
	save := func(s *slot.LocationSlot) {
		defer wg.Done()
		<-start
		err := repo.Save(ctx, s)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ports.ErrConcurrentModification):
			conflicts++
		default:
			t.Errorf("unexpected Save error: %v", err)
		}
	}
	wg.Add(2)
	go save(first)
	go save(second)
	close(start)
	wg.Wait()

	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want exactly one winner and one conflict", successes, conflicts)
	}

	reloaded, err := repo.FindByCode(ctx, code)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Version() != 2 {
		t.Fatalf("version after the race = %d, want exactly 2 (one advance)", reloaded.Version())
	}
	decommissioned := reloaded.Status() == shared.Decommissioned
	hasGeometry := !reloaded.Position().IsZero()
	if decommissioned == hasGeometry {
		t.Fatalf("exactly one mutation must be visible; decommissioned=%v hasGeometry=%v", decommissioned, hasGeometry)
	}
}

func mustGeometry(t *testing.T) (shared.Point3D, shared.Dimensions) {
	t.Helper()
	position, err := shared.NewPoint3D(1, 2, 0)
	if err != nil {
		t.Fatalf("point: %v", err)
	}
	dim, err := shared.NewDimensions(1.2, 0.9, 2.4)
	if err != nil {
		t.Fatalf("dimensions: %v", err)
	}
	return position, dim
}
