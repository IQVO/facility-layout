// Shared harness for the MCP eval suites (E1-E3): one place that builds a
// real Streamable HTTP server over in-memory adapters — seeded through the
// real write use cases — and connects a real SDK client to it, so schema
// evals, wire conformance evals, and the Gherkin behavioral evals all
// exercise exactly the surface a model host would.
package mcp_test

import (
	"context"
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

// evalHarness is a fully wired MCP server over in-memory repos plus the
// client session talking to it, recording the last tool call for the Then
// steps. facility-layout is a read-only Open Host Service, so there is no
// publisher to inspect and no write tool to drive — only read state.
type evalHarness struct {
	session         *sdk.ClientSession
	lastCallResult  *sdk.CallToolResult
	lastCallErr     error
	lastCallContent string
}

// seedEvalLayout builds Deps over in-memory repos and seeds the canonical
// eval state through the REAL write use cases (so the read models under
// eval are built from genuinely-registered aggregates, mirroring the
// in-package harness in tools_test.go):
//
//	Site WH1 "Fulfilment Centre One"
//	Zones  WH1-STOR-AMB, WH1-RCV-AMB, WH1-DOCK-OB (ambient, non-hazmat)
//	Aisles STOR: A07 (two-way, walk hint 7), A09 (one-way, hint 9);
//	       RCV: D01 (hint 1); DOCK-OB: D01 (hint 1)
//	Slots  A07 bays 01/02/03 (pallet rack), A09 bay 01 (shelf),
//	       RCV D01 bay 01 (shelf), DOCK-OB D01 bays 01/02 (dock doors,
//	       Outbound + Inbound)
//
// Note Deps.Reports stays nil: the curated get_facility_catalog_growth_report
// tool is conditionally registered and is deliberately outside the pinned
// default surface (same treatment the pilot repo gave its report tool).
func seedEvalLayout(t *testing.T) inboundmcp.Deps {
	t.Helper()

	sites := memory.NewSiteRepo()
	zones := memory.NewZoneRepo()
	aisles := memory.NewAisleRepo()
	slots := memory.NewSlotRepo()
	locationTypes := memory.NewLocationTypeRepo()
	rules := memory.NewPlacementRuleRepo()
	crossAisles := memory.NewCrossAisleRepo()
	publisher := events.NewBufferedPublisher()
	clock := memory.NewFixedClock(time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC))
	ctx := context.Background()

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
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	registerSite := &usecases.RegisterSite{Sites: sites, Events: publisher, Clock: clock}
	registerZone := &usecases.RegisterZone{Sites: sites, Zones: zones, Events: publisher, Clock: clock}
	registerAisle := &usecases.RegisterAisle{Zones: zones, Aisles: aisles, Events: publisher, Clock: clock}
	registerType := &usecases.RegisterLocationType{LocationTypes: locationTypes, Events: publisher, Clock: clock}
	registerSlot := &usecases.RegisterLocationSlot{
		Sites: sites, Zones: zones, Aisles: aisles, Slots: slots,
		LocationTypes: locationTypes, Rules: rules, Events: publisher, Clock: clock,
	}

	_, err := registerSite.Execute(ctx, "WH1", "Fulfilment Centre One")
	must(err)
	_, err = registerType.Execute(ctx, placement.PalletRack, placement.Storage, mustCapacity(1200, 2.4))
	must(err)
	_, err = registerType.Execute(ctx, placement.Shelf, placement.Storage, mustCapacity(60, 0.4))
	must(err)
	_, err = registerType.Execute(ctx, "DockDoor", placement.Dock, shared.Capacity{})
	must(err)

	_, err = registerZone.Execute(ctx, "WH1", "STOR", "AMB", shared.Ambient, false, nil, nil)
	must(err)
	_, err = registerZone.Execute(ctx, "WH1", "RCV", "AMB", shared.Ambient, false, nil, nil)
	must(err)
	_, err = registerZone.Execute(ctx, "WH1", "DOCK", "OB", shared.Ambient, false, nil, nil)
	must(err)

	_, err = registerAisle.Execute(ctx, "WH1-STOR-AMB", "A09", 9, shared.OneWay)
	must(err)
	_, err = registerAisle.Execute(ctx, "WH1-STOR-AMB", "A07", 7, shared.TwoWay)
	must(err)
	_, err = registerAisle.Execute(ctx, "WH1-RCV-AMB", "D01", 1, shared.TwoWay)
	must(err)
	_, err = registerAisle.Execute(ctx, "WH1-DOCK-OB", "D01", 1, shared.TwoWay)
	must(err)

	for _, s := range []struct {
		raw  string
		typ  string
		flow string
	}{
		{"WH1-STOR-AMB-A07-01-01-A", placement.PalletRack, ""},
		{"WH1-STOR-AMB-A07-02-01-A", placement.PalletRack, ""},
		{"WH1-STOR-AMB-A07-03-01-A", placement.PalletRack, ""},
		{"WH1-STOR-AMB-A07-03-02-A", placement.PalletRack, ""},
		{"WH1-STOR-AMB-A07-03-02-B", placement.PalletRack, ""},
		{"WH1-STOR-AMB-A09-01-01-A", placement.Shelf, ""},
		{"WH1-RCV-AMB-D01-01-01-A", placement.Shelf, ""},
		{"WH1-DOCK-OB-D01-01-01-A", "DockDoor", "Outbound"},
		{"WH1-DOCK-OB-D01-01-02-A", "DockDoor", "Inbound"},
	} {
		_, err = registerSlot.Execute(ctx, mustCode(s.raw), s.typ, shared.Capacity{}, s.flow, nil)
		must(err)
	}

	return inboundmcp.Deps{
		GetSiteLayout: &usecases.GetSiteLayout{Sites: sites, Zones: zones, Aisles: aisles, Slots: slots},
		GetZoneGrid:   &usecases.GetZoneGrid{Zones: zones, Aisles: aisles, Slots: slots},
		ListSites:     &usecases.ListSites{Sites: sites},
		ListLocationsByRole: &usecases.ListLocationsByRole{
			Sites: sites, Zones: zones, Slots: slots,
		},
		GetZoneTravelGraph: &usecases.GetZoneTravelGraph{
			Zones: zones, Aisles: aisles, Slots: slots, CrossAisles: crossAisles,
		},
		EstimateTravelDistance: &usecases.EstimateTravelDistance{
			Zones: zones, Aisles: aisles, Slots: slots, CrossAisles: crossAisles,
		},
	}
}

