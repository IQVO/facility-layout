// Command facility is the composition root: it wires env config into
// adapters, adapters into use cases, and use cases into the HTTP router.
//
// Postgres and Kafka are the ONLY runtime (ADR 0039): there is no in-memory
// mode, no log sink and no optional relay. A missing mandatory variable is a
// boot error naming it, raised before the listener binds. The service never
// creates topics through an admin API: the cluster broker auto-creates them on
// first write (the relay's writer sets AllowAutoTopicCreation) and
// infrastructure declares none.
//
// Environment:
//
//	DATABASE_URL                  REQUIRED. Postgres DSN
//	MIGRATIONS_DATABASE_URL       direct DSN for the boot migrations (default DATABASE_URL)
//	KAFKA_BROKERS                 REQUIRED. Comma-separated brokers
//	EVENT_PUBLISHER               kafka (default). log is REJECTED at boot
//	HTTP_ADDR                     listen address (default :8080)
//	OUTBOX_RELAY_INTERVAL         relay poll interval (default 1s)
//	OUTBOX_MAX_ATTEMPTS           failures before the relay excludes a row (default 20, 0 = unlimited)
//	HOUSEKEEPING_INTERVAL, IDEMPOTENCY_KEY_TTL, OUTBOX_RETENTION   sweeper knobs (ADR 0026)
//	OTEL_EXPORTER_OTLP_ENDPOINT, LOG_LEVEL, SERVICE_VERSION
//
// /readyz is the drain gate AND a Postgres ping AND a Kafka metadata check (a
// broker answers a metadata request that names no topic: reachability only,
// never topics), cached for a few seconds. /healthz is liveness only.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/facility-layout/internal/adapters/inbound/http"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/bootretry"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/system"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/telemetry"
	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/application/usecases"
)

// serviceVersion is overridable at build time with
// -ldflags "-X main.serviceVersion=v1.2.3"; otherwise SERVICE_VERSION, and
// otherwise "dev". It becomes the service.version resource attribute on
// every span, metric and log record this process exports.
var serviceVersion = ""

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("service exited with error", "error", err)
		os.Exit(1)
	}
}

// run boots the service and serves until SIGINT/SIGTERM or until ctx is
// cancelled (the in-process integration test stops it that way), then shuts
// down in the fleet order. It reads the whole configuration from the
// environment.
func run(ctx context.Context) error {
	serviceName := getenv("OTEL_SERVICE_NAME", inboundhttp.DefaultServiceName)

	logger := telemetry.NewLogger(os.Stdout, getenv("LOG_LEVEL", "info"), serviceName)
	slog.SetDefault(logger)

	// Fail fast, before telemetry, any connection and the listener: a
	// missing mandatory variable is a boot error that names it.
	cfg, err := loadBootConfig()
	if err != nil {
		return err
	}

	shutdownTelemetry, err := setupServiceTelemetry(logger, serviceName)
	if err != nil {
		return err
	}
	defer flushTelemetry(logger, shutdownTelemetry)

	locationMetrics, err := telemetry.NewLocationMetrics()
	if err != nil {
		return err
	}

	httpAddr := getenv("HTTP_ADDR", ":8080")

	adapters, relay, sweeper, closeKafka, closePool, err := buildAdapters(cfg, logger)
	if err != nil {
		return err
	}
	// closePool runs LAST (ADR-0020 §graceful shutdown, mirroring
	// order-management's ADR-0025 verbatim): registered here, near the
	// top of run(), so by defer's LIFO order it runs AFTER closeKafka
	// (registered next) and after every consumer/relay goroutine has
	// already stopped touching it in the explicit shutdown sequence
	// below.
	defer closePool()
	defer func() {
		if err := closeKafka(); err != nil {
			logger.Warn("error closing kafka producers", "error", err)
		}
	}()

	// readiness backs GET /readyz (ADR-0020 §graceful shutdown, ADR 0039):
	// the drain gate, flipped to not-ready as the FIRST step of the
	// shutdown sequence below, AND a Postgres ping AND a Kafka metadata
	// check, so a Kubernetes readinessProbe only routes traffic to a pod
	// whose dependencies answer.
	readiness := inboundhttp.NewReadiness(0, 0,
		inboundhttp.Dependency{Name: "postgres", Check: adapters.idempotencyPool.Ping},
		inboundhttp.Dependency{Name: "kafka", Check: kafka.NewBrokerCheck(cfg.kafkaBrokers).Check},
	)

	httpServer := &http.Server{
		Addr:              httpAddr,
		Handler:           inboundhttp.NewRouter(newServer(adapters, system.Clock{}, locationMetrics, readiness), logger, inboundhttp.WithServiceName(serviceName)),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The outbox relay (ADR-0018) runs alongside the HTTP server in the
	// same process, draining outbox_events onto Kafka. It is always on:
	// the service has no other way to publish.
	relayDone := make(chan struct{})
	relayCtx, stopRelay := context.WithCancel(sigCtx)
	defer stopRelay()
	go func() {
		defer close(relayDone)
		logger.Info("outbox relay running", "integration_topic", kafka.Topic, "analytics_topic", kafka.AnalyticsTopic, "brokers", cfg.kafkaBrokers)
		if err := relay.Run(relayCtx); err != nil && !errors.Is(err, context.Canceled) {
			errCh <- err
		}
	}()

	// The housekeeping sweeper (ADR-0026) bounds the two append-only
	// tables — idempotency_keys past their TTL, PUBLISHED outbox_events
	// past retention — in the same process, sharing the shutdown signal.
	// HOUSEKEEPING_INTERVAL=0 disables it inside Run.
	sweeperDone := startBackground(sigCtx, logger, sweeper, "housekeeping sweeper running", errCh)
	defer func() { <-sweeperDone }()

	select {
	case err := <-errCh:
		return err
	case <-sigCtx.Done():
	}

	return gracefulShutdown(logger, httpServer, readiness, stopRelay, relayDone)
}

// flushTelemetry flushes and stops the OTel providers within a 5 s budget,
// the last thing run() does.
func flushTelemetry(logger *slog.Logger, shutdown func(context.Context) error) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(shutdownCtx); err != nil {
		logger.Warn("telemetry shutdown reported an error", "error", err)
	}
}

