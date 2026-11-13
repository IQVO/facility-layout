//go:build integration

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/analytics/report"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// itFreeAddr reserves a loopback port for ADMIN_ADDR.
func itFreeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().String()
}

// itMigrationsDir resolves migrations/analytics relative to this file, so the
// harness works regardless of the directory go test runs from.
func itMigrationsDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("unable to resolve test file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations", "analytics")
}

// itWaitUntil polls cond every 200ms until it holds or the 90s deadline passes.
func itWaitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestRunProjectsTheAnalyticsTopicAndStopsCleanlyOnSIGTERM boots the real
// run() of the projector (signal-driven composition root: it reads the
// environment through os.Getenv, so the documented knobs are set with
// t.Setenv) against an EMPTY testcontainers Postgres it migrates itself and a
// real Kafka: /readyz gates on both dependencies, the events the repo's REAL
// AnalyticsPublisher encoder wrote on warehouse.facility.analytics are folded
// into the catalog-growth rollup under the consumer's documented scopes
// (site, zone-of-location, catalog-wide), and a SIGTERM is the graceful
// shutdown ADR-0020 documents — run returns nil within a bound.
func TestRunProjectsTheAnalyticsTopicAndStopsCleanlyOnSIGTERM(t *testing.T) {
	if err := bootShared(); err != nil {
		t.Fatalf("shared containers: %v", err)
	}
	databaseURL := itFreshDB(t)

	// The broker has auto-creation off on this image: the analytics topic and
	// its DLQ must exist before the consumer starts (ADR-0020).
	ctx := context.Background()
	for _, topic := range []string{outboundkafka.AnalyticsTopic, outboundkafka.AnalyticsTopic + ".dlq"} {
		conn, err := kafkago.Dial("tcp", kafkaBroker)
		if err != nil {
			t.Fatalf("dial %s: %v", kafkaBroker, err)
		}
		if err := conn.CreateTopics(kafkago.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}); err != nil {
			t.Fatalf("create topic %s: %v", topic, err)
		}
		_ = conn.Close()
	}

	adminAddr := itFreeAddr(t)
	t.Setenv("ANALYTICS_DATABASE_URL", databaseURL)
	t.Setenv("KAFKA_BROKERS", kafkaBroker)
	t.Setenv("ANALYTICS_CONSUMER_GROUP", fmt.Sprintf("it-facility-projector-%d", time.Now().UnixNano()))
	t.Setenv("ADMIN_ADDR", adminAddr)
	t.Setenv("ANALYTICS_MIGRATIONS_PATH", itMigrationsDir())
	t.Setenv("LOG_LEVEL", "error")

	done := make(chan error, 1)
	go func() { done <- run() }()
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Error("run did not return after SIGTERM")
		}
	})

	itWaitUntil(t, "/readyz to answer 200 with Postgres and Kafka up", func() bool {
		resp, err := http.Get("http://" + adminAddr + "/readyz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})

	// The REAL encoder of the analytics data product (the same
	// NewAnalyticsPublisher cmd/facility wires): the projector must project
	// what production actually writes.
	at := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	code, err := shared.NewLocationCode("IT1", "AR1", "ZN1", "AS1", "B01", "L1", "P1")
	if err != nil {
		t.Fatal(err)
	}
	capacity, err := shared.NewCapacity(1200, 2.5)
	if err != nil {
		t.Fatal(err)
	}
	publisher := outboundkafka.NewAnalyticsPublisher([]string{kafkaBroker}, uuid.NewString)
	for _, event := range []shared.DomainEvent{
		shared.NewSiteRegistered(at, "IT1", "Integration Site One", "UNSPECIFIED"),
		shared.NewZoneRegistered(at, "IT1-AR1-ZN1", "IT1", "AR1", "ZN1", shared.Ambient, false),
		shared.NewAisleRegistered(at, "IT1-AR1-ZN1-AS1", "IT1-AR1-ZN1", "AS1", 1, shared.TwoWay),
		shared.NewLocationSlotRegistered(at, code, "STORAGE", "Storage", "", nil, capacity),
		shared.NewLocationTypeRegistered(at, "PICK_FACE", "PICK", capacity),
	} {
		if err := publisher.Publish(ctx, event); err != nil {
			t.Fatalf("publish %s: %v", event.EventName(), err)
		}
	}

	pool, err := analyticsstore.NewPool(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	reader := analyticsstore.NewPostgresReport(pool)
	day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	itWaitUntil(t, "the projector to fold the catalog changes into the rollup", func() bool {
		rep, err := reader.Query(ctx, report.ReportQuery{From: day, To: day.Add(24 * time.Hour)})
		if err != nil {
			return false
		}
		return rowOf(rep, "IT1") != nil && rowOf(rep, "IT1").SitesRegistered == 1 &&
			rowOf(rep, "IT1").ZonesRegistered == 1 &&
			rowOf(rep, "IT1-AR1-ZN1") != nil && rowOf(rep, "IT1-AR1-ZN1").AislesRegistered == 1 &&
			rowOf(rep, "IT1-AR1-ZN1").SlotsRegistered == 1 &&
			rowOf(rep, "") != nil && rowOf(rep, "").LocationTypesRegistered == 1
	})

	// The documented drain contract of this signal-bound binary: SIGTERM
	// flips readiness first, drains the admin server and the consumer
	// bounded, and run returns nil.
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a SIGTERM must drain cleanly, got %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the projector did not stop after SIGTERM")
	}
	done <- nil // the cleanup waits for one more value
}

// rowOf returns the rollup row of scope, or nil when absent.
func rowOf(rep report.CatalogReport, scope string) *report.Row {
	for i := range rep.Rows {
		if rep.Rows[i].Key.Scope == scope {
			return &rep.Rows[i]
		}
	}
	return nil
}