// newEvalHarness seeds the canonical eval state over a real Streamable HTTP
// server and connects a client session to it.
func newEvalHarness(t *testing.T) *evalHarness {
	t.Helper()

	h := &evalHarness{}
	h.session = wireSession(t, seedEvalLayout(t))
	return h
}

// wireSession builds a real Streamable HTTP server over the given deps and
// connects a client session to it, so evals see exactly what a model host
// sees after a JSON round-trip (not the in-process Go values).
func wireSession(t *testing.T, deps inboundmcp.Deps) *sdk.ClientSession {
	t.Helper()
	server := inboundmcp.NewServer(deps)
	httpSrv := httptest.NewServer(inboundmcp.Handler(server))
	t.Cleanup(httpSrv.Close)

	client := sdk.NewClient(&sdk.Implementation{Name: "eval-client", Version: "0.0.1"}, nil)
	transport := &sdk.StreamableClientTransport{Endpoint: httpSrv.URL}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("wire session connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// callTool invokes a tool and records the result for the Then steps.
func (h *evalHarness) callTool(ctx context.Context, name string, args map[string]any) error {
	res, err := h.session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	h.lastCallResult, h.lastCallErr = res, err
	h.lastCallContent = ""
	if res != nil {
		for _, c := range res.Content {
			if text, ok := c.(*sdk.TextContent); ok {
				h.lastCallContent += text.Text
			}
		}
	}
	return err
}
