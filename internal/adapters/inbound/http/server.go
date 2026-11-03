package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/claudioed/facility-layout/internal/application/usecases"
)

// DefaultServiceName is the service name reported on this adapter's spans
// and metrics when the composition root does not override it.
const DefaultServiceName = "facility-layout"

// RouterOption tunes NewRouter.
type RouterOption func(*routerConfig)

type routerConfig struct {
	serviceName string
}

// WithServiceName sets the service name otelchi stamps on HTTP spans and
// metrics — normally OTEL_SERVICE_NAME, resolved by the composition root.
func WithServiceName(name string) RouterOption {
	return func(cfg *routerConfig) {
		if name != "" {
			cfg.serviceName = name
		}
	}
}

// Server holds every use case the HTTP adapter depends on.
type Server struct {
	RegisterSite *usecases.RegisterSite
	GetSite      *usecases.GetSite
	ListSites    *usecases.ListSites
	// ChangeSiteTransferCapability backs PUT
	// /sites/{siteCode}/transfer-capability (ADR-0035); the GET reads the
	// same aggregate through GetSite.
	ChangeSiteTransferCapability *usecases.ChangeSiteTransferCapability
	// ChangeSiteRole backs PUT /sites/{siteCode}/role (ADR-0037).
	ChangeSiteRole *usecases.ChangeSiteRole
	// ListSiteRoleTransferDefaults backs GET /site-roles/transfer-defaults
	// (ADR-0037).
	ListSiteRoleTransferDefaults *usecases.ListSiteRoleTransferDefaults
	RegisterZone                 *usecases.RegisterZone
	GetZone                      *usecases.GetZone
	ListZones                    *usecases.ListZones
	RegisterAisle                *usecases.RegisterAisle
	GetAisle                     *usecases.GetAisle
	ListAisles                   *usecases.ListAisles
	RegisterLocationType         *usecases.RegisterLocationType
	GetLocationType              *usecases.GetLocationType
	ListLocationTypes            *usecases.ListLocationTypes
	DefinePlacementRule          *usecases.DefinePlacementRule
	GetPlacementRule             *usecases.GetPlacementRule
	ListPlacementRules           *usecases.ListPlacementRules
	RegisterLocationSlot         *usecases.RegisterLocationSlot
	GetLocationSlot              *usecases.GetLocationSlot
	GetLocationClassification    *usecases.GetLocationClassification
	ListLocationsByRole          *usecases.ListLocationsByRole
	DecommissionLocationSlot     *usecases.DecommissionLocationSlot
	ImportFacilityLayout         *usecases.ImportFacilityLayout
	GetSiteLayout                *usecases.GetSiteLayout
	GetZoneGrid                  *usecases.GetZoneGrid
	SetLocationGeometry          *usecases.SetLocationGeometry
	DeclareLocationHandlingUnit  *usecases.DeclareLocationHandlingUnit
	SetAisleGeometry             *usecases.SetAisleGeometry
	RegisterFixedStructure       *usecases.RegisterFixedStructure
	ListFixedStructures          *usecases.ListFixedStructures
	RegisterCrossAisle           *usecases.RegisterCrossAisle
	ListCrossAisles              *usecases.ListCrossAisles
	GetZoneTravelGraph           *usecases.GetZoneTravelGraph
	EstimateTravelDistance       *usecases.EstimateTravelDistance

	// IdempotencyPool, when non-nil, wires RequireIdempotencyKey (see
	// idempotency.go) onto every true resource-creation POST below. A
	// nil pool means "no transactional Postgres backing wired" (the
	// in-memory dev/test configuration) — the middleware needs a real
	// pgxpool.Pool to begin its own transaction, so it is simply not
	// applied in that case, mirroring this codebase's existing
	// convention for every other optional Postgres-backed capability
	// (UnitOfWork, the outbox relay). Mirrors order-management's
	// identically-named field (PR #105, ADR 0023).
	IdempotencyPool *pgxpool.Pool

	// Readiness backs GET /readyz (ADR-0020 §graceful shutdown, mirroring
	// order-management's ADR-0025 verbatim): flipped to not-ready as the
	// FIRST step of the composition root's shutdown sequence, before
	// anything else stops, so a Kubernetes readinessProbe has a chance to
	// observe the flip and stop routing new traffic during the drain
	// window that follows. nil (the zero value of *Server, every existing
	// test) always reports ready -- see Readiness.Ready's nil-receiver
	// doc comment.
	Readiness *Readiness
}

