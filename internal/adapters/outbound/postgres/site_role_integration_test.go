//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/claudioed/facility-layout/internal/adapters/kafka/cloudevents"
	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

// seededSiteNames are site names exactly as the provisioning catalogue and
// the kind cluster carry them before ADR-0037 (one per role phrase), plus
// names the one-time backfill must leave UNSPECIFIED.
var seededSiteNames = map[string]struct {
	name string
	want site.Role
}{
	"BFI4": {"BFI4 - Robotics Sortable Fulfillment Center (Kent, WA)", site.RoleRoboticsSortableFC},
	"ABE8": {"ABE8 - Non-Sortable Fulfillment Center (Breinigsville, PA)", site.RoleNonSortableFC},
	"BOS7": {"BOS7 - Traditional Sortable Fulfillment Center (Fall River, MA)", site.RoleTraditionalSortableFC},
	"AUS3": {"AUS3 - Sub-Same-Day Fulfillment Center (Austin, TX)", site.RoleSubSameDayFC},
	"EWR8": {"EWR8 - Fresh Grocery Fulfillment Center (Robbinsville, NJ)", site.RoleFreshGroceryFC},
	"CHA2": {"CHA2 - XL (Extra-Large) Fulfillment Center (Chattanooga, TN)", site.RoleXLFC},
	"CVG1": {"CVG1 - Soft-Lines Sortable Fulfillment Center (Hebron, KY)", site.RoleSoftLinesSortableFC},
	"JFK5": {"JFK5 - Micro-Fulfillment Center (store-attached) (Brooklyn, NY)", site.RoleMicroFC},
	"SIM1": {"Simulated FC", site.RoleSimulatedFC},
	"IAH3": {"IAH3 - Inbound Cross-Dock (Houston, TX)", site.RoleInboundCrossDock},
	"DFW9": {"DFW9 - National Inbound Cross-Dock (Dallas, TX)", site.RoleNationalInboundCrossDock},
	"CVG9": {"CVG9 - Reverse Logistics / Returns Center (Florence, KY)", site.RoleReturnsCenter},
	"ATL9": {"ATL9 - Sortation Center (Union City, GA)", site.RoleSortationCenter},
	"DAT3": {"DAT3 - Delivery Station (Atlanta, GA)", site.RoleDeliveryStation},
	"KCVG": {"KCVG - Air Hub Gateway (Hebron, KY (CVG airport))", site.RoleAirHubGateway},
	"WH1":  {"Fulfilment Centre One", site.RoleUnspecified},
	"WH2":  {"WH2 - Delivery Station", site.RoleUnspecified},
	"WH3":  {"WH3 - Big Delivery Station (Leeds, GB)", site.RoleUnspecified},
}

