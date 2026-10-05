package mcp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/facility-layout/internal/adapters/inbound/mcp"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// newServer builds a real MCP HTTP server over in-memory repos seeded (through
// the real write use cases) with a small WH1 layout, and returns its httptest
// URL. There is no auth layer in front of it — the static-bearer-key rollout
// was removed.
func newServer(t *testing.T) string {
	t.Helper()
	sites := memory.NewSiteRepo()
	zones := memory.NewZoneRepo()
	aisles := memory.NewAisleRepo()
	slots := memory.NewSlotRepo()
	locationTypes := memory.NewLocationTypeRepo()
	rules := memory.NewPlacementRuleRepo()
	publisher := events.NewBufferedPublisher()
	clock := memory.NewFixedClock(time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC))
	ctx := context.Background()

	registerSite := &usecases.RegisterSite{Sites: sites, Events: publisher, Clock: clock}
	registerZone := &usecases.RegisterZone{Sites: sites, Zones: zones, Events: publisher, Clock: clock}
	registerAisle := &usecases.RegisterAisle{Zones: zones, Aisles: aisles, Events: publisher, Clock: clock}
	registerType := &usecases.RegisterLocationType{LocationTypes: locationTypes, Events: publisher, Clock: clock}
	registerSlot := &usecases.RegisterLocationSlot{
		Sites: sites, Zones: zones, Aisles: aisles, Slots: slots,
		LocationTypes: locationTypes, Rules: rules, Events: publisher, Clock: clock,
	}

	mustCapacity := func(w, v float64) shared.Capacity {
		c, err := shared.NewCapacity(w, v)
		if err != nil {
			t.Fatalf("capacity: %v", err)
		}
		return c
	}
	mustCode := func(raw string) shared.LocationCode {
		c, err := shared.ParseLocationCode(raw)
		if err != nil {
			t.Fatalf("parse code %q: %v", raw, err)
		}
		return c
	}

	if _, err := registerSite.Execute(ctx, "WH1", "Fulfilment Centre One"); err != nil {
		t.Fatalf("seed site: %v", err)
	}
	if _, err := registerType.Execute(ctx, placement.PalletRack, placement.Storage, mustCapacity(1200, 2.4)); err != nil {
		t.Fatalf("seed type: %v", err)
	}
	if _, err := registerZone.Execute(ctx, "WH1", "STOR", "AMB", shared.Ambient, false, nil, nil); err != nil {
		t.Fatalf("seed zone: %v", err)
	}
	if _, err := registerAisle.Execute(ctx, "WH1-STOR-AMB", "A07", 7, shared.TwoWay); err != nil {
		t.Fatalf("seed aisle: %v", err)
	}
	if _, err := registerSlot.Execute(ctx, mustCode("WH1-STOR-AMB-A07-03-02-B"), placement.PalletRack, shared.Capacity{}, "", nil); err != nil {
		t.Fatalf("seed slot: %v", err)
	}

	deps := inboundmcp.Deps{
		GetSiteLayout: &usecases.GetSiteLayout{Sites: sites, Zones: zones, Aisles: aisles, Slots: slots},
		GetZoneGrid:   &usecases.GetZoneGrid{Zones: zones, Aisles: aisles, Slots: slots},
		ListSites:     &usecases.ListSites{Sites: sites},
	}
	server := inboundmcp.NewServer(deps)
	httpSrv := httptest.NewServer(inboundmcp.Handler(server))
	t.Cleanup(httpSrv.Close)
	return httpSrv.URL
}

func connect(t *testing.T, url string) *sdk.ClientSession {
	t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	transport := &sdk.StreamableClientTransport{
		Endpoint:   url,
		HTTPClient: &http.Client{},
	}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestServer_ToolsListAndCall(t *testing.T) {
	url := newServer(t)
	session := connect(t, url)
	ctx := context.Background()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	want := map[string]bool{"list_sites": false, "get_site_layout": false, "get_zone_grid": false}
	for _, tool := range tools.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("tool %q not advertised", name)
		}
	}
	// This context is read-only: no write tool must ever be advertised.
	for _, tool := range tools.Tools {
		if tool.Annotations != nil && !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %q is not annotated read-only; this context exposes no write tool", tool.Name)
		}
	}

	res, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name:      "get_site_layout",
		Arguments: map[string]any{"siteCode": "WH1"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}
	site, ok := res.StructuredContent.(map[string]any)["site"].(map[string]any)
	if !ok {
		t.Fatalf("no site header in structured content: %+v", res.StructuredContent)
	}
	if site["code"] != "WH1" {
		t.Fatalf("site code = %v, want WH1", site["code"])
	}
}

func TestServer_CallToolRejectsUnknownSite(t *testing.T) {
	url := newServer(t)
	session := connect(t, url)
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "get_site_layout",
		Arguments: map[string]any{"siteCode": "GHOST"},
	})
	if err != nil {
		t.Fatalf("call tool transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected tool-level error for an unknown site")
	}
}

func TestServer_ListSitesOverTheWire(t *testing.T) {
	url := newServer(t)
	session := connect(t, url)
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "list_sites",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_sites returned error: %+v", res.Content)
	}
	sites, ok := res.StructuredContent.(map[string]any)["sites"].([]any)
	if !ok || len(sites) != 1 {
		t.Fatalf("expected 1 site, got %+v", res.StructuredContent)
	}
}

func TestServer_ResourceRead(t *testing.T) {
	url := newServer(t)
	session := connect(t, url)
	res, err := session.ReadResource(context.Background(), &sdk.ReadResourceParams{
		URI: "layout://facility/WH1",
	})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(res.Contents) == 0 || res.Contents[0].Text == "" {
		t.Fatalf("empty resource contents: %+v", res.Contents)
	}
}

func TestServer_PromptGet(t *testing.T) {
	url := newServer(t)
	session := connect(t, url)
	res, err := session.GetPrompt(context.Background(), &sdk.GetPromptParams{Name: "explore_layout"})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if len(res.Messages) == 0 {
		t.Fatal("explore_layout prompt returned no messages")
	}
}
