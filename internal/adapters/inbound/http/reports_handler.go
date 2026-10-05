package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/claudioed/facility-layout/internal/analytics/report"
)

// ReportsHandlers is the inbound HTTP adapter for the facility-layout "Layout
// Catalog Growth & Change" data product's READER. It depends only on the
// read-model port (report.ReportStore); it never touches the OLTP use cases or
// the writer.
type ReportsHandlers struct {
	Store report.ReportStore
	// Readiness backs GET /readyz (ADR-0020 §graceful shutdown, mirroring
	// order-management's ADR-0025 verbatim). nil (the zero value of
	// *ReportsHandlers, every existing test) always reports ready -- see
	// Readiness.Ready's nil-receiver doc comment.
	Readiness *Readiness
}

// catalogRowDTO is the wire shape of one report row. It is a dedicated DTO so
// the read-model struct (report.Row) never leaks onto the API.
type catalogRowDTO struct {
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

// catalogReportDTO is the wire shape of a catalog-growth report response.
type catalogReportDTO struct {
	Rows []catalogRowDTO `json:"rows"`
}

// freshnessDTO is the wire shape of the freshness-lag response.
type freshnessDTO struct {
	LagSeconds float64 `json:"lagSeconds"`
}

// GetCatalogGrowth serves GET /reports/catalog-growth. from and to (RFC3339)
// are required; scope and granularity are optional (granularity defaults to
// day).
func (h *ReportsHandlers) GetCatalogGrowth(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	from, ok := parseRequiredTime(w, r, q.Get("from"), "from")
	if !ok {
		return
	}
	to, ok := parseRequiredTime(w, r, q.Get("to"), "to")
	if !ok {
		return
	}

	granularity := report.GranularityDay
	if g := q.Get("granularity"); g != "" {
		if g != string(report.GranularityDay) {
			writeReportBadRequest(w, r, "granularity must be 'day'")
			return
		}
		granularity = report.Granularity(g)
	}

	rep, err := h.Store.Query(r.Context(), report.ReportQuery{
		From:        from,
		To:          to,
		Scope:       q.Get("scope"),
		Granularity: granularity,
	})
	if err != nil {
		writeReportInternal(w, r, err)
		return
	}

	dto := catalogReportDTO{Rows: make([]catalogRowDTO, 0, len(rep.Rows))}
	for _, row := range rep.Rows {
		dto.Rows = append(dto.Rows, catalogRowDTO{
			Scope:                   row.Key.Scope,
			DayBucket:               row.Key.DayBucket.UTC().Format(time.RFC3339),
			SitesRegistered:         row.SitesRegistered,
			ZonesRegistered:         row.ZonesRegistered,
			AislesRegistered:        row.AislesRegistered,
			LocationTypesRegistered: row.LocationTypesRegistered,
			PlacementRulesDefined:   row.PlacementRulesDefined,
			SlotsRegistered:         row.SlotsRegistered,
			SlotsDecommissioned:     row.SlotsDecommissioned,
			BulkImports:             row.BulkImports,
			ImportRowsSubmitted:     row.ImportRowsSubmitted,
			ImportRowsImported:      row.ImportRowsImported,
			ImportRowsRejected:      row.ImportRowsRejected,
		})
	}
	writeJSON(w, http.StatusOK, dto)
}

// GetCatalogGrowthFreshness serves GET /reports/catalog-growth/freshness.
func (h *ReportsHandlers) GetCatalogGrowthFreshness(w http.ResponseWriter, r *http.Request) {
	lag, err := h.Store.FreshnessLag(r.Context())
	if err != nil {
		writeReportInternal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, freshnessDTO{LagSeconds: lag.Seconds()})
}

// GetReportsHealthz serves GET /healthz for the reports service.
func (h *ReportsHandlers) GetReportsHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// parseRequiredTime parses an RFC3339 timestamp, writing an RFC 7807 400 and
// returning ok=false when it is missing or malformed.
func parseRequiredTime(w http.ResponseWriter, r *http.Request, raw, name string) (time.Time, bool) {
	if raw == "" {
		writeReportBadRequest(w, r, "query parameter '"+name+"' is required (RFC3339)")
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeReportBadRequest(w, r, "query parameter '"+name+"' must be an RFC3339 timestamp")
		return time.Time{}, false
	}
	return t, true
}

// writeReportBadRequest writes the reports service's RFC 7807 400, reusing the
// service-wide problem writer.
func writeReportBadRequest(w http.ResponseWriter, r *http.Request, detail string) {
	writeProblem(w, http.StatusBadRequest,
		problemInfo{"invalid-report-query", "The report query is malformed or missing a required parameter"},
		detail, r.URL.Path)
}

// writeReportInternal writes the reports service's RFC 7807 500.
func writeReportInternal(w http.ResponseWriter, r *http.Request, err error) {
	writeProblem(w, http.StatusInternalServerError,
		problemInfo{"report-store-error", "The report could not be served"},
		err.Error(), r.URL.Path)
}

// NewReportsRouter builds the chi router for the facility-reports reader
// service. A nil logger falls back to slog.Default(). The router carries
// the same otelchi tracing and otelchimetric HTTP metrics as the main
// router (ADR-0012, in the same middleware order server.go uses), under
// its own service name — the chart already injects OTEL_* env into the
// reports pod, so these spans/metrics are actually collected.
func NewReportsRouter(h *ReportsHandlers, logger *slog.Logger, opts ...RouterOption) *chi.Mux {
	if logger == nil {
		logger = slog.Default()
	}

	cfg := routerConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	// Same middleware order as the main router (server.go): tracing before
	// request logging so the logged line sits inside the request span.
	r.Use(otelchi.Middleware(cfg.serviceName, otelchi.WithChiRoutes(r)))
	metricCfg := otelchimetric.NewBaseConfig(cfg.serviceName)
	r.Use(otelchimetric.NewServerRequestDuration(metricCfg))
	r.Use(otelchimetric.NewServerActiveRequests(metricCfg))
	r.Use(RequestLogger(logger))
	r.Use(middleware.Recoverer)

	r.Get("/healthz", h.GetReportsHealthz)
	r.Get("/readyz", HandleReadyz(h.Readiness))

	r.Get("/reports/catalog-growth", h.GetCatalogGrowth)
	r.Get("/reports/catalog-growth/freshness", h.GetCatalogGrowthFreshness)

	return r
}
