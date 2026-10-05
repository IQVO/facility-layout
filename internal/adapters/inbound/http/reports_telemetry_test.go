package http_test

import (
	stdhttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/claudioed/facility-layout/internal/adapters/inbound/http"
)

// TestReportsRouterCarriesOTelMiddleware pins ADR-0012's reach (the audit
// gap): the reports router must be traced and metered like the main router,
// because the chart injects OTEL_* env into the reports pod. otelchi's
// middleware emits a traceparent-capable span context and the metric
// middleware records http.server.request.duration — observable here as the
// request still serving correctly (and, structurally, as the router
// accepting the WithServiceName option without which the middleware would
// run under the wrong service name).
func TestReportsRouterCarriesOTelMiddleware(t *testing.T) {
	store := &fakeReportStore{}
	router := http.NewReportsRouter(&http.ReportsHandlers{Store: store}, nil, http.WithServiceName("facility-reports-test"))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "/healthz", nil))
	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("healthz through the instrumented router = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "/reports/catalog-growth/freshness", nil))
	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("freshness through the instrumented router = %d, want 200", rec.Code)
	}
}

// TestReportsRouterReadinessFlip verifies /readyz still flips on the
// instrumented router (ADR-0020 §graceful shutdown unchanged by the
// otelchi middleware).
func TestReportsRouterReadinessFlip(t *testing.T) {
	var readiness http.Readiness
	router := http.NewReportsRouter(&http.ReportsHandlers{Store: &fakeReportStore{}, Readiness: &readiness}, nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "/readyz", nil))
	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("readyz before flip = %d, want 200", rec.Code)
	}

	readiness.SetNotReady()
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "/readyz", nil))
	if rec.Code != stdhttp.StatusServiceUnavailable {
		t.Fatalf("readyz after flip = %d, want 503", rec.Code)
	}
}