// startBackground runs a background worker (the housekeeping sweeper;
// anything with the same Run(ctx) error shape) on ctx, reporting a
// non-cancel error to errCh. It returns a channel closed when the worker
// has fully stopped, so the caller can wait for it during shutdown;
// a nil worker closes the channel immediately.
func startBackground(ctx context.Context, logger *slog.Logger, worker interface {
	Run(context.Context) error
}, startMsg string, errCh chan<- error) chan struct{} {
	done := make(chan struct{})
	if worker == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		logger.Info(startMsg)
		if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			errCh <- err
		}
	}()
	return done
}

// gracefulShutdown drains the process (ADR-0020, mirroring
// order-management's ADR-0025 §graceful shutdown verbatim), in order:
//
//  1. Flip readiness to not-ready FIRST, before anything else stops
//     -- a Kubernetes readinessProbe polling /readyz needs a window
//     to observe this and stop routing NEW traffic to this pod
//     before step 2 below ever closes the listener.
//  2. Stop accepting new HTTP connections and drain in-flight
//     requests, bounded by shutdownCtx.
//  3. Stop the outbox relay cleanly: cancel its context (no new work
//     is picked up after this) and wait, bounded by the SAME
//     shutdownCtx, for it to actually finish in-flight work rather
//     than merely asking it to stop and moving on.
//  4. Only THEN do the deferred closeKafka/closePool calls
//     (registered earlier in run), so by defer's LIFO
//     order closeKafka runs after this function returns and
//     closePool -- which closes the pgx pool -- runs LAST of all,
//     after every relay/producer has already stopped touching it.
func gracefulShutdown(logger *slog.Logger, httpServer *http.Server, readiness *inboundhttp.Readiness, stopRelay context.CancelFunc, relayDone chan struct{}) error {
	readiness.SetNotReady()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := httpServer.Shutdown(shutdownCtx)
	// Let the relay finish its in-flight pass so an event committed by a
	// request that completed just before shutdown is not stranded until
	// the next pod boots.
	stopRelay()
	select {
	case <-relayDone:
	case <-shutdownCtx.Done():
		logger.Warn("outbox relay did not stop before the shutdown deadline")
	}
	return err
}

