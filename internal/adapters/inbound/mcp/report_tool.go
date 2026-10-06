package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- reports REST views (tool + client boundary) ------------------------------

// CatalogGrowthRowView is one row of the catalog-growth report as the MCP tool
// returns it and the reports REST client decodes it. Field tags match the
// reports service's JSON so the same struct round-trips both ways.
type CatalogGrowthRowView struct {
	Scope                   string `json:"scope"`
	DayBucket               string `json:"dayBucket"`
	SitesRegistered         int    `json:"sitesRegistered"`
	ZonesRegistered         int    `json:"zonesRegistered"`
	AislesRegistered        int    `json:"aislesRegistered"`
	LocationTypesRegistered int    `json:"locationTypesRegistered"`
	PlacementRulesDefined   int    `json:"placementRulesDefined"`
	SlotsRegistered         int    `json:"slotsRegistered"`
	SlotsDecommissioned     int    `json:"slotsDecommissioned"`
	BulkImports             int    `json:"bulkImports"`
	ImportRowsSubmitted     int    `json:"importRowsSubmitted"`
	ImportRowsImported      int    `json:"importRowsImported"`
	ImportRowsRejected      int    `json:"importRowsRejected"`
}

// CatalogGrowthReportView is the catalog-growth report body.
type CatalogGrowthReportView struct {
	Rows []CatalogGrowthRowView `json:"rows"`
}

// FreshnessView is the freshness-lag body.
type FreshnessView struct {
	LagSeconds float64 `json:"lagSeconds"`
}

// CatalogGrowthQuery is the filter set passed to the reports REST client.
type CatalogGrowthQuery struct {
	From        string
	To          string
	Scope       string
	Granularity string
}

// ReportsClient is the narrow port the MCP report tool depends on: a client of
// the facility-reports REST service. It is an interface so the tool can be
// unit-tested with a fake, and so the curated tool never talks to the analytical
// database directly — it goes through the reports REST surface, preserving the
// single read path (ADR-0010).
type ReportsClient interface {
	GetCatalogGrowth(ctx context.Context, q CatalogGrowthQuery) (CatalogGrowthReportView, error)
	GetFreshness(ctx context.Context) (FreshnessView, error)
}

// --- reports REST client ------------------------------------------------------

// ReportsRESTClient is the HTTP implementation of ReportsClient. Base URL and
// the *http.Client are injected so the composition root controls the target and
// timeouts, and tests can point it at an httptest server.
type ReportsRESTClient struct {
	baseURL string
	http    *http.Client
}

// NewReportsRESTClient constructs a ReportsRESTClient for the reports service at
// baseURL. A nil httpClient falls back to a client with a sane timeout.
func NewReportsRESTClient(baseURL string, httpClient *http.Client) *ReportsRESTClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &ReportsRESTClient{baseURL: baseURL, http: httpClient}
}

// GetCatalogGrowth calls GET /reports/catalog-growth with q as the query string.
func (c *ReportsRESTClient) GetCatalogGrowth(ctx context.Context, q CatalogGrowthQuery) (CatalogGrowthReportView, error) {
	vals := url.Values{}
	vals.Set("from", q.From)
	vals.Set("to", q.To)
	if q.Scope != "" {
		vals.Set("scope", q.Scope)
	}
	if q.Granularity != "" {
		vals.Set("granularity", q.Granularity)
	}
	var out CatalogGrowthReportView
	if err := c.getJSON(ctx, "/reports/catalog-growth?"+vals.Encode(), &out); err != nil {
		return CatalogGrowthReportView{}, err
	}
	return out, nil
}

// GetFreshness calls GET /reports/catalog-growth/freshness.
func (c *ReportsRESTClient) GetFreshness(ctx context.Context) (FreshnessView, error) {
	var out FreshnessView
	if err := c.getJSON(ctx, "/reports/catalog-growth/freshness", &out); err != nil {
		return FreshnessView{}, err
	}
	return out, nil
}

// getJSON performs a GET against baseURL+path and decodes a 2xx JSON body into
// out. A non-2xx response is an error.
func (c *ReportsRESTClient) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("reports client: build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("reports client: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("reports client: unexpected status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("reports client: decode: %w", err)
	}
	return nil
}

// Compile-time assertion that ReportsRESTClient satisfies the port.
var _ ReportsClient = (*ReportsRESTClient)(nil)

// --- get_facility_catalog_growth_report tool ----------------------------------

// CatalogGrowthToolInput is the tool's argument set (untrusted, from a model).
type CatalogGrowthToolInput struct {
	From        string `json:"from" jsonschema:"start of the window, inclusive, RFC3339 (required)"`
	To          string `json:"to" jsonschema:"end of the window, exclusive, RFC3339 (required)"`
	Scope       string `json:"scope" jsonschema:"optional exact-match scope filter: a site code (e.g. WH1), a zone id (e.g. WH1-STOR-AMB), or empty for the catalog-wide scope"`
	Granularity string `json:"granularity" jsonschema:"time bucket granularity; only 'day' is supported"`
}

// getCatalogGrowthReport is the tool handler: it validates the required window,
// delegates to the reports REST client, and returns the report view.
func (d Deps) getCatalogGrowthReport(ctx context.Context, in CatalogGrowthToolInput) (CatalogGrowthReportView, error) {
	return GetCatalogGrowthReportForTest(ctx, d.Reports, in)
}

// GetCatalogGrowthReportForTest is the tool's pure logic, factored out so it can
// be unit-tested with a fake ReportsClient independent of the MCP server wiring.
// It validates from/to and forwards the filters.
func GetCatalogGrowthReportForTest(ctx context.Context, client ReportsClient, in CatalogGrowthToolInput) (CatalogGrowthReportView, error) {
	if client == nil {
		return CatalogGrowthReportView{}, toolError(slugInternalError, "reports client not configured")
	}
	if in.From == "" || in.To == "" {
		return CatalogGrowthReportView{}, toolError(slugInvalidReportQuery, "from and to are required (RFC3339)")
	}
	q := CatalogGrowthQuery{}
	q.From = in.From
	q.To = in.To
	q.Scope = in.Scope
	q.Granularity = in.Granularity
	return client.GetCatalogGrowth(ctx, q)
}

// registerReportTool adds the curated read-only catalog-growth report tool. It
// is registered only when a reports client is configured (Deps.Reports != nil),
// so an MCP deployment without the reports service simply does not expose it.
func (d Deps) registerReportTool(server *mcp.Server) {
	if d.Reports == nil {
		return
	}
	readOnly := true
	addTool(server, &mcp.Tool{
		Name:        "get_facility_catalog_growth_report",
		Description: "Return the facility-layout 'Layout Catalog Growth & Change' report (slots registered/decommissioned, zones/aisles/location-types registered, placement rules defined, and bulk-import row tallies) for a time window, bucketed by day and optionally filtered by scope (a site code, a zone id, or the catalog-wide scope). Reads via the facility-reports REST service. " + toolErrorShape,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}, d.getCatalogGrowthReport)
}
