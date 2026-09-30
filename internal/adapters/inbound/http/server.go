package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/structure"
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
	RegisterSite              *usecases.RegisterSite
	GetSite                   *usecases.GetSite
	ListSites                 *usecases.ListSites
	RegisterZone              *usecases.RegisterZone
	GetZone                   *usecases.GetZone
	ListZones                 *usecases.ListZones
	RegisterAisle             *usecases.RegisterAisle
	GetAisle                  *usecases.GetAisle
	ListAisles                *usecases.ListAisles
	RegisterLocationType      *usecases.RegisterLocationType
	GetLocationType           *usecases.GetLocationType
	ListLocationTypes         *usecases.ListLocationTypes
	DefinePlacementRule       *usecases.DefinePlacementRule
	GetPlacementRule          *usecases.GetPlacementRule
	ListPlacementRules        *usecases.ListPlacementRules
	RegisterLocationSlot      *usecases.RegisterLocationSlot
	GetLocationSlot           *usecases.GetLocationSlot
	GetLocationClassification *usecases.GetLocationClassification
	ListLocationsByRole       *usecases.ListLocationsByRole
	DecommissionLocationSlot  *usecases.DecommissionLocationSlot
	ImportFacilityLayout      *usecases.ImportFacilityLayout
	GetSiteLayout             *usecases.GetSiteLayout
	GetZoneGrid               *usecases.GetZoneGrid
	SetLocationGeometry       *usecases.SetLocationGeometry
	SetAisleGeometry          *usecases.SetAisleGeometry
	RegisterFixedStructure    *usecases.RegisterFixedStructure
	ListFixedStructures       *usecases.ListFixedStructures
	RegisterCrossAisle        *usecases.RegisterCrossAisle
	GetZoneTravelGraph        *usecases.GetZoneTravelGraph
	EstimateTravelDistance    *usecases.EstimateTravelDistance

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
	r.Use(corsMiddleware())

	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", HandleReadyz(s.Readiness))

	// idempotent wraps a chi router handler behind RequireIdempotencyKey
	// when a Postgres-backed IdempotencyPool is configured — every true
	// resource-creation POST in this router (server-generated or
	// natural-key id, caller does not supply the resource's identity in
	// a way that makes a retry naturally safe) is wrapped this way, so a
	// client retry after a lost response never double-creates. Bulk
	// import (POST /locations/import) and decommission (POST
	// /locations/{locationCode}/decommission) are deliberately excluded
	// — see docs/docs/adr/0019-idempotency-key-middleware.md. IdempotencyPool
	// nil (in-memory dev/test configuration, no transactional Postgres
	// backing) is a no-op passthrough, mirroring order-management's
	// identical nil convention.
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
		r.Get("/{zoneId}/travel-graph", s.handleGetZoneTravelGraph)
	})

	r.Get("/distance", s.handleEstimateTravelDistance)

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
		// POST /locations/import is a BULK/batch import over many rows
		// (partial-success semantics, ADR-0006) — deliberately excluded,
		// see the idempotent helper's doc comment above.
		r.Post("/import", s.handleImportFacilityLayout)
		r.Get("/{locationCode}", s.handleGetLocationSlot)
		r.Get("/{locationCode}/classification", s.handleGetLocationClassification)
		// POST /locations/{locationCode}/decommission acts on an
		// EXISTING caller-supplied resource, not a creation — excluded.
		r.Post("/{locationCode}/decommission", s.handleDecommissionLocationSlot)
		r.Put("/{locationCode}/geometry", s.handleSetLocationGeometry)
	})

	return r
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------- sites ----

func (s *Server) handleRegisterSite(w http.ResponseWriter, r *http.Request) {
	var req registerSiteRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	registered, err := s.RegisterSite.Execute(r.Context(), req.SiteCode, req.Name)
	if err != nil {
		writeError(w, r, err)
		return
	}

	w.Header().Set("Location", "/sites/"+registered.Code())
	writeJSON(w, http.StatusCreated, toSiteResponse(registered))
}