// NewRouter builds the chi router for every endpoint in CLAUDE.md's REST
// API section.
//
// Three single-resource GETs (a zone, an aisle, a location type, a
// placement rule) are additive to that list: without them the Location
// header this service is required to set on every 201 would point at a URL
// with no representation, which is not REST maturity level 2.
//
// logger drives per-request structured logging; a nil logger falls back to
// slog.Default().
//
// opts tune the OpenTelemetry instrumentation; without any, the router is
// still traced and metered, under DefaultServiceName.
func NewRouter(s *Server, logger *slog.Logger, opts ...RouterOption) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	cfg := routerConfig{serviceName: DefaultServiceName}
	for _, opt := range opts {
		opt(&cfg)
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	// Tracing goes on before request logging so the logged line already
	// sits inside the request span and picks up its trace_id/span_id.
	// WithChiRoutes makes the span name the route PATTERN
	// (GET /locations/{locationCode}), not the raw path, which is what
	// keeps span names from exploding into one per location code.
	r.Use(otelchi.Middleware(cfg.serviceName, otelchi.WithChiRoutes(r)))
	metricCfg := otelchimetric.NewBaseConfig(cfg.serviceName)
	// http.server.request.duration, per the OTel HTTP semantic
	// conventions — emitted by otelchi's metric middleware rather than
	// hand-rolled here, so the bucket boundaries and attributes match
	// what every other OTel HTTP instrumentation produces.
	r.Use(otelchimetric.NewServerRequestDuration(metricCfg))
	r.Use(otelchimetric.NewServerActiveRequests(metricCfg))
	r.Use(RequestLogger(logger))
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware(), rejectNUL)

	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", HandleReadyz(s.Readiness))

	// idempotent wraps a chi router handler behind RequireIdempotencyKey
	// when a Postgres-backed IdempotencyPool is configured — EVERY POST in
	// this router is wrapped this way (REST API guideline 2.1, ADR 0044), so
	// a client retry after a lost response never repeats the effect: the
	// resource-creation POSTs, the bulk layout import
	// (POST /location-imports) and the decommission transition
	// (POST /locations/{locationCode}/decommissionings). PUT routes are
	// idempotent by method and take no key. IdempotencyPool nil (in-memory
	// dev/test configuration, no transactional Postgres backing) is a no-op
	// passthrough, mirroring order-management's identical nil convention.
	idempotent := func(r chi.Router) chi.Router {
		if s.IdempotencyPool != nil {
			return r.With(RequireIdempotencyKey(s.IdempotencyPool))
		}
		return r
	}

	r.Route("/sites", func(r chi.Router) {
		idempotent(r).Post("/", s.handleRegisterSite)
		r.Get("/", s.handleListSites)
		r.Get("/{siteCode}", s.handleGetSite)
		r.Get("/{siteCode}/layout", s.handleGetSiteLayout)
		r.Get("/{siteCode}/locations", s.handleListLocationsByRole)
		// PUT /sites/{siteCode}/transfer-capability replaces a full
		// snapshot of an EXISTING site's capability: naturally idempotent
		// (a repeat is a no-op, ADR-0035), so it is not wrapped by the
		// Idempotency-Key middleware, like the geometry PUTs.
		r.Get("/{siteCode}/transfer-capability", s.handleGetSiteTransferCapability)
		r.Put("/{siteCode}/transfer-capability", s.handleChangeSiteTransferCapability)
		r.Put("/{siteCode}/role", s.handleChangeSiteRole) // ADR-0037: a repeat is a no-op, no Idempotency-Key
		idempotent(r).Post("/{siteCode}/zones", s.handleRegisterZone)
		r.Get("/{siteCode}/zones", s.handleListZones)
		idempotent(r).Post("/{siteCode}/structures", s.handleRegisterFixedStructure)
		r.Get("/{siteCode}/structures", s.handleListFixedStructures)
	})

	r.Route("/zones", func(r chi.Router) {
		r.Get("/{zoneId}", s.handleGetZone)
		r.Get("/{zoneId}/grid", s.handleGetZoneGrid)
		idempotent(r).Post("/{zoneId}/aisles", s.handleRegisterAisle)
		r.Get("/{zoneId}/aisles", s.handleListAisles)
		r.Get("/{zoneId}/aisles/{aisleCode}", s.handleGetAisle)
		r.Put("/{zoneId}/aisles/{aisleCode}/geometry", s.handleSetAisleGeometry)
		idempotent(r).Post("/{zoneId}/cross-aisles", s.handleRegisterCrossAisle)
		r.Get("/{zoneId}/cross-aisles", s.handleListCrossAisles)
		r.Get("/{zoneId}/travel-graph", s.handleGetZoneTravelGraph)
	})

	r.Get("/distance", s.handleEstimateTravelDistance)
	r.Get("/site-roles/transfer-defaults", s.handleListSiteRoleTransferDefaults)
	// POST /location-imports is the batch-import job noun (guideline 1.3 e):
	// partial-success semantics (ADR-0006), idempotent through the key.
	idempotent(r).Post("/location-imports", s.handleImportFacilityLayout)

	r.Route("/location-types", func(r chi.Router) {
		idempotent(r).Post("/", s.handleRegisterLocationType)
		r.Get("/", s.handleListLocationTypes)
		r.Get("/{name}", s.handleGetLocationType)
	})

	r.Route("/placement-rules", func(r chi.Router) {
		idempotent(r).Post("/", s.handleDefinePlacementRule)
		r.Get("/", s.handleListPlacementRules)
		r.Get("/{ruleId}", s.handleGetPlacementRule)
	})

	r.Route("/locations", func(r chi.Router) {
		idempotent(r).Post("/", s.handleRegisterLocationSlot)
		r.Get("/{locationCode}", s.handleGetLocationSlot)
		r.Get("/{locationCode}/classification", s.handleGetLocationClassification)
		// POST /locations/{locationCode}/decommissionings is the
		// decommission transition resource (guideline 1.3 d, ADR 0044).
		idempotent(r).Post("/{locationCode}/decommissionings", s.handleDecommissionLocationSlot)
		s.mountSlotUpdates(r)
	})

	return r
}

