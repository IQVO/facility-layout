// Command mcp is the composition root for the Facility Layout MCP server: it
// wires env config to outbound adapters, adapters to the read use cases, and
// those to the inbound MCP adapter, then serves MCP over Streamable HTTP. It
// is a second, independent deployable alongside cmd/facility (the HTTP
// service), per ADR-0007.
//
// facility-layout is a read-only Open Host Service, so this server wires only
// the read use cases (GetSiteLayout, GetZoneGrid, ListSites) and exposes only
// read tools.
//
// There is no authentication layer in front of this server: the fleet's
// static-bearer-key rollout was removed (see the ADR recorded alongside that
// change). It is reachable only from inside the cluster.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	inboundmcp "github.com/claudioed/facility-layout/internal/adapters/inbound/mcp"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/bootretry"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/telemetry"
	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/application/usecases"
)

func main() {
	if err := run(); err != nil {
		slog.Error("mcp server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	serviceName := getenv("OTEL_SERVICE_NAME", "facility-layout-mcp")

	logger := telemetry.NewLogger(os.Stdout, getenv("LOG_LEVEL", "info"), serviceName)
	slog.SetDefault(logger)

	// Same non-blocking telemetry setup as the HTTP service: an unreachable
	// Collector degrades to dropped telemetry, never a server that won't start.
	shutdownTelemetry, err := telemetry.Setup(
		context.Background(),
		serviceName,
		getenv("SERVICE_VERSION", "dev"),
		getenv("OTEL_EXPORTER_OTLP_ENDPOINT", telemetry.DefaultOTLPEndpoint),
	)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTelemetry(shutdownCtx); err != nil {
			logger.Warn("telemetry shutdown reported an error", "error", err)
		}
	}()

	httpAddr := getenv("MCP_ADDR", ":8090")
	databaseURL := os.Getenv("DATABASE_URL")
	migrationsPath := getenv("MIGRATIONS_PATH", "migrations")

	adapters, closeAdapters, err := buildAdapters(databaseURL, migrationsPath, logger)
	if err != nil {
		return err
	}
	defer closeAdapters()

	// The MCP adapter reuses the SAME read use cases the HTTP adapter uses:
	// GetSiteLayout, GetZoneGrid and ListSites, each over the same repos. No
	// write use case is wired — this context's map is consumed, not mutated.
	//
	// When REPORTS_BASE_URL is set, the curated catalog-growth report tool is
	// additionally wired to call the facility-reports REST service (ADR-0010);
	// when it is unset the tool is simply not registered.
	deps := inboundmcp.Deps{
		GetSiteLayout:       &usecases.GetSiteLayout{Sites: adapters.sites, Zones: adapters.zones, Aisles: adapters.aisles, Slots: adapters.slots, Structures: adapters.structures},
		GetZoneGrid:         &usecases.GetZoneGrid{Zones: adapters.zones, Aisles: adapters.aisles, Slots: adapters.slots},
		ListSites:           &usecases.ListSites{Sites: adapters.sites},
		ListLocationsByRole: &usecases.ListLocationsByRole{Sites: adapters.sites, Zones: adapters.zones, Slots: adapters.slots},
		GetZoneTravelGraph: &usecases.GetZoneTravelGraph{
			Zones: adapters.zones, Aisles: adapters.aisles, Slots: adapters.slots, CrossAisles: adapters.crossAisles,
		},
		EstimateTravelDistance: &usecases.EstimateTravelDistance{
			Zones: adapters.zones, Aisles: adapters.aisles, Slots: adapters.slots, CrossAisles: adapters.crossAisles,
		},
	}
	if reportsURL := os.Getenv("REPORTS_BASE_URL"); reportsURL != "" {
		deps.Reports = inboundmcp.NewReportsRESTClient(reportsURL, nil)
		logger.Info("catalog-growth report tool wired", "reports_base_url", reportsURL)
	}
	server := inboundmcp.NewServer(deps)

	handler := newRouter(inboundmcp.Handler(server))

	srv := &http.Server{Addr: httpAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		logger.Info("mcp server listening (Streamable HTTP)", "addr", httpAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("mcp server failed", "error", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// newRouter wraps the MCP handler in the process's HTTP surface:
//
//   - GET /healthz answers 200 {"status":"ok"}, so the Kubernetes
//     liveness/readiness probes can see the process is up.
//   - The MCP Streamable HTTP endpoint is mounted at BOTH "/" and "/mcp":
//     "/" keeps the original root mount working, "/mcp" is the convention
//     warehouse-ops-agent's *_MCP_ENDPOINT values and the docs' examples
//     use.
func newRouter(mcpHandler http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", healthz)
	r.Handle("/", mcpHandler)
	r.Mount("/mcp", mcpHandler)
	return r
}

// healthz is the process-liveness endpoint; it mirrors the shape
// cmd/facility-projector and cmd/facility-reports already expose.
func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// adapterSet is the read-side outbound repos the MCP server needs, chosen at
// startup: Postgres when DATABASE_URL is set, in-memory otherwise. It mirrors
// cmd/facility's selection so both binaries read the same map.
type adapterSet struct {
	sites       ports.SiteRepo
	zones       ports.ZoneRepo
	aisles      ports.AisleRepo
	slots       ports.SlotRepo
	structures  ports.FixedStructureRepo
	crossAisles ports.CrossAisleRepo
}

// buildAdapters wires the Postgres repos when DATABASE_URL is set, or falls
// back to the in-memory repos for local development without a database —
// exactly as cmd/facility/main.go does.
func buildAdapters(databaseURL, migrationsPath string, logger *slog.Logger) (adapterSet, func(), error) {
	noop := func() {}

	if databaseURL == "" {
		logger.Info("database url not configured; using in-memory adapters")
		return adapterSet{
			sites:       memory.NewSiteRepo(),
			zones:       memory.NewZoneRepo(),
			aisles:      memory.NewAisleRepo(),
			slots:       memory.NewSlotRepo(),
			structures:  memory.NewFixedStructureRepo(),
			crossAisles: memory.NewCrossAisleRepo(),
		}, noop, nil
	}

	// Retried, because in this fleet EVERY injected pod's first outbound TCP
	// dial is reset ~10s after the app starts (Istio native sidecars). A
	// single attempt turns that known, transient condition into
	// CrashLoopBackOff; the retry still refuses to boot once the budget is
	// exhausted, reporting the real underlying error.
	if err := bootretry.Retry(context.Background(), logger, "run migrations", func() error {
		return postgres.RunMigrations(databaseURL, migrationsPath)
	}); err != nil {
		return adapterSet{}, noop, err
	}

	pool, err := postgres.NewPool(context.Background(), databaseURL)
	if err != nil {
		return adapterSet{}, noop, err
	}
	// pgxpool does not itself dial until first use, so without this the
	// first real failure would surface inside a request instead of at boot.
	if err := bootretry.Retry(context.Background(), logger, "ping database", func() error {
		return pool.Ping(context.Background())
	}); err != nil {
		pool.Close()
		return adapterSet{}, noop, err
	}

	return adapterSet{
		sites:       postgres.NewSiteRepo(pool),
		zones:       postgres.NewZoneRepo(pool),
		aisles:      postgres.NewAisleRepo(pool),
		slots:       postgres.NewSlotRepo(pool),
		structures:  postgres.NewFixedStructureRepo(pool),
		crossAisles: postgres.NewCrossAisleRepo(pool),
	}, pool.Close, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
