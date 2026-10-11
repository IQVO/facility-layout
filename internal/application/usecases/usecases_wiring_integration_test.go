//go:build integration

// Package usecases_test proves the facility-layout write use cases against a
// REAL Postgres (testcontainers): the real repos, the real UnitOfWork, and a
// buffering publisher, wired exactly like the composition root in
// cmd/facility. These are integration tests in the fleet's sense: they
// execute the real cross-component contracts (the Site -> Zone -> Aisle ->
// Slot chain of custody, placement-rule enforcement, decommission
// finality, and the atomic Publish-inside-UoW bracket) against real
// infrastructure, with no in-memory repo fakes anywhere in the path.
//
// The package boots its own throwaway Postgres via testcontainers in
// TestMain: one container for the whole package, migrated once into a
// template database, one private database per test. Never an external
// DATABASE_URL, never t.Skip.
package usecases_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// One Postgres container serves the whole package (containers are slow to
// boot). TestMain starts it, applies every migration ONCE into a template
// database, and each test then gets its own database cloned from that
// template (CREATE DATABASE ... TEMPLATE, a file-level copy: milliseconds).
// Isolation is therefore total — no TRUNCATE bookkeeping, no dependence on
// test order, and tests that assert on published state still start pristine.
//
// Never an external DATABASE_URL, never t.Skip.
const templateDB = "usecases_migrated_template"

var (
	sharedBaseURL string // connection URL of the container's default database
	dbSeq         atomic.Uint64
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("facility_usecases"),
		tcpostgres.WithUsername("facility"),
		tcpostgres.WithPassword("facility"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start postgres container: %v\n", err)
		return 1
	}
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			fmt.Fprintf(os.Stderr, "terminate postgres container: %v\n", err)
		}
	}()

	sharedBaseURL, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres connection string: %v\n", err)
		return 1
	}

	// Migrate a template database once; every test clones it.
	if err := createDatabase(ctx, templateDB); err != nil {
		fmt.Fprintf(os.Stderr, "create template database: %v\n", err)
		return 1
	}
	if err := postgres.RunMigrations(withDB(sharedBaseURL, templateDB), itcovMigrationsDir()); err != nil {
		fmt.Fprintf(os.Stderr, "migrate template: %v\n", err)
		return 1
	}

	return m.Run()
}

// itcovMigrationsDir resolves /migrations relative to this test file, so the
// harness works regardless of the working directory `go test` runs from
// (same technique as internal/adapters/outbound/postgres's integration
// harness).
func itcovMigrationsDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("unable to resolve test file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "migrations")
}

// withDB rewrites the path of a connection URL to the named database.
func withDB(baseURL, name string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		panic(err)
	}
	u.Path = "/" + name
	return u.String()
}

// createDatabase creates an empty database inside the shared container.
func createDatabase(ctx context.Context, name string) error {
	conn, err := pgx.Connect(ctx, sharedBaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", name)); err != nil {
		return fmt.Errorf("create database %s: %w", name, err)
	}
	return nil
}

// migratedDB hands the test a connection URL to its own private database,
// cloned from the migrated template. Cloning is a file-level copy, so it
// costs milliseconds and the test's writes never leak into another test.
func migratedDB(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("usecases_%d", dbSeq.Add(1))
	conn, err := pgx.Connect(context.Background(), sharedBaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), fmt.Sprintf(
		"CREATE DATABASE %q WITH TEMPLATE %q", name, templateDB)); err != nil {
		t.Fatalf("clone database: %v", err)
	}
	return withDB(sharedBaseURL, name)
}

// wiredUsecases is the real adapter stack over a private migrated database,
// wired exactly like cmd/facility's composition root: real Postgres repos,
// the real UnitOfWork bracketing every Save + Publish (ADR-0018), and a
// buffering publisher the tests assert on. No in-memory repo fakes.
type wiredUsecases struct {
	registerSite         *usecases.RegisterSite
	registerZone         *usecases.RegisterZone
	registerAisle        *usecases.RegisterAisle
	registerLocationType *usecases.RegisterLocationType
	defineRule           *usecases.DefinePlacementRule
	registerSlot         *usecases.RegisterLocationSlot
	decommissionSlot     *usecases.DecommissionLocationSlot
	getSlot              *usecases.GetLocationSlot
	getSiteLayout        *usecases.GetSiteLayout
	getZoneGrid          *usecases.GetZoneGrid
	publisher            *events.BufferedPublisher
}