// adapterSet is the concrete outbound side of the hexagon: Postgres
// repositories, the transactional outbox and the unit of work. There is no
// in-memory variant in the binary (internal/architecture
// TestRuntimeBinaryHasNoInMemoryAdapters).
type adapterSet struct {
	sites         ports.SiteRepo
	zones         ports.ZoneRepo
	aisles        ports.AisleRepo
	slots         ports.SlotRepo
	locationTypes ports.LocationTypeRepo
	rules         ports.PlacementRuleRepo
	structures    ports.FixedStructureRepo
	crossAisles   ports.CrossAisleRepo
	publisher     ports.EventPublisher
	// unitOfWork brackets a use case's Save(s) + Publish(es) atomically
	// (ADR-0018).
	unitOfWork ports.UnitOfWork
	// idempotencyPool is the pgxpool.Pool the Postgres adapters use. It is
	// threaded through to inboundhttp.Server.IdempotencyPool so
	// RequireIdempotencyKey can begin its own transaction directly
	// (ADR-0019), and its Ping backs /readyz.
	idempotencyPool *pgxpool.Pool
}

// newServer wires every use case over the chosen adapters. It is the one
// place in the codebase that knows about all three layers at once.
func newServer(a adapterSet, clock ports.Clock, locationMetrics ports.LocationMetrics, readiness *inboundhttp.Readiness) *inboundhttp.Server {
	return &inboundhttp.Server{
		RegisterSite: &usecases.RegisterSite{Sites: a.sites, Events: a.publisher, Clock: clock, UnitOfWork: a.unitOfWork},
		GetSite:      &usecases.GetSite{Sites: a.sites},
		ListSites:    &usecases.ListSites{Sites: a.sites},
		ChangeSiteTransferCapability: &usecases.ChangeSiteTransferCapability{
			Sites: a.sites, Events: a.publisher, Clock: clock, UnitOfWork: a.unitOfWork,
		},
		ChangeSiteRole: &usecases.ChangeSiteRole{
			Sites: a.sites, Events: a.publisher, Clock: clock, UnitOfWork: a.unitOfWork,
		},
		ListSiteRoleTransferDefaults: &usecases.ListSiteRoleTransferDefaults{},

		RegisterZone: &usecases.RegisterZone{Sites: a.sites, Zones: a.zones, Events: a.publisher, Clock: clock, UnitOfWork: a.unitOfWork},
		GetZone:      &usecases.GetZone{Zones: a.zones},
		ListZones:    &usecases.ListZones{Sites: a.sites, Zones: a.zones},

		RegisterAisle: &usecases.RegisterAisle{Zones: a.zones, Aisles: a.aisles, Events: a.publisher, Clock: clock, UnitOfWork: a.unitOfWork},
		GetAisle:      &usecases.GetAisle{Aisles: a.aisles},
		ListAisles:    &usecases.ListAisles{Zones: a.zones, Aisles: a.aisles},

		RegisterLocationType: &usecases.RegisterLocationType{LocationTypes: a.locationTypes, Events: a.publisher, Clock: clock, UnitOfWork: a.unitOfWork},
		GetLocationType:      &usecases.GetLocationType{LocationTypes: a.locationTypes},
		ListLocationTypes:    &usecases.ListLocationTypes{LocationTypes: a.locationTypes},

		DefinePlacementRule: &usecases.DefinePlacementRule{LocationTypes: a.locationTypes, Rules: a.rules, Events: a.publisher, Clock: clock, UnitOfWork: a.unitOfWork},
		GetPlacementRule:    &usecases.GetPlacementRule{Rules: a.rules},
		ListPlacementRules:  &usecases.ListPlacementRules{Rules: a.rules},

		RegisterLocationSlot: &usecases.RegisterLocationSlot{
			Sites: a.sites, Zones: a.zones, Aisles: a.aisles, Slots: a.slots,
			LocationTypes: a.locationTypes, Rules: a.rules, Events: a.publisher, Clock: clock,
			Metrics: locationMetrics, UnitOfWork: a.unitOfWork,
		},
		GetLocationSlot:           &usecases.GetLocationSlot{Slots: a.slots},
		GetLocationClassification: &usecases.GetLocationClassification{Slots: a.slots, Zones: a.zones},
		ListLocationsByRole:       &usecases.ListLocationsByRole{Sites: a.sites, Zones: a.zones, Slots: a.slots},
		DecommissionLocationSlot:  &usecases.DecommissionLocationSlot{Slots: a.slots, Events: a.publisher, Clock: clock, UnitOfWork: a.unitOfWork},
		ImportFacilityLayout: &usecases.ImportFacilityLayout{
			Sites: a.sites, Zones: a.zones, Aisles: a.aisles, Slots: a.slots,
			LocationTypes: a.locationTypes, Rules: a.rules, Events: a.publisher, Clock: clock,
			Metrics: locationMetrics, UnitOfWork: a.unitOfWork,
		},

		GetSiteLayout: &usecases.GetSiteLayout{Sites: a.sites, Zones: a.zones, Aisles: a.aisles, Slots: a.slots, Structures: a.structures},
		GetZoneGrid:   &usecases.GetZoneGrid{Zones: a.zones, Aisles: a.aisles, Slots: a.slots},

		SetLocationGeometry:    &usecases.SetLocationGeometry{Slots: a.slots, Events: a.publisher, Clock: clock, UnitOfWork: a.unitOfWork},
		SetAisleGeometry:       &usecases.SetAisleGeometry{Aisles: a.aisles, Events: a.publisher, Clock: clock, UnitOfWork: a.unitOfWork},
		RegisterFixedStructure: &usecases.RegisterFixedStructure{Sites: a.sites, Structures: a.structures, Events: a.publisher, Clock: clock, UnitOfWork: a.unitOfWork},
		ListFixedStructures:    &usecases.ListFixedStructures{Sites: a.sites, Structures: a.structures},

		RegisterCrossAisle: &usecases.RegisterCrossAisle{
			Zones: a.zones, Aisles: a.aisles, CrossAisles: a.crossAisles, Events: a.publisher, Clock: clock, UnitOfWork: a.unitOfWork,
		},
		ListCrossAisles: &usecases.ListCrossAisles{
			Zones: a.zones, CrossAisles: a.crossAisles,
		},
		GetZoneTravelGraph: &usecases.GetZoneTravelGraph{
			Zones: a.zones, Aisles: a.aisles, Slots: a.slots, CrossAisles: a.crossAisles,
		},
		EstimateTravelDistance: &usecases.EstimateTravelDistance{
			Zones: a.zones, Aisles: a.aisles, Slots: a.slots, CrossAisles: a.crossAisles,
		},
		IdempotencyPool: a.idempotencyPool,
		Readiness:       readiness,
	}
}

