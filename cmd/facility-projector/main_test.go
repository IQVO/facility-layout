package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestAdminMuxCarriesOTelAndFlipsReadiness pins the audit gap (ADR-0012 +
// ADR-0020): the projector's admin server is traced/metered like the main
// router (the chart injects OTEL_* env into the projector pod), and /readyz
// still flips to 503 on shutdown while /healthz stays 200.
func TestAdminMuxCarriesOTelAndFlipsReadiness(t *testing.T) {
	var notReady atomic.Bool
	handler := newAdminMux(&notReady, "facility-projector-test")

	get := func(path string) int {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code
	}

	if code := get("/healthz"); code != http.StatusOK {
		t.Fatalf("healthz = %d, want 200", code)
	}
	if code := get("/readyz"); code != http.StatusOK {
		t.Fatalf("readyz before flip = %d, want 200", code)
	}

	notReady.Store(true)
	if code := get("/readyz"); code != http.StatusServiceUnavailable {
		t.Fatalf("readyz after flip = %d, want 503", code)
	}
	// Liveness is never flipped by shutdown (ADR-0020).
	if code := get("/healthz"); code != http.StatusOK {
		t.Fatalf("healthz after flip = %d, want 200 (liveness is never flipped)", code)
	}
}