// newWiredUsecases opens the pool over a fresh private database and wires
// every use case this suite drives.
func newWiredUsecases(t *testing.T) *wiredUsecases {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, migratedDB(t))
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	publisher := events.NewBufferedPublisher()
	clock := memory.NewFixedClock(fixedNow)
	uow := postgres.NewUnitOfWork(pool)

	sites := postgres.NewSiteRepo(pool)
	zones := postgres.NewZoneRepo(pool)
	aisles := postgres.NewAisleRepo(pool)
	slots := postgres.NewSlotRepo(pool)
	locationTypes := postgres.NewLocationTypeRepo(pool)
	rules := postgres.NewPlacementRuleRepo(pool)

	return &wiredUsecases{
		registerSite:         &usecases.RegisterSite{Sites: sites, Events: publisher, Clock: clock, UnitOfWork: uow},
		registerZone:         &usecases.RegisterZone{Sites: sites, Zones: zones, Events: publisher, Clock: clock, UnitOfWork: uow},
		registerAisle:        &usecases.RegisterAisle{Zones: zones, Aisles: aisles, Events: publisher, Clock: clock, UnitOfWork: uow},
		registerLocationType: &usecases.RegisterLocationType{LocationTypes: locationTypes, Events: publisher, Clock: clock, UnitOfWork: uow},
		defineRule:           &usecases.DefinePlacementRule{LocationTypes: locationTypes, Rules: rules, Events: publisher, Clock: clock, UnitOfWork: uow},
		registerSlot: &usecases.RegisterLocationSlot{
			Sites: sites, Zones: zones, Aisles: aisles, Slots: slots,
			LocationTypes: locationTypes, Rules: rules, Events: publisher, Clock: clock,
			UnitOfWork: uow,
		},
		decommissionSlot: &usecases.DecommissionLocationSlot{Slots: slots, Events: publisher, Clock: clock, UnitOfWork: uow},
		getSlot:          &usecases.GetLocationSlot{Slots: slots},
		getSiteLayout:    &usecases.GetSiteLayout{Sites: sites, Zones: zones, Aisles: aisles, Slots: slots},
		getZoneGrid:      &usecases.GetZoneGrid{Zones: zones, Aisles: aisles, Slots: slots},
		publisher:        publisher,
	}
}

// seedStorageChain registers the canonical WH1 / STOR / AMB / A07 structure
// plus a PalletRack location type through the REAL write use cases — the
// setup the slot-registration tests need.
func (w *wiredUsecases) seedStorageChain(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := w.registerSite.Execute(ctx, "WH1", "Fulfilment Centre One"); err != nil {
		t.Fatalf("seed site: %v", err)
	}
	if _, err := w.registerLocationType.Execute(ctx, placement.PalletRack, placement.Storage, mustCapacity(t, 1200, 2.4)); err != nil {
		t.Fatalf("seed location type: %v", err)
	}
	if _, err := w.registerZone.Execute(ctx, "WH1", "STOR", "AMB", shared.Ambient, false, nil, nil); err != nil {
		t.Fatalf("seed zone: %v", err)
	}
	if _, err := w.registerAisle.Execute(ctx, "WH1-STOR-AMB", "A07", 7, shared.TwoWay); err != nil {
		t.Fatalf("seed aisle: %v", err)
	}
}

// assertPublished fails unless an event with the given name was published.
func (w *wiredUsecases) assertPublished(t *testing.T, name string) {
	t.Helper()
	for _, e := range w.publisher.Events() {
		if e.EventName() == name {
			return
		}
	}
	t.Fatalf("expected domain event %q to be published, got %v", name, w.publishedNames())
}

// assertNotPublished fails when an event with the given name was published.
func (w *wiredUsecases) assertNotPublished(t *testing.T, name string) {
	t.Helper()
	for _, e := range w.publisher.Events() {
		if e.EventName() == name {
			t.Fatalf("expected domain event %q NOT to be published, got %v", name, w.publishedNames())
		}
	}
}

