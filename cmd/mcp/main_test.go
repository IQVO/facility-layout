package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	inboundmcp "github.com/claudioed/facility-layout/internal/adapters/inbound/mcp"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/application/usecases"
)

// testRouter builds the exact HTTP surface cmd/mcp serves: the real MCP
// server over the in-memory TEST DOUBLES (the shipped binary has none: see
// TestRuntimeBinaryHasNoInMemoryAdapters), wrapped by newRouter. There is no
// auth layer.
func testRouter(t *testing.T) http.Handler {
	t.Helper()
	sites := memory.NewSiteRepo()
	zones := memory.NewZoneRepo()
	aisles := memory.NewAisleRepo()
	slots := memory.NewSlotRepo()
	server := inboundmcp.NewServer(inboundmcp.Deps{
		GetSiteLayout: &usecases.GetSiteLayout{Sites: sites, Zones: zones, Aisles: aisles, Slots: slots},
		GetZoneGrid:   &usecases.GetZoneGrid{Zones: zones, Aisles: aisles, Slots: slots},
		ListSites:     &usecases.ListSites{Sites: sites},
	})
	return newRouter(inboundmcp.Handler(server))
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz: got %d, want 200", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"status":"ok"}` {
		t.Fatalf("GET /healthz body: got %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("GET /healthz Content-Type: got %q", ct)
	}
}

func TestMCPMounts_ReachStreamableHandler(t *testing.T) {
	// An initialize request is the one call a fresh session may make, so a
	// JSON-RPC result (not 404) proves the handler is mounted at that path.
	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`
	for _, path := range []string{"/", "/mcp"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(initialize))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			rec := httptest.NewRecorder()
			testRouter(t).ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("POST %s initialize: got %d, want 200 (body %q)", path, rec.Code, rec.Body.String())
			}
			body, _ := io.ReadAll(rec.Body)
			if !strings.Contains(string(body), `"serverInfo"`) {
				t.Fatalf("POST %s initialize: expected a JSON-RPC initialize result, got %q", path, body)
			}
		})
	}
}
