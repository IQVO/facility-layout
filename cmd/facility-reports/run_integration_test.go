//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
)

// repMigrationsDir resolves migrations/analytics relative to this file.
func repMigrationsDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("unable to resolve test file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations", "analytics")
}

// repFreeAddr reserves a loopback port for HTTP_ADDR.
func repFreeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().String()
}

// repGet fetches url and returns the status and decoded JSON body.
func repGet(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// TestRunServesTheCatalogGrowthReportFromTheAnalyticalDatabaseAndStopsCleanly
// boots the real run() of the reader (signal-driven composition root reading
// os.Getenv, so the documented knobs are set with t.Setenv) over an
// analytical database seeded through the repo's own writer — the same
// PostgresProjection the projector drives: /readyz gates on the database, the
// catalog-growth report answers the seeded counts under their documented
// scopes, the freshness endpoint answers, and a SIGTERM is the graceful
// shutdown ADR-0020 documents — run returns nil within a bound.
func TestRunServesTheCatalogGrowthReportFromTheAnalyticalDatabaseAndStopsCleanly(t *testing.T) {
	ctx := context.Background()
	databaseURL := repFreshDB(t)
	if err := postgres.RunMigrations(databaseURL, repMigrationsDir()); err != nil {
		t.Fatal(err)
	}
	writer, err := analyticsstore.NewPool(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	at := time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC)
	projection := analyticsstore.NewPostgresProjection(writer)
	seed := []struct {
		apply func() error
	}{
		{func() error { return projection.ApplySiteRegistered(ctx, "rep-e1", "IT1", at) }},
		{func() error { return projection.ApplyZoneRegistered(ctx, "rep-e2", "IT1", at) }},
		{func() error { return projection.ApplyLocationSlotRegistered(ctx, "rep-e3", "IT1-AR1-ZN1", at) }},
		{func() error { return projection.ApplyFacilityLayoutImported(ctx, "rep-e4", "", 10, 7, 3, at) }},
	}
	for i, s := range seed {
		if err := s.apply(); err != nil {
			t.Fatalf("seed fact %d: %v", i, err)
		}
	}

	httpAddr := repFreeAddr(t)
	t.Setenv("ANALYTICS_DATABASE_URL", databaseURL)
	t.Setenv("HTTP_ADDR", httpAddr)
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

	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if code, _ := repGet(t, "http://"+httpAddr+"/readyz"); code == http.StatusOK {
			break
		}
	}
	if code, body := repGet(t, "http://"+httpAddr+"/readyz"); code != http.StatusOK {
		t.Fatalf("/readyz = %d %v", code, body)
	}

	// The report answers from the seeded facts, under the consumer's
	// documented scopes: the site's own row, the zone-of-location row and the
	// catalog-wide row of the bulk import.
	code, body := repGet(t, "http://"+httpAddr+"/reports/catalog-growth?from=2026-11-12T00:00:00Z&to=2026-11-13T00:00:00Z")
	if code != http.StatusOK {
		t.Fatalf("catalog-growth = %d %v", code, body)
	}
	rows, _ := body["rows"].([]any)
	rowOf := func(scope string) map[string]any {
		for _, r := range rows {
			if m, _ := r.(map[string]any); m != nil && m["scope"] == scope {
				return m
			}
		}
		return nil
	}
	site := rowOf("IT1")
	zone := rowOf("IT1-AR1-ZN1")
	catalog := rowOf("")
	if site == nil || site["sitesRegistered"] != float64(1) || site["zonesRegistered"] != float64(1) {
		t.Fatalf("site row = %v, want one site and one zone registered", site)
	}
	if zone == nil || zone["slotsRegistered"] != float64(1) {
		t.Fatalf("zone row = %v, want one slot registered", zone)
	}
	if catalog == nil || catalog["bulkImports"] != float64(1) ||
		catalog["importRowsSubmitted"] != float64(10) || catalog["importRowsImported"] != float64(7) || catalog["importRowsRejected"] != float64(3) {
		t.Fatalf("catalog-wide row = %v, want the import tallies folded", catalog)
	}

	// A scope filter narrows to that scope's rows only.
	code, body = repGet(t, "http://"+httpAddr+"/reports/catalog-growth?from=2026-11-12T00:00:00Z&to=2026-11-13T00:00:00Z&scope=IT1")
	if code != http.StatusOK || len(body["rows"].([]any)) != 1 {
		t.Fatalf("scoped catalog-growth = %d %v", code, body)
	}
	// A missing bound is the documented 400 problem.
	if code, body := repGet(t, "http://"+httpAddr+"/reports/catalog-growth?from=2026-11-12T00:00:00Z"); code != http.StatusBadRequest {
		t.Fatalf("catalog-growth without to = %d %v, want 400", code, body)
	}
	// The freshness of the read model answers.
	if code, body := repGet(t, "http://"+httpAddr+"/reports/catalog-growth/freshness"); code != http.StatusOK {
		t.Fatalf("freshness = %d %v", code, body)
	}

	// The documented drain contract of this signal-bound binary: SIGTERM
	// flips readiness first, then drains the HTTP server bounded, and run
	// returns nil.
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a SIGTERM must drain cleanly, got %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the reports server did not stop after SIGTERM")
	}
	done <- nil // the cleanup waits for one more value
}