// publishedNames lists every published event's name.
func (w *wiredUsecases) publishedNames() []string {
	published := w.publisher.Events()
	names := make([]string, 0, len(published))
	for _, e := range published {
		names = append(names, e.EventName())
	}
	return names
}

func TestUsecases_WarehouseMapLifecycleRoundTrip(t *testing.T) {
	w := newWiredUsecases(t)
	ctx := context.Background()

	// Register the whole chain of custody through the real write use cases.
	w.seedStorageChain(t)
	if _, err := w.registerSlot.Execute(ctx, mustCode(t, "WH1-STOR-AMB-A07-01-01-A"), placement.PalletRack, shared.Capacity{}, "", nil); err != nil {
		t.Fatalf("register slot: %v", err)
	}

	// A duplicate site code is rejected, not silently upserted.
	if _, err := w.registerSite.Execute(ctx, "WH1", "Fulfilment Centre One"); !errors.Is(err, usecases.ErrDuplicateSite) {
		t.Fatalf("duplicate site code must be rejected with ErrDuplicateSite, got %v", err)
	}

	// Persisted state: the slot round-trips through the real repo with the
	// location type's default capacity envelope applied.
	persisted, err := w.getSlot.Execute(ctx, mustCode(t, "WH1-STOR-AMB-A07-01-01-A"))
	if err != nil {
		t.Fatalf("read slot back: %v", err)
	}
	if !persisted.IsActive() || persisted.LocationType() != placement.PalletRack {
		t.Fatalf("slot did not round-trip: %+v", persisted)
	}
	if persisted.Capacity().MaxWeightKg() != 1200 {
		t.Fatalf("expected the PalletRack default capacity (1200kg), got %+v", persisted.Capacity())
	}

	// Read model: the drawable site layout projection sees the whole chain.
	layout, err := w.getSiteLayout.Execute(ctx, "WH1")
	if err != nil {
		t.Fatalf("site layout: %v", err)
	}
	if len(layout.Zones) != 1 || layout.Zones[0].Zone.ID() != "WH1-STOR-AMB" {
		t.Fatalf("layout must expose zone WH1-STOR-AMB, got %+v", layout.Zones)
	}
	zone := layout.Zones[0]
	if len(zone.Aisles) != 1 || zone.Aisles[0].Aisle.AisleCode() != "A07" {
		t.Fatalf("layout must expose aisle A07, got %+v", zone.Aisles)
	}
	if len(zone.Aisles[0].Slots) != 1 || zone.Aisles[0].Slots[0].Code().String() != "WH1-STOR-AMB-A07-01-01-A" {
		t.Fatalf("layout must expose the registered slot, got %+v", zone.Aisles[0].Slots)
	}

	// Read model: the zone grid matrix places the slot at (A07, bay 01, level 01).
	grid, err := w.getZoneGrid.Execute(ctx, "WH1-STOR-AMB")
	if err != nil {
		t.Fatalf("zone grid: %v", err)
	}
	if grid.Zone.ID() != "WH1-STOR-AMB" || len(grid.Columns) != 1 || grid.Columns[0].Bay != "01" {
		t.Fatalf("grid shape wrong: %+v", grid)
	}
	if len(grid.Rows) != 1 || len(grid.Rows[0].Cells) != 1 || len(grid.Rows[0].Cells[0].Slots) != 1 {
		t.Fatalf("grid must have one cell holding the slot: %+v", grid.Rows)
	}

	// Every registration published its event, and the first carries the
	// deterministic clock and the context's CloudEvents type namespace.
	for _, name := range []string{
		"SiteRegistered", "LocationTypeRegistered", "ZoneRegistered",
		"AisleRegistered", "LocationSlotRegistered",
	} {
		w.assertPublished(t, name)
	}
	published := w.publisher.Events()
	first := published[0]
	if first.EventType() != "com.warehouse.wms.facility-layout.site.SiteRegistered" {
		t.Fatalf("unexpected wire type %q", first.EventType())
	}
	if !first.OccurredAt().Equal(fixedNow) {
		t.Fatalf("events must carry the injected clock's time, got %v want %v", first.OccurredAt(), fixedNow)
	}
}

