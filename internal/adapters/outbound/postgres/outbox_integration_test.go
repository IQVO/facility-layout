//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/claudioed/facility-layout/internal/adapters/kafka/cloudevents"
	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// outboxDB boots a throwaway Postgres (testcontainers — the test owns its
// own database, never an external DATABASE_URL) and runs migrations.
func outboxDB(t *testing.T) *pgxpool.Pool {
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
	if err := postgres.RunMigrations(url, migrationsDir(t)); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type recordingSink struct {
	sent    []outboundkafka.Encoded
	failOn  string // EventType to fail on, "" for never
	failErr error
}

func (s *recordingSink) Send(_ context.Context, enc outboundkafka.Encoded) error {
	if s.failOn != "" && enc.EventType == s.failOn {
		return s.failErr
	}
	s.sent = append(s.sent, enc)
	return nil
}

func countOutbox(t *testing.T, pool *pgxpool.Pool, where string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM outbox_events WHERE "+where).Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	return n
}

// failingEncoder always fails to encode, so a test can force the outbox
// insert to fail without touching Postgres directly.
type failingEncoder struct{}

func (failingEncoder) Encode(_ context.Context, _ shared.DomainEvent, _ string) (outboundkafka.Encoded, error) {
	return outboundkafka.Encoded{}, errors.New("encode: forced failure")
}

// TestOutbox_RegisterSite_CommitsAggregateAndEventTogether proves the
// aggregate write and BOTH topics' outbox rows land in the same
// transaction.
func TestOutbox_RegisterSite_CommitsAggregateAndEventTogether(t *testing.T) {
	pool := outboxDB(t)
	ctx := context.Background()
	newId := func() string { return "evt-fixed" }
	uc := &usecases.RegisterSite{
		Sites: postgres.NewSiteRepo(pool),
		Events: postgres.NewOutboxPublisher(pool, newId,
			&outboundkafka.Publisher{NewId: newId},
			&outboundkafka.AnalyticsPublisher{NewId: newId},
		),
		Clock:      fixedClock{t: time.Now().UTC().Truncate(time.Microsecond)},
		UnitOfWork: postgres.NewUnitOfWork(pool),
	}

	if _, err := uc.Execute(ctx, "WH1", "Main"); err != nil {
		t.Fatalf("register site: %v", err)
	}

	if got := countOutbox(t, pool, "published_at IS NULL AND topic = 'warehouse.facility.events'"); got != 1 {
		t.Fatalf("expected 1 unpublished integration-topic row, got %d", got)
	}
	if got := countOutbox(t, pool, "published_at IS NULL AND topic = 'warehouse.facility.analytics'"); got != 1 {
		t.Fatalf("expected 1 unpublished analytics-topic row, got %d", got)
	}
	found, err := postgres.NewSiteRepo(pool).FindByCode(ctx, "WH1")
	if err != nil || found == nil {
		t.Fatalf("expected WH1 persisted, got %v err=%v", found, err)
	}

	// Both rows carry a CloudEvents 1.0 value with the SAME id (minted once
	// per domain event) and the stream-specific dataschema (ADR-0024).
	rows, err := pool.Query(ctx, `SELECT topic, value FROM outbox_events ORDER BY id`)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()
	schemas := map[string]string{}
	for rows.Next() {
		var topic string
		var value []byte
		if err := rows.Scan(&topic, &value); err != nil {
			t.Fatalf("scan: %v", err)
		}
		evt, err := cloudevents.Decode(value)
		if err != nil {
			t.Fatalf("outbox row on %s is not a CloudEvent: %v", topic, err)
		}
		if evt.ID() != "evt-fixed" || evt.Subject() != "WH1" || evt.Type() != "com.warehouse.wms.facility-layout.site.SiteRegistered" {
			t.Errorf("row on %s: id=%q subject=%q type=%q", topic, evt.ID(), evt.Subject(), evt.Type())
		}
		schemas[topic] = evt.DataSchema()
	}
	if schemas["warehouse.facility.events"] != "urn:warehouse:facility-layout:events:SiteRegistered:v1" ||
		schemas["warehouse.facility.analytics"] != "urn:warehouse:facility-layout:analytics:SiteRegistered:v1" {
		t.Errorf("dataschemas = %v", schemas)
	}
}

// TestOutbox_PublishFailure_RollsBackAggregate is the whole point of the
// outbox: if the event cannot be enqueued the aggregate change must not
// survive either.
func TestOutbox_PublishFailure_RollsBackAggregate(t *testing.T) {
	pool := outboxDB(t)
	ctx := context.Background()
	uc := &usecases.RegisterSite{
		Sites:      postgres.NewSiteRepo(pool),
		Events:     postgres.NewOutboxPublisher(pool, func() string { return "evt" }, failingEncoder{}),
		Clock:      fixedClock{t: time.Now().UTC()},
		UnitOfWork: postgres.NewUnitOfWork(pool),
	}

	if _, err := uc.Execute(ctx, "WH2", "Failing"); err == nil {
		t.Fatal("expected the failing encoder to fail the publish")
	}
	found, err := postgres.NewSiteRepo(pool).FindByCode(ctx, "WH2")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found != nil {
		t.Fatal("aggregate row survived a failed publish: the unit of work did not roll back")
	}
	if got := countOutbox(t, pool, "1=1"); got != 0 {
		t.Fatalf("expected no outbox rows at all, got %d", got)
	}
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// TestOutboxRelay_PublishesInOrderAndMarksRows exercises three sequential
// registrations through the same UnitOfWork/OutboxPublisher and then
// drains them with the relay, in order.
func TestOutboxRelay_PublishesInOrderAndMarksRows(t *testing.T) {
	pool := outboxDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	newId := func() string { return uniqueID() }
	sites := postgres.NewSiteRepo(pool)
	zones := postgres.NewZoneRepo(pool)
	pub := postgres.NewOutboxPublisher(pool, newId, &outboundkafka.Publisher{NewId: newId})
	uow := postgres.NewUnitOfWork(pool)

	registerSite := &usecases.RegisterSite{Sites: sites, Events: pub, Clock: fixedClock{t: now}, UnitOfWork: uow}
	registerZone := &usecases.RegisterZone{Sites: sites, Zones: zones, Events: pub, Clock: fixedClock{t: now.Add(time.Second)}, UnitOfWork: uow}

	if _, err := registerSite.Execute(ctx, "WH3", "Third"); err != nil {
		t.Fatalf("register site: %v", err)
	}
	if _, err := registerZone.Execute(ctx, "WH3", "STOR", "AMB", shared.Ambient, false); err != nil {
		t.Fatalf("register zone: %v", err)
	}

	sink := &recordingSink{}
	relay := postgres.NewOutboxRelay(pool, sink, slog.Default())
	n, err := relay.RelayOnce(ctx)
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	if n != 2 || len(sink.sent) != 2 {
		t.Fatalf("expected 2 published, got n=%d sent=%d", n, len(sink.sent))
	}
	if sink.sent[0].EventType != "com.warehouse.wms.facility-layout.site.SiteRegistered" {
		t.Fatalf("expected SiteRegistered first, got %s", sink.sent[0].EventType)
	}
	if sink.sent[1].EventType != "com.warehouse.wms.facility-layout.zone.ZoneRegistered" {
		t.Fatalf("expected ZoneRegistered second, got %s", sink.sent[1].EventType)
	}
	if got := countOutbox(t, pool, "published_at IS NULL"); got != 0 {
		t.Fatalf("expected every row marked published, %d still pending", got)
	}
	// A second pass finds nothing and republishes nothing.
	n, err = relay.RelayOnce(ctx)
	if err != nil || n != 0 || len(sink.sent) != 2 {
		t.Fatalf("second pass should be a no-op, got n=%d err=%v sent=%d", n, err, len(sink.sent))
	}
}

// TestOutboxRelay_SinkFailure_StopsAtFailedRowAndRetriesLater proves a
// failed row halts the pass at that row (preserving ordering) and that
// the next pass, once the sink recovers, resumes from there.
func TestOutboxRelay_SinkFailure_StopsAtFailedRowAndRetriesLater(t *testing.T) {
	pool := outboxDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	newId := func() string { return uniqueID() }
	sites := postgres.NewSiteRepo(pool)
	pub := postgres.NewOutboxPublisher(pool, newId, &outboundkafka.Publisher{NewId: newId})
	uow := postgres.NewUnitOfWork(pool)
	registerSite := &usecases.RegisterSite{Sites: sites, Events: pub, Clock: fixedClock{t: now}, UnitOfWork: uow}

	for _, code := range []string{"A1", "B2", "C3"} {
		if _, err := registerSite.Execute(ctx, code, code); err != nil {
			t.Fatalf("register %s: %v", code, err)
		}
	}

	// Every row here is a SiteRegistered on the same topic; fail on the
	// SECOND Send call by keying the fake on the row's Key instead of its
	// (identical) EventType.
	sink := &keyFailingSink{failOnKey: "B2", failErr: errors.New("broker down")}
	relay := postgres.NewOutboxRelay(pool, sink, slog.Default())
	n, err := relay.RelayOnce(ctx)
	if err == nil {
		t.Fatal("expected the failing row to surface an error")
	}
	if n != 1 || len(sink.sent) != 1 || string(sink.sent[0].Key) != "A1" {
		t.Fatalf("expected only A1 published before the failure, got n=%d sent=%v", n, sink.sent)
	}
	if got := countOutbox(t, pool, "published_at IS NULL"); got != 2 {
		t.Fatalf("expected B2 and C3 still pending (ordering preserved), got %d pending", got)
	}
	var attempts int
	var lastErr string
	if err := pool.QueryRow(ctx, "SELECT attempts, coalesce(last_error,'') FROM outbox_events WHERE key = 'B2'").Scan(&attempts, &lastErr); err != nil {
		t.Fatalf("read B2: %v", err)
	}
	if attempts != 1 || lastErr == "" {
		t.Fatalf("expected B2 to record the failed attempt, got attempts=%d last_error=%q", attempts, lastErr)
	}

	// Broker recovers: the next pass drains the rest, in order.
	sink.failOnKey = ""
	n, err = relay.RelayOnce(ctx)
	if err != nil || n != 2 {
		t.Fatalf("recovery pass: n=%d err=%v", n, err)
	}
	if string(sink.sent[1].Key) != "B2" || string(sink.sent[2].Key) != "C3" {
		t.Fatalf("expected B2 then C3 after recovery, got %v", sink.sent)
	}
	if got := countOutbox(t, pool, "published_at IS NULL"); got != 0 {
		t.Fatalf("expected outbox drained, %d pending", got)
	}
}

type keyFailingSink struct {
	sent      []outboundkafka.Encoded
	failOnKey string
	failErr   error
}

func (s *keyFailingSink) Send(_ context.Context, enc outboundkafka.Encoded) error {
	if s.failOnKey != "" && string(enc.Key) == s.failOnKey {
		return s.failErr
	}
	s.sent = append(s.sent, enc)
	return nil
}

var idCounter int

// uniqueID mints a distinct id per call within a test process — good
// enough for these tests, which never need cryptographic randomness.
func uniqueID() string {
	idCounter++
	return "evt-" + strconv.Itoa(idCounter)
}
