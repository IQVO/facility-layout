package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// toolErrorShape is the one sentence every tool description carries to
// document its error text (ADR-0033).
const toolErrorShape = "Errors read '<slug>: detail', where slug is the stable problem slug the REST API uses for the same condition (for example site-not-found or malformed-location-code); an unexpected failure is reported as internal-error."

// tracerName is the OTel instrumentation scope for MCP tool spans.
const tracerName = "github.com/claudioed/facility-layout/internal/adapters/inbound/mcp"

// Deps is everything the MCP tools need, injected by the composition root.
// It carries the SAME read use cases the HTTP adapter uses; the adapter never
// constructs an outbound adapter itself.
//
// facility-layout is read-only to the rest of the system, so every dependency
// here is a read-model use case. There is no write use case and no write tool.
type Deps struct {
	// GetSiteLayout is the existing read-model use case behind get_site_layout
	// and the layout resource, reused unchanged.
	GetSiteLayout *usecases.GetSiteLayout
	// GetZoneGrid is the existing read-model use case behind get_zone_grid.
	GetZoneGrid *usecases.GetZoneGrid
	// ListSites is the existing read use case behind list_sites.
	ListSites *usecases.ListSites
	// ListLocationsByRole backs list_functional_locations (ADR-0016).
	ListLocationsByRole *usecases.ListLocationsByRole
	// GetZoneTravelGraph backs get_zone_travel_graph (ADR-0017).
	GetZoneTravelGraph *usecases.GetZoneTravelGraph
	// EstimateTravelDistance backs estimate_travel_distance (ADR-0017).
	EstimateTravelDistance *usecases.EstimateTravelDistance
	// Reports is the client of the facility-reports REST service, backing the
	// curated get_facility_catalog_growth_report tool. When nil, that tool is
	// not registered (an MCP deployment without the reports service).
	Reports ReportsClient
}

// --- get_site_layout ----------------------------------------------------------

type siteLayoutInput struct {
	SiteCode string `json:"siteCode" jsonschema:"the code of the site whose layout to return, e.g. WH1"`
}

func (d Deps) getSiteLayout(ctx context.Context, in siteLayoutInput) (siteLayoutDTO, error) {
	if in.SiteCode == "" {
		return siteLayoutDTO{}, toolError(slugInvalidSiteCode, "siteCode is required")
	}
	layout, err := d.GetSiteLayout.Execute(ctx, in.SiteCode)
	if err != nil {
		return siteLayoutDTO{}, err
	}
	return toSiteLayoutDTO(layout), nil
}

// --- get_zone_grid ------------------------------------------------------------

type zoneGridInput struct {
	ZoneID string `json:"zoneId" jsonschema:"the id of the zone whose drawable grid to return, e.g. WH1-STOR-AMB"`
}

func (d Deps) getZoneGrid(ctx context.Context, in zoneGridInput) (zoneGridDTO, error) {
	if in.ZoneID == "" {
		return zoneGridDTO{}, toolError(slugInvalidZoneCode, "zoneId is required")
	}
	grid, err := d.GetZoneGrid.Execute(ctx, in.ZoneID)
	if err != nil {
		return zoneGridDTO{}, err
	}
	return toZoneGridDTO(grid), nil
}

// --- list_sites ---------------------------------------------------------------

type listSitesInput struct{}

type listSitesOutput struct {
	Sites []siteRef `json:"sites"`
}

func (d Deps) listSites(ctx context.Context, _ listSitesInput) (listSitesOutput, error) {
	sites, err := d.ListSites.Execute(ctx)
	if err != nil {
		return listSitesOutput{}, err
	}
	out := listSitesOutput{Sites: make([]siteRef, 0, len(sites))}
	for _, s := range sites {
		out.Sites = append(out.Sites, toSiteRef(s))
	}
	return out, nil
}

// --- list_functional_locations --------------------------------------------------

type listFunctionalLocationsInput struct {
	SiteCode string `json:"siteCode" jsonschema:"the code of the site to search, e.g. WH1"`
	Role     string `json:"role" jsonschema:"the LocationRole to filter on: Storage, Dock, Yard, WorkCenter, Drop, Staging, QC, Consolidation, or Shipping"`
}

type listFunctionalLocationsOutput struct {
	Locations []functionalLocationDTO `json:"locations"`
}

func (d Deps) listFunctionalLocations(ctx context.Context, in listFunctionalLocationsInput) (listFunctionalLocationsOutput, error) {
	if in.SiteCode == "" {
		return listFunctionalLocationsOutput{}, toolError(slugInvalidSiteCode, "siteCode is required")
	}
	role, err := placement.ParseLocationRole(in.Role)
	if err != nil {
		return listFunctionalLocationsOutput{}, err
	}
	slots, err := d.ListLocationsByRole.Execute(ctx, in.SiteCode, role)
	if err != nil {
		return listFunctionalLocationsOutput{}, err
	}
	out := listFunctionalLocationsOutput{Locations: make([]functionalLocationDTO, 0, len(slots))}
	for _, s := range slots {
		out.Locations = append(out.Locations, toFunctionalLocationDTO(s))
	}
	return out, nil
}

// --- get_zone_travel_graph -----------------------------------------------------

type zoneTravelGraphInput struct {
	ZoneID string `json:"zoneId" jsonschema:"the id of the zone whose travel graph to return, e.g. WH1-STOR-AMB"`
}

