package http_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	inboundhttp "github.com/claudioed/facility-layout/internal/adapters/inbound/http"
)

// TestCORSPreflightAllowsIdempotencyKey pins the CORS contract the console
// depends on (ADR-0019 + ADR-0011): the browser only sends Idempotency-Key on
// the real request if the preflight response's Access-Control-Allow-Headers
// lists it. Before this was pinned, AllowedHeaders carried only
// Content-Type/Authorization, every console create form's preflight dropped
// the key, and the actual POST answered 400 idempotency-key-required against
// a Postgres-backed API.
func TestCORSPreflightAllowsIdempotencyKey(t *testing.T) {
	handler := inboundhttp.NewRouter(&inboundhttp.Server{}, slog.New(slog.NewTextHandler(httptest.NewRecorder(), nil)))

	req := httptest.NewRequest(http.MethodOptions, "/sites", nil)
	req.Header.Set("Origin", "http://localhost:5186")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "content-type,authorization,idempotency-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Fatalf("preflight status = %d, want 204 (or 200)", rec.Code)
	}
	allowed := rec.Header().Get("Access-Control-Allow-Headers")
	for _, want := range []string{"Content-Type", "Authorization", "Idempotency-Key"} {
		found := false
		for _, h := range strings.Split(allowed, ",") {
			if strings.TrimSpace(h) == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Access-Control-Allow-Headers = %q, want it to include %q", allowed, want)
		}
	}
}
