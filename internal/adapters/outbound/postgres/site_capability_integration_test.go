//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/adapters/kafka/cloudevents"
	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

// nipSiteCapabilityChangedData is network-inventory-planning's decoder for
// this event, hand-mirrored byte-for-byte from its
// internal/adapters/inbound/kafka/consumers.go (never imported): if the
// outbox row stops decoding into it, NIP's site_capability read model
// stops receiving facts.
type nipSiteCapabilityChangedData struct {
	SiteCode                   string `json:"site_code"`
	TransferOriginEnabled      bool   `json:"transfer_origin_enabled"`
	TransferDestinationEnabled bool   `json:"transfer_destination_enabled"`
	CapabilityRevision         int64  `json:"capability_revision"`
}

func registerSiteForCapability(t *testing.T, ctx context.Context, sites *postgres.SiteRepo, code string) {
	t.Helper()
	s, err := site.NewSite(code, "Fulfilment Centre "+code)
	if err != nil {
		t.Fatalf("build site: %v", err)
	}
	if err := sites.Save(ctx, s); err != nil {
		t.Fatalf("seed site: %v", err)
	}
}

// TestOutbox_ChangeSiteTransferCapability_WritesTheCloudEventNIPDecodes is
// the ADR-0035 end-to-end proof on a real Postgres: the PUT's use case
// commits the site row and the integration-topic outbox row in one
// transaction, and that row is exactly the CloudEvent
// network-inventory-planning consumes (type, subject, key, dataschema and
// the snake_case payload, ADR-0035 / D2).
func TestOutbox_ChangeSiteTransferCapability_WritesTheCloudEventNIPDecodes(t *testing.T) {
	pool := outboxDB(t)
	ctx := context.Background()
	sites := postgres.NewSiteRepo(pool)
	registerSiteForCapability(t, ctx, sites, "WH1")
	newID := func() string { return "evt-capability" }
	at := time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)
	uc := &usecases.ChangeSiteTransferCapability{
		Sites:      sites,
		Events:     postgres.NewOutboxPublisher(pool, newID, &outboundkafka.Publisher{NewId: newID}),
		Clock:      fixedClock{t: at},
		UnitOfWork: postgres.NewUnitOfWork(pool),
	}

	if _, err := uc.Execute(ctx, "WH1", site.NewTransferCapability(true, false)); err != nil {
		t.Fatalf("change capability: %v", err)
	}

	var topic, eventType string
	var key, value []byte
	if err := pool.QueryRow(ctx, `SELECT topic, event_type, key, value FROM outbox_events`).Scan(&topic, &eventType, &key, &value); err != nil {
		t.Fatalf("read the single outbox row: %v", err)
	}
	const wantType = "com.warehouse.wms.facility-layout.site.SiteCapabilityChanged"
	if topic != "warehouse.facility.events" || eventType != wantType || string(key) != "WH1" {
		t.Fatalf("outbox row: topic=%q event_type=%q key=%q", topic, eventType, key)
	}

	evt, err := cloudevents.Decode(value)
	if err != nil {
		t.Fatalf("outbox value is not a CloudEvent: %v", err)
	}
	if evt.SpecVersion() != "1.0" || evt.ID() != "evt-capability" || evt.Type() != wantType ||
		evt.Subject() != "WH1" || evt.Source() != "/warehouse/facility-layout" ||
		evt.DataSchema() != "urn:warehouse:facility-layout:events:SiteCapabilityChanged:v1" ||
		!evt.Time().Equal(at) {
		t.Fatalf("CloudEvent attributes: %s", evt.String())
	}

	var nip nipSiteCapabilityChangedData
	if err := json.Unmarshal(evt.Data(), &nip); err != nil {
		t.Fatalf("NIP cannot decode the data: %v", err)
	}
	if nip != (nipSiteCapabilityChangedData{SiteCode: "WH1", TransferOriginEnabled: true, CapabilityRevision: 1}) {
		t.Fatalf("NIP decoded %+v", nip)
	}

	// D2: the payload stays snake_case (plus the shared camelCase envelope
	// prefix every facility-layout payload carries) — no recased duplicate.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(evt.Data(), &raw); err != nil {
		t.Fatalf("data is not a JSON object: %v", err)
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"capability_revision", "eventName", "eventType", "occurredAt", "site_code", "transfer_destination_enabled", "transfer_origin_enabled"}
	if len(keys) != len(want) {
		t.Fatalf("data keys = %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("data keys = %v, want %v", keys, want)
		}
	}

	stored, err := sites.FindByCode(ctx, "WH1")
	if err != nil || stored == nil {
		t.Fatalf("reload: %v %v", stored, err)
	}
	if stored.CapabilityRevision() != 1 || !stored.TransferCapability().OriginEnabled() || stored.Version() != 2 {
		t.Fatalf("persisted: revision=%d capability=%+v version=%d", stored.CapabilityRevision(), stored.TransferCapability(), stored.Version())
	}
}