func (d Deps) getZoneTravelGraph(ctx context.Context, in zoneTravelGraphInput) (travelGraphDTO, error) {
	if in.ZoneID == "" {
		return travelGraphDTO{}, toolError(slugInvalidZoneCode, "zoneId is required")
	}
	view, err := d.GetZoneTravelGraph.Execute(ctx, in.ZoneID)
	if err != nil {
		return travelGraphDTO{}, err
	}
	return toTravelGraphDTO(view), nil
}

// --- estimate_travel_distance --------------------------------------------------

type estimateTravelDistanceInput struct {
	From string `json:"from" jsonschema:"the seven-segment location code to travel from, e.g. WH1-STOR-AMB-A07-01-01-A"`
	To   string `json:"to" jsonschema:"the seven-segment location code to travel to, e.g. WH1-STOR-AMB-A09-03-01-A"`
}

func (d Deps) estimateTravelDistance(ctx context.Context, in estimateTravelDistanceInput) (travelDistanceDTO, error) {
	if strings.TrimSpace(in.From) == "" {
		return travelDistanceDTO{}, toolError(slugMissingLocationCode, "from is required")
	}
	if strings.TrimSpace(in.To) == "" {
		return travelDistanceDTO{}, toolError(slugMissingLocationCode, "to is required")
	}
	from, err := shared.ParseLocationCode(in.From)
	if err != nil {
		return travelDistanceDTO{}, err
	}
	to, err := shared.ParseLocationCode(in.To)
	if err != nil {
		return travelDistanceDTO{}, err
	}
	distance, err := d.EstimateTravelDistance.Execute(ctx, from, to)
	if err != nil {
		return travelDistanceDTO{}, err
	}
	return toTravelDistanceDTO(distance), nil
}

// --- registration -------------------------------------------------------------

// registerTools adds every tool to the server, each wrapped so its handler
// runs inside an OTel span named "mcp.tool <name>".
//
// facility-layout is a read-only Open Host Service: every registered tool is a
// read tool. No write tool is registered (the map is consumed, not mutated).
func (d Deps) registerTools(server *mcp.Server) {
	readOnly := true

	addTool(server, &mcp.Tool{
		Name:        "list_sites",
		Description: "List every registered site on the warehouse map as {code, name}. Start here to discover which sites exist before drilling into a layout. " + toolErrorShape,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}, d.listSites)

	addTool(server, &mcp.Tool{
		Name:        "get_site_layout",
		Description: "Return one site's full drawable structure as a compact nested map: zones -> aisles -> slot codes, with each zone's temperature class and hazmat flag and each aisle's walk-order sequence hint. Use it to answer placement and travel questions for a site. " + toolErrorShape,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}, d.getSiteLayout)

	addTool(server, &mcp.Tool{
		Name:        "get_zone_grid",
		Description: "Return one zone's slots as a 2D grid: rows are levels, columns are (aisle, bay) pairs in walk order, each cell holds the location codes at that coordinate. Use it to reason about a single zone's rack layout in detail. " + toolErrorShape,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}, d.getZoneGrid)

	addTool(server, &mcp.Tool{
		Name:        "list_functional_locations",
		Description: "List a site's non-storage functional locations by role: dock doors, yard spots, work centers (pack/sort/QC/VAS stations), drop points, staging, consolidation, or shipping locations (ADR-0016). Use it to answer 'where are this site's dock doors' without walking the full site layout. " + toolErrorShape,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}, d.listFunctionalLocations)

	addTool(server, &mcp.Tool{
		Name:        "get_zone_travel_graph",
		Description: "Return one zone's travel graph as nodes (aisle/bay waypoints) and directed edges (weighted in metres, flagged estimated when derived from a zone's bay pitch rather than real aisle centreline geometry) (ADR-0017). Use it to inspect or render a zone's walkable topology. " + toolErrorShape,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}, d.getZoneTravelGraph)

	addTool(server, &mcp.Tool{
		Name:        "estimate_travel_distance",
		Description: "Compute the shortest travel distance, in metres, between two coded locations (ADR-0017). Within one zone it routes over the pure-domain travel graph, honouring one-way aisles and cross-aisle connections; the result is flagged estimated when any leg used a zone's bay pitch fallback instead of real geometry. Across zones there is no routed path (the travel graph is per zone): when BOTH locations have recorded position geometry it returns the straight-line distance between them, always flagged estimated; otherwise it refuses (does not guess). This reports MAP TOPOLOGY only, never travel time or congestion. " + toolErrorShape,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}, d.estimateTravelDistance)

	// Curated read-only data-product tool, registered only when the reports
	// client is configured (ADR-0010).
	d.registerReportTool(server)
}

// addTool registers one tool. It centralises the cross-cutting concern every
// tool shares: a span per call, mapping a handler error onto the span before
// returning it.
func addTool[In, Out any](
	server *mcp.Server,
	tool *mcp.Tool,
	handle func(context.Context, In) (Out, error),
) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		ctx, span := otel.Tracer(tracerName).Start(ctx, "mcp.tool "+tool.Name,
			trace.WithAttributes(
				attribute.String("mcp.tool.name", tool.Name),
			),
		)
		defer span.End()

		out, err := handle(ctx, in)
		if err != nil {
			// Every handler error leaves here as "<slug>: detail" (ADR-0033).
			// The span records the mapped text, so unexpected internals are
			// not exported through tracing either; mapError logs them.
			err = mapError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, zero, err
		}
		return nil, out, nil
	})
}
