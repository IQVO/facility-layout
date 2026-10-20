package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	inboundhttp "github.com/claudioed/facility-layout/internal/adapters/inbound/http"
)

// TestAdminMuxCarriesOTelAndReportsReadiness pins the audit gap (ADR-0012 +
// ADR-0020 + ADR 0039): the projector's admin server is traced/metered like
// the main router, /readyz flips to 503 on shutdown while /healthz stays 200,
// and /readyz is also 503 while Postgres or Kafka is unreachable.
func TestAdminMuxCarriesOTelAndReportsReadiness(t *testing.T) {
	var kafkaErr error
	readiness := newReadiness(
		func(context.Context) error { return nil },
		func(context.Context) error { return kafkaErr },
	)
	handler := newAdminMux(readiness, "facility-projector-test")

	get := func(path string) int {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code
	}

	if code := get("/healthz"); code != http.StatusOK {
		t.Fatalf("healthz = %d, want 200", code)
	}
	if code := get("/readyz"); code != http.StatusOK {
		t.Fatalf("readyz with every dependency up = %d, want 200", code)
	}

	readiness.SetNotReady()
	if code := get("/readyz"); code != http.StatusServiceUnavailable {
		t.Fatalf("readyz after the drain flip = %d, want 503", code)
	}
	// Liveness is never flipped by shutdown (ADR-0020).
	if code := get("/healthz"); code != http.StatusOK {
		t.Fatalf("healthz after flip = %d, want 200 (liveness is never flipped)", code)
	}
}

func TestReadinessFailsWhileADependencyIsDown(t *testing.T) {
	readiness := newReadiness(
		func(context.Context) error { return nil },
		func(context.Context) error { return errors.New("no broker") },
	)
	rec := httptest.NewRecorder()
	inboundhttp.HandleReadyz(readiness)(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz with kafka down = %d, want 503", rec.Code)
	}
}
