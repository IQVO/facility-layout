// Command a2a is the composition root for the Facility Layout A2A agent: it
// wires env config to outbound adapters, adapters to the read use cases, and
// those to the inbound A2A adapter, then serves Agent2Agent JSON-RPC over
// HTTP with the Agent Card at the well-known path. It is a third, independent
// deployable alongside cmd/facility (the HTTP service) and cmd/mcp, per
// ADR-0043.
//
// facility-layout is a read-only Open Host Service and its MCP surface
// offers no write, so this agent wires only read use cases and offers only
// read skills (site_layout, travel_distance). Because nothing here writes, it
// opens no event publisher and no outbox: the map is edited only through
// cmd/facility.
//
// Like cmd/mcp it runs on real Postgres only (ADR 0039): there is no
// in-memory mode. A missing mandatory variable is a boot error naming it,
// raised before telemetry, any connection and the listener.
//
// There is no authentication layer in front of this agent: the fleet's A2A,
// MCP and REST endpoints are unauthenticated by decision (ADR-0015). It is
// reachable only from inside the cluster (or through the Kong route).
//
// Environment:
//
//	DATABASE_URL             REQUIRED. Postgres DSN
//	A2A_AGENT_URL            REQUIRED. Absolute http(s) URL of this agent's
//	                         JSON-RPC endpoint, advertised on the Agent Card
//	MIGRATIONS_DATABASE_URL  direct DSN for the boot migrations (default DATABASE_URL)
//	MIGRATIONS_PATH          migrations directory (default migrations)
//	A2A_ADDR                 listen address (default :8093)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/go-chi/chi/v5"

	inbounda2a "github.com/claudioed/facility-layout/internal/adapters/inbound/a2a"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/bootretry"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/telemetry"
	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/application/usecases"
)