// TestOutbox_ChangeSiteTransferCapability_RepeatWritesNoRow: an identical
// snapshot is a no-op, so redelivered PUTs never inflate the revision.
func TestOutbox_ChangeSiteTransferCapability_RepeatWritesNoRow(t *testing.T) {
	pool := outboxDB(t)
	ctx := context.Background()
	sites := postgres.NewSiteRepo(pool)
	registerSiteForCapability(t, ctx, sites, "WH1")
	newID := func() string { return uniqueID() }
	uc := &usecases.ChangeSiteTransferCapability{
		Sites:      sites,
		Events:     postgres.NewOutboxPublisher(pool, newID, &outboundkafka.Publisher{NewId: newID}),
		Clock:      fixedClock{t: time.Now().UTC()},
		UnitOfWork: postgres.NewUnitOfWork(pool),
	}
	for i := 0; i < 3; i++ {
		if _, err := uc.Execute(ctx, "WH1", site.NewTransferCapability(true, true)); err != nil {
			t.Fatalf("change %d: %v", i, err)
		}
	}
	if got := countOutbox(t, pool, "1=1"); got != 1 {
		t.Fatalf("expected exactly one outbox row for three identical snapshots, got %d", got)
	}
}

// TestSiteRepo_ConcurrentCapabilityChanges_NeverShareARevision is the
// ADR-0035 concurrency proof: two writers load the SAME site, each applies
// a DIFFERENT snapshot (both compute revision 1) and Save concurrently.
// Exactly one wins; the loser gets ports.ErrConcurrentModification, so no
// two committed snapshots can ever carry the same capability_revision.
func TestSiteRepo_ConcurrentCapabilityChanges_NeverShareARevision(t *testing.T) {
	pool := outboxDB(t)
	ctx := context.Background()
	sites := postgres.NewSiteRepo(pool)
	registerSiteForCapability(t, ctx, sites, "WH1")

	first, err := sites.FindByCode(ctx, "WH1")
	if err != nil {
		t.Fatalf("load 1: %v", err)
	}
	second, err := sites.FindByCode(ctx, "WH1")
	if err != nil {
		t.Fatalf("load 2: %v", err)
	}
	if _, err := first.ChangeTransferCapability(site.NewTransferCapability(true, false)); err != nil {
		t.Fatalf("change 1: %v", err)
	}
	if _, err := second.ChangeTransferCapability(site.NewTransferCapability(false, true)); err != nil {
		t.Fatalf("change 2: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var successes, conflicts int
	save := func(s *site.Site) {
		defer wg.Done()
		<-start
		err := sites.Save(ctx, s)
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
		t.Fatalf("successes=%d conflicts=%d, want exactly one winner", successes, conflicts)
	}
	reloaded, err := sites.FindByCode(ctx, "WH1")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Version() != 2 || reloaded.CapabilityRevision() != 1 {
		t.Fatalf("version=%d revision=%d, want 2/1", reloaded.Version(), reloaded.CapabilityRevision())
	}
}
