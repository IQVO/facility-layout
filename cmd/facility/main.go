// Command facility is the composition root: it wires env config into
// adapters, adapters into use cases, and use cases into the HTTP router.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/facility-layout/internal/adapters/inbound/http"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/bootretry"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
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
	if err := run(); err != nil {
		slog.Error("service exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	serviceName := getenv("OTEL_SERVICE_NAME", inboundhttp.DefaultServiceName)

	logger := telemetry.NewLogger(os.Stdout, getenv("LOG_LEVEL", "info"), serviceName)
	slog.SetDefault(logger)

	shutdownTelemetry, err := setupServiceTelemetry(logger, serviceName)
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

	locationMetrics, err := telemetry.NewLocationMetrics()
	if err != nil {
		return err
	}

	httpAddr := getenv("HTTP_ADDR", ":8080")

	adapters, relay, sweeper, closeKafka, closePool, err := buildAdapters(publisherConfigFromEnv(), logger)
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

	// readiness backs GET /readyz (ADR-0020 §graceful shutdown): flipped
	// to not-ready as the FIRST step of the shutdown sequence below,
	// before the HTTP server itself stops accepting connections, so a
	// Kubernetes readinessProbe has a chance to observe the flip and stop
	// routing new traffic during the drain window that follows.
	readiness := &inboundhttp.Readiness{}

	httpServer := &http.Server{
		Addr:              httpAddr,
		Handler:           inboundhttp.NewRouter(newServer(adapters, memory.SystemClock{}, locationMetrics, readiness), logger, inboundhttp.WithServiceName(serviceName)),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The outbox relay (ADR-0018) runs alongside the HTTP server in the
	// same process, draining outbox_events onto Kafka. It is only wired
	// when both Postgres and the kafka publisher are configured.
	relayDone := make(chan struct{})
	relayCtx, stopRelay := context.WithCancel(ctx)
	defer stopRelay()
	if relay != nil {
		go func() {
			defer close(relayDone)
			logger.Info("outbox relay running", "integration_topic", kafka.Topic, "analytics_topic", kafka.AnalyticsTopic)
			if err := relay.Run(relayCtx); err != nil && !errors.Is(err, context.Canceled) {
				errCh <- err
			}
		}()
	} else {
		close(relayDone)
	}

	// The housekeeping sweeper (ADR-0026) bounds the two append-only
	// tables — idempotency_keys past their TTL, PUBLISHED outbox_events
	// past retention — in the same process, sharing the shutdown signal.
	// Wired whenever Postgres is configured (both tables exist even when
	// the kafka publisher is not selected); HOUSEKEEPING_INTERVAL=0
	// disables it inside Run.
	sweeperDone := startBackground(ctx, logger, sweeper, "housekeeping sweeper running", errCh)
	defer func() { <-sweeperDone }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	return gracefulShutdown(logger, httpServer, readiness, stopRelay, relayDone)
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
	// A typed nil (*postgres.Sweeper)(nil) boxed into this interface
	// parameter is != nil, so the guard below alone would let it through
	// and panic in Run. Normalize it before checking.
	if sw, ok := worker.(*postgres.Sweeper); ok && sw == nil {
		worker = nil
	}
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

// adapterSet is the concrete outbound side of the hexagon, chosen at
// startup: Postgres when DATABASE_URL is set, in-memory otherwise.
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
	// (ADR-0018). nil when running without Postgres — every use case
	// treats that identically to "run them back to back" (see the
	// usecases package's atomically helper).
	unitOfWork ports.UnitOfWork
	// idempotencyPool, when non-nil, is the same pgxpool.Pool the
	// Postgres adapters use. It is threaded through to
	// inboundhttp.Server.IdempotencyPool so RequireIdempotencyKey can
	// begin its own transaction directly (ADR-0019). nil in the
	// in-memory adapter set (no Postgres configured).
	idempotencyPool *pgxpool.Pool
}

// newServer wires every use case over the chosen adapters. It is the one
// place in the codebase that knows about all three layers at once.
func newServer(a adapterSet, clock ports.Clock, locationMetrics ports.LocationMetrics, readiness *inboundhttp.Readiness) *inboundhttp.Server {
	return &inboundhttp.Server{
		RegisterSite: &usecases.RegisterSite{Sites: a.sites, Events: a.publisher, Clock: clock, UnitOfWork: a.unitOfWork},
		GetSite:      &usecases.GetSite{Sites: a.sites},
		ListSites:    &usecases.ListSites{Sites: a.sites},

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

// publisherConfig carries the composition-root inputs that decide which
// repositories and which EventPublisher buildAdapters wires.
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
	// eventPublisher selects the outbound EventPublisher: "kafka" publishes
	// the Published Language to the integration topic (and, with a database
	// configured, via the transactional outbox); "" (default) uses the log
	// publisher.
	eventPublisher string
	kafkaBrokers   string
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

// buildAdapters wires the Postgres adapters when DATABASE_URL is set, or
// falls back to the in-memory adapters for local development without a
// database. The EventPublisher is chosen independently:
// EVENT_PUBLISHER=kafka selects Kafka publishing regardless of the
// repository choice, so the Published Language reaches the broker
// whether the store is Postgres or in-memory. With BOTH Postgres and
// kafka configured, use cases publish into the transactional outbox
// (ADR-0018) and the returned relay drains it onto Kafka; the store and
// the topic can no longer diverge. The returned Sweeper (ADR-0026) is
// non-nil whenever Postgres is configured. The mode matrix:
//
//	DATABASE_URL | EVENT_PUBLISHER | publisher wired                    | relay
//	unset        | log (default)   | log                                | no
//	unset        | kafka           | direct Kafka fan-out (no outbox)   | no
//	set          | log (default)   | log                                | no
//	set          | kafka           | outbox (fans out to both topics)   | yes
func buildAdapters(cfg publisherConfig, logger *slog.Logger) (adapterSet, *postgres.OutboxRelay, *postgres.Sweeper, func() error, func(), error) {
	noop := func() {}
	noopKafkaClose := func() error { return nil }
	kafkaEnabled := cfg.eventPublisher == "kafka"

	if cfg.databaseURL == "" {
		return memoryAdapters(cfg, logger, kafkaEnabled)
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

	if !kafkaEnabled {
		base.publisher = events.NewLogPublisher(logger)
		base.idempotencyPool = pool
		return base, nil, sweeper, noopKafkaClose, pool.Close, nil
	}

	return outboxAdapters(cfg, logger, base, pool, sweeper)
}

// memoryAdapters is the no-database branch of buildAdapters: in-memory
// repositories with either the log publisher or, when EVENT_PUBLISHER=kafka,
// direct Kafka fan-out (no outbox — there is no store to keep in step with).
func memoryAdapters(cfg publisherConfig, logger *slog.Logger, kafkaEnabled bool) (adapterSet, *postgres.OutboxRelay, *postgres.Sweeper, func() error, func(), error) {
	logger.Info("database url not configured; using in-memory adapters")
	pub := ports.EventPublisher(events.NewLogPublisher(logger))
	closeKafka := func() error { return nil }
	if kafkaEnabled {
		brokers := strings.Split(cfg.kafkaBrokers, ",")
		kafkaPublisher := kafka.NewPublisher(brokers, uuidLike)
		analyticsPublisher := kafka.NewAnalyticsPublisher(brokers, uuidLike)
		pub = kafka.FanOut{NewId: uuidLike, Targets: []kafka.EncodeSender{kafkaPublisher, analyticsPublisher}}
		closeKafka = func() error {
			return errors.Join(kafkaPublisher.Close(), analyticsPublisher.Close())
		}
		logger.Info("event publisher configured", "publisher", "kafka", "mode", "direct",
			"integration_topic", kafka.Topic, "analytics_topic", kafka.AnalyticsTopic, "brokers", brokers)
	}
	return adapterSet{
		sites:         memory.NewSiteRepo(),
		zones:         memory.NewZoneRepo(),
		aisles:        memory.NewAisleRepo(),
		slots:         memory.NewSlotRepo(),
		locationTypes: memory.NewLocationTypeRepo(),
		rules:         memory.NewPlacementRuleRepo(),
		structures:    memory.NewFixedStructureRepo(),
		crossAisles:   memory.NewCrossAisleRepo(),
		publisher:     pub,
	}, nil, nil, closeKafka, func() {}, nil
}

// outboxAdapters is the DATABASE_URL + EVENT_PUBLISHER=kafka branch
// (ADR-0018): every domain event is enqueued onto BOTH the integration
// topic and the analytics topic in the same transaction as the aggregate
// write, and the returned relay drains the outbox onto Kafka.
func outboxAdapters(cfg publisherConfig, logger *slog.Logger, base adapterSet, pool *pgxpool.Pool, sweeper *postgres.Sweeper) (adapterSet, *postgres.OutboxRelay, *postgres.Sweeper, func() error, func(), error) {
	brokers := strings.Split(cfg.kafkaBrokers, ",")
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
	relay := postgres.NewOutboxRelay(pool, relaySink, logger, postgres.WithInterval(cfg.relayInterval))
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

// publisherConfigFromEnv gathers the outbound-wiring environment for
// buildAdapters in one place.
func publisherConfigFromEnv() publisherConfig {
	databaseURL := os.Getenv("DATABASE_URL")
	return publisherConfig{
		databaseURL: databaseURL,
		// See publisherConfig.migrationsDatabaseURL's doc comment and ADR
		// 0023-migrations-direct-postgres-connection.md: falls back to
		// databaseURL (DATABASE_URL) when MIGRATIONS_DATABASE_URL is unset,
		// which is every environment that doesn't provision the split
		// (local dev, CI integration tests, a cluster whose Terraform
		// predates this fix) — byte-identical to this service's behavior
		// before this env var existed in that case.
		migrationsDatabaseURL: getenv("MIGRATIONS_DATABASE_URL", databaseURL),
		migrationsPath:        getenv("MIGRATIONS_PATH", "migrations"),
		eventPublisher:        getenv("EVENT_PUBLISHER", ""),
		kafkaBrokers:          getenv("KAFKA_BROKERS", "localhost:9092"),
		relayInterval:         durationEnv("OUTBOX_RELAY_INTERVAL", time.Second),
		// Housekeeping (ADR-0026): a zero/negative value disables the
		// respective knob (durationEnv falls back to the default on a
		// malformed value; an explicit "0s" parses to 0 and disables).
		housekeepingInterval: durationEnvRaw("HOUSEKEEPING_INTERVAL", postgres.DefaultSweepInterval),
		idempotencyKeyTTL:    durationEnvRaw("IDEMPOTENCY_KEY_TTL", postgres.DefaultIdempotencyKeyTTL),
		outboxRetention:      durationEnvRaw("OUTBOX_RETENTION", postgres.DefaultOutboxRetention),
	}
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