// publisherConfig carries the composition-root inputs buildAdapters wires the
// real Postgres adapters, the outbox and the relay from.
type publisherConfig struct {
	databaseURL string
	// migrationsDatabaseURL is a DIRECT (non-pooled, session-mode) Postgres
	// connection string used ONLY for the golang-migrate step in
	// dialPostgres below — never for the pgxpool built right after, which
	// always uses databaseURL. They are deliberately different in a
	// PgBouncer-fronted environment: golang-migrate's postgres driver takes
	// a session-scoped `SELECT pg_advisory_lock($1)` to serialize
	// concurrent migration runs across replicas starting at the same time,
	// and PgBouncer's transaction-pooling mode (this fleet's pool_mode for
	// every OLTP DATABASE_URL, warehouse-infra PR #43) does not support
	// session-scoped state — each statement in one logical client session
	// can land on a different physical backend connection, so the
	// advisory lock never behaves as a real mutex. Losing replicas
	// crash-loop with `pq: unnamed prepared statement does not exist` /
	// `pq: canceling statement due to statement timeout` until one wins
	// the race. See ADR 0023-migrations-direct-postgres-connection.md
	// (mirroring order-management's ADR-0029) for the full incident and
	// fix. publisherConfigFromEnv sets this to MIGRATIONS_DATABASE_URL
	// when set (warehouse-infra provisions it as a direct, non-pooled DSN
	// alongside DATABASE_URL) or falls back to databaseURL itself for any
	// environment that doesn't provision the split (local dev, CI
	// integration tests) — byte-identical to this field's behavior before
	// it existed in that case.
	migrationsDatabaseURL string
	migrationsPath        string
	// kafkaBrokers is KAFKA_BROKERS, parsed. There is no default: an empty
	// list is a boot error (loadBootConfig).
	kafkaBrokers []string
	// relayInterval is how long the outbox relay sleeps between empty
	// passes (OUTBOX_RELAY_INTERVAL).
	relayInterval time.Duration
	// housekeeping tunes the Sweeper (ADR-0026): HOUSEKEEPING_INTERVAL,
	// IDEMPOTENCY_KEY_TTL, OUTBOX_RETENTION. Zero interval disables the
	// sweeper; zero TTL/retention disables that half of it.
	housekeepingInterval time.Duration
	idempotencyKeyTTL    time.Duration
	outboxRetention      time.Duration
}