func (s *Server) handleListSites(w http.ResponseWriter, r *http.Request) {
	sites, err := s.ListSites.Execute(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]siteResponse, 0, len(sites))
	for _, site := range sites {
		out = append(out, toSiteResponse(site))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetSite(w http.ResponseWriter, r *http.Request) {
	found, err := s.GetSite.Execute(r.Context(), chi.URLParam(r, "siteCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toSiteResponse(found))
}

// handleListLocationsByRole answers "where are this site's dock doors" (or
// any other LocationRole) without walking the full nested site layout
// (ADR-0016). The role query parameter is required.
func (s *Server) handleListLocationsByRole(w http.ResponseWriter, r *http.Request) {
	role, err := placement.ParseLocationRole(r.URL.Query().Get("role"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	slots, err := s.ListLocationsByRole.Execute(r.Context(), chi.URLParam(r, "siteCode"), role)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]locationSlotResponse, 0, len(slots))
	for _, sl := range slots {
		out = append(out, toLocationSlotResponse(sl))
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------- zones ----

func (s *Server) handleRegisterZone(w http.ResponseWriter, r *http.Request) {
	var req registerZoneRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	temperatureClass, err := shared.ParseTemperatureClass(req.TemperatureClass)
	if err != nil {
		writeError(w, r, err)
		return
	}

	registered, err := s.RegisterZone.Execute(r.Context(), chi.URLParam(r, "siteCode"), req.AreaCode, req.ZoneCode, temperatureClass, req.Hazmat)
	if err != nil {
		writeError(w, r, err)
		return
	}

	w.Header().Set("Location", "/zones/"+registered.ID())
	writeJSON(w, http.StatusCreated, toZoneResponse(registered))
}

func (s *Server) handleListZones(w http.ResponseWriter, r *http.Request) {
	zones, err := s.ListZones.Execute(r.Context(), chi.URLParam(r, "siteCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]zoneResponse, 0, len(zones))
	for _, z := range zones {
		out = append(out, toZoneResponse(z))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetZone(w http.ResponseWriter, r *http.Request) {
	found, err := s.GetZone.Execute(r.Context(), chi.URLParam(r, "zoneId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toZoneResponse(found))
}

// --------------------------------------------------------------- aisles ----

func (s *Server) handleRegisterAisle(w http.ResponseWriter, r *http.Request) {
	var req registerAisleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.SequenceHint == nil {
		writeError(w, r, errMissingSequenceHint)
		return
	}

	direction, err := shared.ParseDirection(req.Direction)
	if err != nil {
		writeError(w, r, err)
		return
	}

	zoneID := chi.URLParam(r, "zoneId")
	registered, err := s.RegisterAisle.Execute(r.Context(), zoneID, req.AisleCode, *req.SequenceHint, direction)
	if err != nil {
		writeError(w, r, err)
		return
	}

	w.Header().Set("Location", "/zones/"+zoneID+"/aisles/"+registered.AisleCode())
	writeJSON(w, http.StatusCreated, toAisleResponse(registered))
}

func (s *Server) handleListAisles(w http.ResponseWriter, r *http.Request) {
	aisles, err := s.ListAisles.Execute(r.Context(), chi.URLParam(r, "zoneId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]aisleResponse, 0, len(aisles))
	for _, a := range aisles {
		out = append(out, toAisleResponse(a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetAisle(w http.ResponseWriter, r *http.Request) {
	aisleID := chi.URLParam(r, "zoneId") + "-" + chi.URLParam(r, "aisleCode")
	found, err := s.GetAisle.Execute(r.Context(), aisleID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAisleResponse(found))
}

// -------------------------------------------------------- location types ---

func (s *Server) handleRegisterLocationType(w http.ResponseWriter, r *http.Request) {
	var req registerLocationTypeRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	roleName := req.Role
	if roleName == "" {
		roleName = string(placement.Storage)
	}
	role, err := placement.ParseLocationRole(roleName)
	if err != nil {
		writeError(w, r, err)
		return
	}

	capacity := shared.Capacity{}
	if req.DefaultCapacity.MaxWeightKg != 0 || req.DefaultCapacity.MaxVolumeM3 != 0 {
		capacity, err = shared.NewCapacity(req.DefaultCapacity.MaxWeightKg, req.DefaultCapacity.MaxVolumeM3)
		if err != nil {
			writeError(w, r, err)
			return
		}
	}

	registered, err := s.RegisterLocationType.Execute(r.Context(), req.Name, role, capacity)
	if err != nil {
		writeError(w, r, err)
		return
	}

	w.Header().Set("Location", "/location-types/"+registered.Name())
	writeJSON(w, http.StatusCreated, toLocationTypeResponse(registered))
}

func (s *Server) handleListLocationTypes(w http.ResponseWriter, r *http.Request) {
	types, err := s.ListLocationTypes.Execute(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]locationTypeResponse, 0, len(types))
	for _, t := range types {
		out = append(out, toLocationTypeResponse(t))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetLocationType(w http.ResponseWriter, r *http.Request) {
	found, err := s.GetLocationType.Execute(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLocationTypeResponse(found))
}

// -------------------------------------------------------- placement rules --

func (s *Server) handleDefinePlacementRule(w http.ResponseWriter, r *http.Request) {
	var req definePlacementRuleRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	effect, err := placement.ParseEffect(req.Effect)
	if err != nil {
		writeError(w, r, err)
		return
	}
	predicate, err := placement.NewZonePredicate(req.Zone.ZoneCode, shared.TemperatureClass(req.Zone.TemperatureClass), req.Zone.Hazmat)
	if err != nil {
		writeError(w, r, err)
		return
	}

	defined, err := s.DefinePlacementRule.Execute(r.Context(), req.RuleID, req.LocationType, effect, predicate)
	if err != nil {
		writeError(w, r, err)
		return
	}

	w.Header().Set("Location", "/placement-rules/"+defined.ID())
	writeJSON(w, http.StatusCreated, toPlacementRuleResponse(defined))
}

func (s *Server) handleListPlacementRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.ListPlacementRules.Execute(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]placementRuleResponse, 0, len(rules))
	for _, rule := range rules {
		out = append(out, toPlacementRuleResponse(rule))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetPlacementRule(w http.ResponseWriter, r *http.Request) {
	found, err := s.GetPlacementRule.Execute(r.Context(), chi.URLParam(r, "ruleId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toPlacementRuleResponse(found))
}

// ------------------------------------------------------- location slots ----

func (s *Server) handleRegisterLocationSlot(w http.ResponseWriter, r *http.Request) {
	var req registerLocationSlotRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	code, err := shared.ParseLocationCode(req.LocationCode)
	if err != nil {
		writeError(w, r, err)
		return
	}

	override := shared.Capacity{}
	if req.CapacityOverride != nil {
		override, err = shared.NewCapacity(req.CapacityOverride.MaxWeightKg, req.CapacityOverride.MaxVolumeM3)
		if err != nil {
			writeError(w, r, err)
			return
		}
	}

	registered, err := s.RegisterLocationSlot.Execute(r.Context(), code, req.LocationType, override, req.DockFlow, req.Activities)
	if err != nil {
		writeError(w, r, err)
		return
	}

	w.Header().Set("Location", "/locations/"+registered.Code().String())
	writeJSON(w, http.StatusCreated, toLocationSlotResponse(registered))
}

func (s *Server) handleGetLocationSlot(w http.ResponseWriter, r *http.Request) {
	code, err := shared.ParseLocationCode(chi.URLParam(r, "locationCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	found, err := s.GetLocationSlot.Execute(r.Context(), code)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLocationSlotResponse(found))
}

// handleGetLocationClassification resolves a LocationSlot's LocationCode to
// its parent Zone and returns that Zone's Hazmat/TemperatureClass
// attributes — the Open Host Service / Published Language realization that
// lets inventory-storage (and future consumers) validate placement of a
// classified product without duplicating Zone data of its own. Zone
// remains the sole domain aggregate that owns these attributes; this
// handler only reads and joins.
func (s *Server) handleGetLocationClassification(w http.ResponseWriter, r *http.Request) {
	code, err := shared.ParseLocationCode(chi.URLParam(r, "locationCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	found, err := s.GetLocationClassification.Execute(r.Context(), code)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLocationClassificationResponse(found))
}

func (s *Server) handleDecommissionLocationSlot(w http.ResponseWriter, r *http.Request) {
	code, err := shared.ParseLocationCode(chi.URLParam(r, "locationCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.DecommissionLocationSlot.Execute(r.Context(), code); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleImportFacilityLayout answers 200 OK, not 201: a bulk import is a
// partial-success report over many rows, not the creation of one
// addressable resource, so there is no single Location to hand back. The
// per-row outcome is in the body.
func (s *Server) handleImportFacilityLayout(w http.ResponseWriter, r *http.Request) {
	var req []importRowRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	rows := make([]usecases.ImportRow, 0, len(req))
	for _, row := range req {
		rows = append(rows, toImportRow(row))
	}

	report, err := s.ImportFacilityLayout.Execute(r.Context(), rows)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toImportReportResponse(report))
}

// --------------------------------------------- "draw the warehouse" reads --

func (s *Server) handleGetSiteLayout(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format != "" && format != "json" && format != "svg" {
		writeProblem(w, http.StatusBadRequest, problemInfo{"invalid-layout-format", "Layout format must be json or svg"}, "layout format must be \"json\" or \"svg\", got "+strconv.Quote(format), r.URL.Path)
		return
	}

	layout, err := s.GetSiteLayout.Execute(r.Context(), chi.URLParam(r, "siteCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}

	if format == "svg" {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(renderLayoutSVG(layout)))
		return
	}

	writeJSON(w, http.StatusOK, toSiteLayoutResponse(layout))
}

func (s *Server) handleGetZoneGrid(w http.ResponseWriter, r *http.Request) {
	grid, err := s.GetZoneGrid.Execute(r.Context(), chi.URLParam(r, "zoneId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toZoneGridResponse(grid))
}

// --------------------------------------------------------- geometry (ADR-0017) --

func (s *Server) handleSetLocationGeometry(w http.ResponseWriter, r *http.Request) {
	code, err := shared.ParseLocationCode(chi.URLParam(r, "locationCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req setLocationGeometryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	position, err := fromPoint3DRequest(req.Position)
	if err != nil {
		writeError(w, r, err)
		return
	}
	dimensions, err := fromDimensionsRequest(req.Dimensions)
	if err != nil {
		writeError(w, r, err)
		return
	}
	updated, err := s.SetLocationGeometry.Execute(r.Context(), code, position, dimensions, req.PickSequence)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLocationSlotResponse(updated))
}

func (s *Server) handleSetAisleGeometry(w http.ResponseWriter, r *http.Request) {
	aisleID := chi.URLParam(r, "zoneId") + "-" + chi.URLParam(r, "aisleCode")
	var req setAisleGeometryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	centreline, err := fromSegmentRequest(req.Centreline)
	if err != nil {
		writeError(w, r, err)
		return
	}
	updated, err := s.SetAisleGeometry.Execute(r.Context(), aisleID, centreline)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAisleResponse(updated))
}

// handleRegisterFixedStructure registers a site-scoped physical obstacle
// (wall, column, office, conveyor, or other). id is caller-supplied, the
// same pattern PlacementRule ids follow: when the caller omits it, a UUID
// is minted here.
func (s *Server) handleRegisterFixedStructure(w http.ResponseWriter, r *http.Request) {
	siteCode := chi.URLParam(r, "siteCode")
	var req registerFixedStructureRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	kind, err := structure.ParseKind(req.Kind)
	if err != nil {
		writeError(w, r, err)
		return
	}
	footprint, err := fromRectRequest(req.Footprint)
	if err != nil {
		writeError(w, r, err)
		return
	}
	id := req.ID
	if id == "" {
		id = uuid.NewString()
	}

	registered, err := s.RegisterFixedStructure.Execute(r.Context(), id, siteCode, kind, footprint, req.Label)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/sites/"+siteCode+"/structures/"+registered.ID())
	writeJSON(w, http.StatusCreated, toFixedStructureResponse(registered))
}

func (s *Server) handleListFixedStructures(w http.ResponseWriter, r *http.Request) {
	structures, err := s.ListFixedStructures.Execute(r.Context(), chi.URLParam(r, "siteCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]fixedStructureResponse, 0, len(structures))
	for _, f := range structures {
		out = append(out, toFixedStructureResponse(f))
	}
	writeJSON(w, http.StatusOK, out)
}

// --------------------------------------------------- travel graph (ADR-0017) --

// handleRegisterCrossAisle declares a zone-scoped connection between two of
// its aisles at a bay ordinal, adding an edge to the zone's travel graph.
func (s *Server) handleRegisterCrossAisle(w http.ResponseWriter, r *http.Request) {
	zoneID := chi.URLParam(r, "zoneId")
	var req registerCrossAisleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	registered, err := s.RegisterCrossAisle.Execute(r.Context(), zoneID, req.FromAisle, req.ToAisle, req.AtBay)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toCrossAisleResponse(registered))
}

// handleGetZoneTravelGraph returns one zone's travel graph as nodes +
// edges, built fresh from its aisles, slots, and cross-aisles.
func (s *Server) handleGetZoneTravelGraph(w http.ResponseWriter, r *http.Request) {
	view, err := s.GetZoneTravelGraph.Execute(r.Context(), chi.URLParam(r, "zoneId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTravelGraphResponse(view))
}

// handleEstimateTravelDistance answers the shortest travel distance
// between two coded locations over the pure-domain travel graph. A
// malformed code is a 400 (it could never identify a location); two
// locations in different zones is a 422 (ErrNoRouteBetweenZones) — this
// phase's graph does not connect zones, so the endpoint refuses rather
// than guessing.
func (s *Server) handleEstimateTravelDistance(w http.ResponseWriter, r *http.Request) {
	from, err := shared.ParseLocationCode(r.URL.Query().Get("from"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	to, err := shared.ParseLocationCode(r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	distance, err := s.EstimateTravelDistance.Execute(r.Context(), from, to)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTravelDistanceResponse(distance))
}

// --------------------------------------------------------------- writing ---

func decodeJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	if err := json.NewDecoder(r.Body).Decode(dest); err != nil {
		writeProblem(w, http.StatusBadRequest, problemInfo{"malformed-request-body", "The request body is not valid JSON"}, err.Error(), r.URL.Path)
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
		AllowedOrigins:   origins,
		AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: false,
		MaxAge:           300,
	})
}