// mountSlotUpdates mounts the PUT updates on an existing slot: geometry
// (ADR-0017) and handling unit (ADR-0040). PUTs are naturally idempotent
// (same body, same state), so neither takes an Idempotency-Key.
func (s *Server) mountSlotUpdates(r chi.Router) {
	r.Put("/{locationCode}/geometry", s.handleSetLocationGeometry)
	r.Put("/{locationCode}/handling-unit", s.handleDeclareLocationHandlingUnit)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{statusField: "ok"})
}

// --------------------------------------------------------------- writing ---

func decodeJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	if err := json.NewDecoder(r.Body).Decode(dest); err != nil {
		writeProblem(w, http.StatusBadRequest, problemInfo{slugMalformedRequestBody, "The request body is not valid JSON"}, err.Error(), r.URL.Path)
		return false
	}
	return true
}

// writeError writes a domain/application error as an RFC 7807
// (application/problem+json) response.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	writeProblem(w, statusFor(err), problemFor(err), err.Error(), r.URL.Path)
}

func writeProblem(w http.ResponseWriter, status int, info problemInfo, detail, instance string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problemDetails{
		Type:     problemBaseURI + info.slug,
		Title:    info.title,
		Status:   status,
		Detail:   detail,
		Instance: instance,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// corsMiddleware allows the warehouse-console browser SPA (and this
// service's own future MFE remote dev origin) to call this API directly
// from the browser. CORS_ALLOWED_ORIGINS overrides the local-dev default
// (comma-separated) for staging/prod deployments.
func corsMiddleware() func(http.Handler) http.Handler {
	origins := []string{"http://localhost:5173", "http://localhost:5186"}
	if v := os.Getenv("CORS_ALLOWED_ORIGINS"); v != "" {
		origins = strings.Split(v, ",")
	}
	return cors.Handler(cors.Options{
		AllowedOrigins: origins,
		AllowedMethods: []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete},
		// Idempotency-Key MUST be allowed: every resource-creation POST is
		// wrapped by RequireIdempotencyKey (ADR-0019), and a preflight that
		// omits the header from Access-Control-Allow-Headers makes the
		// browser drop it — the console's create forms then answer 400
		// idempotency-key-required against a Postgres-backed API. Authorization
		// stays listed even though REST auth was removed (ADR-0015): the
		// header is harmless to allow and keeps the middleware stable if a
		// gateway in front ever re-introduces it.
		AllowedHeaders:   []string{"Content-Type", "Authorization", IdempotencyKeyHeader},
		AllowCredentials: false,
		MaxAge:           300,
	})
}