// buildAdapters wires the real adapters: Postgres repositories, the
// transactional outbox (ADR-0018) and the unit of work over cfg.databaseURL,
// and the always-on relay that drains the outbox onto Kafka. Every domain
// event is enqueued onto BOTH the integration topic and the analytics topic in
// the same transaction as the aggregate write; the store and the topics can
// never diverge. There is no other runtime: an empty databaseURL or broker
// list is a boot error. The returned Sweeper (ADR-0026) and relay are always
// non-nil.
func buildAdapters(cfg publisherConfig, logger *slog.Logger) (adapterSet, *postgres.OutboxRelay, *postgres.Sweeper, func() error, func(), error) {
	noop := func() {}
	noopKafkaClose := func() error { return nil }

	if cfg.databaseURL == "" {
		return adapterSet{}, nil, nil, noopKafkaClose, noop, errNoDatabase
	}
	if len(cfg.kafkaBrokers) == 0 {
		return adapterSet{}, nil, nil, noopKafkaClose, noop, errNoBrokers
	}

	pool, err := dialPostgres(cfg, logger)
	if err != nil {
		return adapterSet{}, nil, nil, noopKafkaClose, noop, err
	}

	base := adapterSet{
		sites:         postgres.NewSiteRepo(pool),
		zones:         postgres.NewZoneRepo(pool),
		aisles:        postgres.NewAisleRepo(pool),
		slots:         postgres.NewSlotRepo(pool),
		locationTypes: postgres.NewLocationTypeRepo(pool),
		rules:         postgres.NewPlacementRuleRepo(pool),
		structures:    postgres.NewFixedStructureRepo(pool),
		crossAisles:   postgres.NewCrossAisleRepo(pool),
	}

	sweeper := postgres.NewSweeper(pool,
		postgres.WithSweepInterval(cfg.housekeepingInterval),
		postgres.WithIdempotencyKeyTTL(cfg.idempotencyKeyTTL),
		postgres.WithOutboxRetention(cfg.outboxRetention),
		postgres.WithSweeperLogger(logger),
	)

	return outboxAdapters(cfg, logger, base, pool, sweeper)
}

// outboxAdapters wires the transactional outbox (ADR-0018): every domain event
// is enqueued onto BOTH the integration topic and the analytics topic in the
// same transaction as the aggregate write, and the returned relay drains the
// outbox onto Kafka.
func outboxAdapters(cfg publisherConfig, logger *slog.Logger, base adapterSet, pool *pgxpool.Pool, sweeper *postgres.Sweeper) (adapterSet, *postgres.OutboxRelay, *postgres.Sweeper, func() error, func(), error) {
	brokers := cfg.kafkaBrokers
	kafkaPublisher := kafka.NewPublisher(brokers, uuidLike)
	analyticsPublisher := kafka.NewAnalyticsPublisher(brokers, uuidLike)
	relaySink := kafka.NewRelaySink(brokers)
	// closeKafka releases every Kafka producer this process opened
	// (integration publisher, analytics publisher, relay sink) WITHOUT
	// touching the pgx pool -- ADR-0020 §graceful shutdown requires the
	// pool to close LAST, after every consumer/relay/producer has already
	// stopped touching it, so the pool's own close is returned separately
	// (closePool below) rather than bundled into this function.
	closeKafka := func() error {
		return errors.Join(kafkaPublisher.Close(), analyticsPublisher.Close(), relaySink.Close())
	}

	base.unitOfWork = postgres.NewUnitOfWork(pool)
	base.publisher = postgres.NewOutboxPublisher(pool, uuidLike, kafkaPublisher, analyticsPublisher)
	base.idempotencyPool = pool
	relay := postgres.NewOutboxRelay(pool, relaySink, logger,
		postgres.WithInterval(cfg.relayInterval),
		postgres.WithMaxAttempts(outboxMaxAttempts(logger)))
	logger.Info("event publisher configured", "publisher", "kafka", "mode", "outbox",
		"integration_topic", kafka.Topic, "analytics_topic", kafka.AnalyticsTopic, "brokers", brokers)

	return base, relay, sweeper, closeKafka, pool.Close, nil
}