func TestUsecases_PlacementRuleGovernsSlotRegistration(t *testing.T) {
	w := newWiredUsecases(t)
	ctx := context.Background()
	w.seedStorageChain(t)

	// A Deny rule on PalletRack in zones whose code is AMB.
	if _, err := w.defineRule.Execute(ctx, "RULE-ITCOV-NO-RACK", placement.PalletRack, placement.Deny, mustPredicate(t, "AMB", "", nil)); err != nil {
		t.Fatalf("define rule: %v", err)
	}
	w.assertPublished(t, "PlacementRuleDefined")

	// A PalletRack slot in that zone is refused by the rule...
	if _, err := w.registerSlot.Execute(ctx, mustCode(t, "WH1-STOR-AMB-A07-01-01-A"), placement.PalletRack, shared.Capacity{}, "", nil); !errors.Is(err, placement.ErrPlacementRuleViolated) {
		t.Fatalf("rule-violating registration must be refused with ErrPlacementRuleViolated, got %v", err)
	}
	// ...nothing was persisted, and no LocationSlotRegistered fired.
	if _, err := w.getSlot.Execute(ctx, mustCode(t, "WH1-STOR-AMB-A07-01-01-A")); !errors.Is(err, usecases.ErrLocationSlotNotFound) {
		t.Fatalf("a refused registration must leave no slot behind, got %v", err)
	}
	w.assertNotPublished(t, "LocationSlotRegistered")

	// The rule governs one location type only: a Shelf slot registers fine.
	if _, err := w.registerLocationType.Execute(ctx, placement.Shelf, placement.Storage, mustCapacity(t, 60, 0.4)); err != nil {
		t.Fatalf("seed shelf type: %v", err)
	}
	if _, err := w.registerSlot.Execute(ctx, mustCode(t, "WH1-STOR-AMB-A07-01-02-A"), placement.Shelf, shared.Capacity{}, "", nil); err != nil {
		t.Fatalf("shelf registration must pass the rule set: %v", err)
	}
	w.assertPublished(t, "LocationSlotRegistered")
}

func TestUsecases_DecommissionSlotLifecycle(t *testing.T) {
	w := newWiredUsecases(t)
	ctx := context.Background()
	w.seedStorageChain(t)

	code := mustCode(t, "WH1-STOR-AMB-A07-02-01-A")
	if _, err := w.registerSlot.Execute(ctx, code, placement.PalletRack, shared.Capacity{}, "", nil); err != nil {
		t.Fatalf("register slot: %v", err)
	}

	// Decommission is the one-way state change of the aggregate lifecycle.
	if err := w.decommissionSlot.Execute(ctx, code); err != nil {
		t.Fatalf("decommission: %v", err)
	}

	// Persisted state: the row survives, no longer Active.
	reloaded, err := w.getSlot.Execute(ctx, code)
	if err != nil {
		t.Fatalf("read slot back after decommission: %v", err)
	}
	if reloaded.IsActive() {
		t.Fatal("expected the persisted slot to be Decommissioned")
	}

	// The event fired with the slot's code in its payload.
	var sawDecommissioned bool
	for _, e := range w.publisher.Events() {
		if ev, ok := e.(shared.LocationSlotDecommissioned); ok && e.EventName() == "LocationSlotDecommissioned" {
			sawDecommissioned = true
			if ev.LocationCode != code.String() {
				t.Fatalf("decommission event carried %q, want %q", ev.LocationCode, code.String())
			}
		}
	}
	if !sawDecommissioned {
		t.Fatalf("expected LocationSlotDecommissioned to be published, got %v", w.publishedNames())
	}

	// Re-registering the code is a duplicate, never a resurrection.
	if _, err := w.registerSlot.Execute(ctx, code, placement.PalletRack, shared.Capacity{}, "", nil); !errors.Is(err, usecases.ErrDuplicateLocationCode) {
		t.Fatalf("re-registering a decommissioned code must be ErrDuplicateLocationCode, got %v", err)
	}
}