// migratedTo boots a throwaway Postgres and migrates it to exactly version.
func migratedTo(t *testing.T, version uint) (string, *migrate.Migrate) {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("facility"),
		tcpostgres.WithUsername("facility"),
		tcpostgres.WithPassword("facility"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	m, err := migrate.New("file://"+migrationsDir(t), url)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	t.Cleanup(func() { _, _ = m.Close() })
	if err := m.Migrate(version); err != nil {
		t.Fatalf("migrate to %d: %v", version, err)
	}
	return url, m
}

// TestMigration0009_BackfillsTheRoleFromTheSeededNamePattern is the ADR-0037
// data-migration proof: rows written before the column existed (schema
// version 8, the seeded names) get their role from the name pattern exactly
// once, and anything that is not one of the 15 role phrases stays
// UNSPECIFIED. Capability and version are untouched.
func TestMigration0009_BackfillsTheRoleFromTheSeededNamePattern(t *testing.T) {
	url, m := migratedTo(t, 8)
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	for code, seeded := range seededSiteNames {
		if _, err := pool.Exec(ctx, `INSERT INTO sites (code, name, status, transfer_origin_enabled, capability_revision, version) VALUES ($1, $2, 'Active', TRUE, 3, 4)`, code, seeded.name); err != nil {
			t.Fatalf("seed %s: %v", code, err)
		}
	}
	if err := m.Migrate(9); err != nil {
		t.Fatalf("migrate to 9: %v", err)
	}
	// SiteRepo selects every current sites column, so apply the later
	// additive migrations (e.g. 0012, ADR-0041) before reading through it;
	// they do not touch the backfilled rows.
	if err := m.Up(); err != nil {
		t.Fatalf("migrate to latest: %v", err)
	}

	sites := postgres.NewSiteRepo(pool)
	for code, seeded := range seededSiteNames {
		got, err := sites.FindByCode(ctx, code)
		if err != nil || got == nil {
			t.Fatalf("reload %s: %v %v", code, got, err)
		}
		if got.Role() != seeded.want {
			t.Errorf("%s (%q): role %q, want %q", code, seeded.name, got.Role(), seeded.want)
		}
		if !got.TransferCapability().OriginEnabled() || got.CapabilityRevision() != 3 || got.Version() != 4 {
			t.Errorf("%s: the backfill touched capability/version: %+v rev=%d v=%d", code, got.TransferCapability(), got.CapabilityRevision(), got.Version())
		}
	}

	var outbox int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&outbox); err != nil || outbox != 0 {
		t.Fatalf("the backfill must publish nothing: %d rows, %v", outbox, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE sites SET site_role = 'WAREHOUSE' WHERE code = 'WH1'`); err == nil {
		t.Fatal("the CHECK constraint must reject an unknown role")
	}
}

// TestSiteRepo_PersistsTheRoleAndGuardsRoleChanges proves the role
// round-trips and that two concurrent role changes of one site serialise on
// the version guard: the loser gets ErrConcurrentModification and its
// SiteRoleChanged outbox row rolls back.
func TestSiteRepo_PersistsTheRoleAndGuardsRoleChanges(t *testing.T) {
	pool := outboxDB(t)
	ctx := context.Background()
	sites := postgres.NewSiteRepo(pool)
	s, err := site.NewSiteWithRole("ABE8", "ABE8 - Non-Sortable Fulfillment Center (Breinigsville, PA)", site.RoleNonSortableFC)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := sites.Save(ctx, s); err != nil {
		t.Fatalf("save: %v", err)
	}
	stale, _ := sites.FindByCode(ctx, "ABE8")
	fresh, _ := sites.FindByCode(ctx, "ABE8")
	if fresh.Role() != site.RoleNonSortableFC {
		t.Fatalf("role = %q", fresh.Role())
	}

	if _, _, err := fresh.ChangeRole(site.RoleXLFC); err != nil {
		t.Fatalf("change: %v", err)
	}
	if err := sites.Save(ctx, fresh); err != nil {
		t.Fatalf("save fresh: %v", err)
	}
	if _, _, err := stale.ChangeRole(site.RoleMicroFC); err != nil {
		t.Fatalf("change stale: %v", err)
	}
	if err := sites.Save(ctx, stale); !errors.Is(err, ports.ErrConcurrentModification) {
		t.Fatalf("stale save: expected ErrConcurrentModification, got %v", err)
	}
	stored, _ := sites.FindByCode(ctx, "ABE8")
	if stored.Role() != site.RoleXLFC || stored.Version() != 2 {
		t.Fatalf("persisted: role=%q version=%d", stored.Role(), stored.Version())
	}
}

// TestOutbox_ChangeSiteRole_WritesTheSiteRoleChangedCloudEvent proves the
// PUT /sites/{siteCode}/role use case commits the row and a CloudEvents 1.0
// SiteRoleChanged outbox row together, with the documented attributes and
// camelCase payload.
func TestOutbox_ChangeSiteRole_WritesTheSiteRoleChangedCloudEvent(t *testing.T) {
	pool := outboxDB(t)
	ctx := context.Background()
	sites := postgres.NewSiteRepo(pool)
	registerSiteForCapability(t, ctx, sites, "WH1")
	newID := func() string { return "evt-role" }
	at := time.Date(2026, 10, 13, 21, 0, 0, 0, time.UTC)
	uc := &usecases.ChangeSiteRole{
		Sites:      sites,
		Events:     postgres.NewOutboxPublisher(pool, newID, &outboundkafka.Publisher{NewId: newID}),
		Clock:      fixedClock{t: at},
		UnitOfWork: postgres.NewUnitOfWork(pool),
	}
	if _, err := uc.Execute(ctx, "WH1", site.RoleDeliveryStation); err != nil {
		t.Fatalf("change role: %v", err)
	}

	var topic, eventType string
	var key, value []byte
	if err := pool.QueryRow(ctx, `SELECT topic, event_type, key, value FROM outbox_events`).Scan(&topic, &eventType, &key, &value); err != nil {
		t.Fatalf("read the single outbox row: %v", err)
	}
	const wantType = "com.warehouse.wms.facility-layout.site.SiteRoleChanged"
	if topic != "warehouse.facility.events" || eventType != wantType || string(key) != "WH1" {
		t.Fatalf("outbox row: topic=%q event_type=%q key=%q", topic, eventType, key)
	}
	evt, err := cloudevents.Decode(value)
	if err != nil {
		t.Fatalf("outbox value is not a CloudEvent: %v", err)
	}
	if evt.SpecVersion() != "1.0" || evt.ID() != "evt-role" || evt.Type() != wantType || evt.Subject() != "WH1" ||
		evt.DataSchema() != "urn:warehouse:facility-layout:events:SiteRoleChanged:v1" || !evt.Time().Equal(at) {
		t.Fatalf("CloudEvent attributes: %s", evt.String())
	}
	var data struct {
		SiteCode         string `json:"siteCode"`
		SiteRole         string `json:"siteRole"`
		PreviousSiteRole string `json:"previousSiteRole"`
	}
	if err := json.Unmarshal(evt.Data(), &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if got := fmt.Sprintf("%+v", data); got != "{SiteCode:WH1 SiteRole:DELIVERY_STATION PreviousSiteRole:UNSPECIFIED}" {
		t.Fatalf("data = %s", got)
	}
	stored, _ := sites.FindByCode(ctx, "WH1")
	if stored.Role() != site.RoleDeliveryStation || stored.CapabilityRevision() != 0 {
		t.Fatalf("persisted: role=%q capabilityRevision=%d", stored.Role(), stored.CapabilityRevision())
	}
}