// dialPostgres runs the schema migrations, opens the pool, and verifies it
// with a ping, each under boot retry. Retried, because in this fleet EVERY
// injected pod's first outbound TCP dial is reset ~10s after the app starts
// (Istio native sidecars; holdApplicationUntilProxyStarts is a no-op for
// them). A single attempt turns that known, transient condition into
// CrashLoopBackOff. The retry does not weaken the fail-closed rule: after
// the budget is exhausted this still refuses to boot, reporting the real
// underlying error.
func dialPostgres(cfg publisherConfig, logger *slog.Logger) (*pgxpool.Pool, error) {
	ctx := context.Background()
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error {
		return postgres.RunMigrations(cfg.migrationsDatabaseURL, cfg.migrationsPath)
	}); err != nil {
		return nil, err
	}

	pool, err := postgres.NewPool(ctx, cfg.databaseURL)
	if err != nil {
		return nil, err
	}
	// pgxpool does not itself dial until first use, so without this the
	// first real failure would surface inside a request instead of at boot.
	if err := bootretry.Retry(ctx, logger, "ping database", func() error {
		return pool.Ping(ctx)
	}); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// setupServiceTelemetry configures OTel once at boot, before any adapter
// is built, so a failure in the database or migrations is itself traced and
// logged with the right service identity. An unreachable Collector is not
// an error: the OTLP exporters are non-blocking, and telemetry is dropped
// rather than the service failing to start.
func setupServiceTelemetry(logger *slog.Logger, serviceName string) (func(context.Context) error, error) {
	shutdown, err := telemetry.Setup(
		context.Background(),
		serviceName,
		resolveServiceVersion(),
		getenv("OTEL_EXPORTER_OTLP_ENDPOINT", telemetry.DefaultOTLPEndpoint),
	)
	if err != nil {
		return nil, err
	}
	logger.Info("telemetry configured",
		"service_name", serviceName,
		"service_version", resolveServiceVersion(),
		"environment", telemetry.Environment(),
		"otlp_endpoint", getenv("OTEL_EXPORTER_OTLP_ENDPOINT", telemetry.DefaultOTLPEndpoint),
	)
	return shutdown, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// durationEnv parses key as a time.Duration, falling back on absence or a
// malformed value (the relay interval is a tuning knob, not a contract).
func durationEnv(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// durationEnvRaw is durationEnv for knobs where ZERO is meaningful (it
// disables the feature): only absence or a malformed value falls back to
// the default, an explicit "0s"/"0" is preserved as-is.
func durationEnvRaw(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

// outboxMaxAttempts parses OUTBOX_MAX_ATTEMPTS (ADR 0036): how many failed
// sends exclude an outbox row from the relay's claims. Unset means
// postgres.DefaultOutboxMaxAttempts (20); 0 means unlimited (the pre-ADR
// behaviour, where one poison row blocks the relay forever).
func outboxMaxAttempts(logger *slog.Logger) int {
	return intEnv(logger, "OUTBOX_MAX_ATTEMPTS", postgres.DefaultOutboxMaxAttempts)
}

// intEnv parses a non-negative integer env var. Unset yields fallback; an
// invalid or negative value logs a warning and yields fallback, so a typo
// cannot silently remove a safety limit. "0" is valid and its meaning is the
// caller's.
func intEnv(logger *slog.Logger, key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		logger.Warn("invalid integer env var, using default", "key", key, "value", raw, "default", fallback, "error", err)
		return fallback
	}
	return n
}

// uuidLike mints the CloudEvents `id` (UUID v4) — once per domain event —
// stamped on each published event (ADR-0024).
func uuidLike() string {
	return uuid.NewString()
}

// resolveServiceVersion reports this build's version: the ldflags-injected
// value when the binary was stamped, else SERVICE_VERSION, else "dev".
func resolveServiceVersion() string {
	if serviceVersion != "" {
		return serviceVersion
	}
	return getenv("SERVICE_VERSION", "dev")
}