func main() {
	if err := run(); err != nil {
		slog.Error("a2a agent exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	serviceName := getenv("OTEL_SERVICE_NAME", "facility-layout-a2a")

	logger := telemetry.NewLogger(os.Stdout, getenv("LOG_LEVEL", "info"), serviceName)
	slog.SetDefault(logger)

	// Fail fast, before telemetry, any connection and the listener: the A2A
	// agent reads the facility map from real Postgres (ADR 0039) and must
	// advertise the URL its clients dial (ADR-0043).
	cfg, err := loadBootConfig()
	if err != nil {
		return err
	}
	databaseURL := cfg.databaseURL

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

	httpAddr := getenv("A2A_ADDR", ":8093")
	// See buildAdapters' doc comment and ADR
	// 0023-migrations-direct-postgres-connection.md (mirroring
	// order-management's ADR-0029) for the full "why" (session-scoped
	// pg_advisory_lock vs PgBouncer transaction-pooling incompatibility).
	// This binary also runs migrations on start (buildAdapters below), so
	// it needs the same direct-connection split as cmd/facility.
	migrationsDatabaseURL := getenv("MIGRATIONS_DATABASE_URL", databaseURL)
	migrationsPath := getenv("MIGRATIONS_PATH", "migrations")

	adapters, closeAdapters, err := buildAdapters(databaseURL, migrationsDatabaseURL, migrationsPath, logger)
	if err != nil {
		return err
	}
	defer closeAdapters()

	// The A2A adapter reuses the SAME read use cases the HTTP and MCP adapters
	// use: GetSiteLayout and EstimateTravelDistance, each over the same repos.
	// No write use case is wired -- this context's map is consumed, not
	// mutated, and its MCP surface offers no write either.
	deps := inbounda2a.Deps{
		GetSiteLayout: &usecases.GetSiteLayout{Sites: adapters.sites, Zones: adapters.zones, Aisles: adapters.aisles, Slots: adapters.slots, Structures: adapters.structures},
		EstimateTravelDistance: &usecases.EstimateTravelDistance{
			Zones: adapters.zones, Aisles: adapters.aisles, Slots: adapters.slots, CrossAisles: adapters.crossAisles,
		},
	}
	card := inbounda2a.Card(inbounda2a.Config{AgentURL: cfg.agentURL, Version: getenv("SERVICE_VERSION", "dev")})
	handler := newRouter(inbounda2a.Handler(inbounda2a.NewExecutor(deps)), card)

	srv := &http.Server{Addr: httpAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		logger.Info("a2a agent listening (JSON-RPC over HTTP)", "addr", httpAddr, "agent_url", cfg.agentURL)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("a2a agent failed", "error", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// newRouter wraps the A2A handler in the process's HTTP surface:
//
//   - GET /healthz answers 200 {"status":"ok"}, so the Kubernetes
//     liveness/readiness probes can see the process is up. A2A is
//     unauthenticated by decision, so probes need no credentials, and the
//     route leaks no skill or state.
//   - The Agent Card is served at the well-known path
//     /.well-known/agent-card.json (the discovery contract, ADR-0043).
//   - The JSON-RPC endpoint is mounted at BOTH "/" (the canonical root) and
//     "/a2a" (the fleet's <CTX>_A2A_ENDPOINT convention, mirroring cmd/mcp's
//     dual "/mcp" mount).
func newRouter(a2aHandler http.Handler, card *a2a.AgentCard) http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", healthz)
	r.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	r.Handle("/a2a", a2aHandler)
	r.Handle("/", a2aHandler)
	return r
}

// healthz is the process-liveness endpoint; it mirrors the shape
// cmd/facility-projector and cmd/facility-reports already expose.
func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// adapterSet is the read-side outbound repos the A2A agent needs: the same
// Postgres repos cmd/facility writes, so all binaries read the same map.
// There is no in-memory variant in the binary (ADR 0039).
type adapterSet struct {
	sites       ports.SiteRepo
	zones       ports.ZoneRepo
	aisles      ports.AisleRepo
	slots       ports.SlotRepo
	structures  ports.FixedStructureRepo
	crossAisles ports.CrossAisleRepo
}

// errNoDatabase is the boot error of a missing DATABASE_URL: this binary reads
// the facility map from real Postgres and has no other runtime.
var errNoDatabase = errors.New("DATABASE_URL is required: this context runs on real Postgres")

// errNoAgentURL is the boot error for a missing A2A_AGENT_URL: the Agent Card
// must advertise the absolute URL clients dial (ADR-0043).
var errNoAgentURL = errors.New("A2A_AGENT_URL is required: the Agent Card must advertise the absolute URL of this agent's JSON-RPC endpoint (ADR-0043), e.g. http://<release>-a2a.<ns>.svc.cluster.local:8093/")

// bootConfig is the mandatory configuration of cmd/a2a: real Postgres and the
// URL the Agent Card advertises, with no default behind either.
type bootConfig struct {
	databaseURL string
	agentURL    string
}

// loadBootConfig reads and validates the mandatory environment. Each failure
// is a boot error that names its variable.
func loadBootConfig() (bootConfig, error) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return bootConfig{}, errNoDatabase
	}
	agentURL := strings.TrimSpace(os.Getenv("A2A_AGENT_URL"))
	if err := checkAgentURL(agentURL); err != nil {
		return bootConfig{}, err
	}
	return bootConfig{databaseURL: databaseURL, agentURL: agentURL}, nil
}

// checkAgentURL validates A2A_AGENT_URL: an absolute http(s) URL. The card is
// the discovery contract -- a relative or schemeless URL is a misconfiguration
// every client would hit, so it is refused at boot, never silently served.
func checkAgentURL(raw string) error {
	if raw == "" {
		return errNoAgentURL
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("A2A_AGENT_URL=%q is not an absolute http(s) URL: the Agent Card must advertise the endpoint clients dial (ADR-0043)", raw)
	}
	return nil
}

// buildAdapters wires the Postgres repos over databaseURL. An empty
// databaseURL is a boot error: there is no fallback runtime.
//
// migrationsDatabaseURL is used ONLY for the golang-migrate step below —
// the pgxpool opened just after it (and used for every subsequent request)
// always uses databaseURL. See cmd/facility/main.go's publisherConfig.
// migrationsDatabaseURL doc comment and ADR
// 0023-migrations-direct-postgres-connection.md for the full "why" a
// direct, non-pooled connection is needed here even though the pgxpool
// opened just after stays on PgBouncer.
func buildAdapters(databaseURL, migrationsDatabaseURL, migrationsPath string, logger *slog.Logger) (adapterSet, func(), error) {
	noop := func() {}

	if databaseURL == "" {
		return adapterSet{}, noop, errNoDatabase
	}

	// Retried, because in this fleet EVERY injected pod's first outbound TCP
	// dial is reset ~10s after the app starts (Istio native sidecars). A
	// single attempt turns that known, transient condition into
	// CrashLoopBackOff; the retry still refuses to boot once the budget is
	// exhausted, reporting the real underlying error.
	if err := bootretry.Retry(context.Background(), logger, "run migrations", func() error {
		return postgres.RunMigrations(migrationsDatabaseURL, migrationsPath)
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
