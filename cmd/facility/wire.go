package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/facility-layout/internal/adapters/inbound/http"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/bootretry"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/telemetry"
	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/application/usecases"
)

// adapterSet is the concrete outbound side of the hexagon: Postgres
// repositories, the transactional outbox and the unit of work. There is no
// in-memory variant in the binary (internal/architecture
// TestRuntimeBinaryHasNoInMemoryAdapters).
type adapterSet struct {
	sites ports.SiteRepo
	// capabilityHeartbeatDue lists the sites due for a SiteCapabilityChanged
	// heartbeat (ADR 0041); the same Postgres repository as sites.
	capabilityHeartbeatDue ports.SiteCapabilityHeartbeatSource
	zones                  ports.ZoneRepo
	aisles                 ports.AisleRepo
	slots                  ports.SlotRepo
	locationTypes          ports.LocationTypeRepo
	rules                  ports.PlacementRuleRepo
	structures             ports.FixedStructureRepo
	crossAisles            ports.CrossAisleRepo
	publisher              ports.EventPublisher
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

		SetLocationGeometry:         &usecases.SetLocationGeometry{Slots: a.slots, Events: a.publisher, Clock: clock, UnitOfWork: a.unitOfWork},
		DeclareLocationHandlingUnit: &usecases.DeclareLocationHandlingUnit{Slots: a.slots, Events: a.publisher, Clock: clock, UnitOfWork: a.unitOfWork},
		SetAisleGeometry:            &usecases.SetAisleGeometry{Aisles: a.aisles, Events: a.publisher, Clock: clock, UnitOfWork: a.unitOfWork},
		RegisterFixedStructure:      &usecases.RegisterFixedStructure{Sites: a.sites, Structures: a.structures, Events: a.publisher, Clock: clock, UnitOfWork: a.unitOfWork},
		ListFixedStructures:         &usecases.ListFixedStructures{Sites: a.sites, Structures: a.structures},

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
	// siteCapabilityHeartbeatInterval is SITE_CAPABILITY_HEARTBEAT_INTERVAL
	// (ADR 0041): how often a site's capability fact is re-published so
	// downstream freshness windows hold. Zero disables the heartbeat.
	siteCapabilityHeartbeatInterval time.Duration
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

	siteRepo := postgres.NewSiteRepo(pool)
	base := adapterSet{
		sites:                  siteRepo,
		capabilityHeartbeatDue: siteRepo,
		zones:                  postgres.NewZoneRepo(pool),
		aisles:                 postgres.NewAisleRepo(pool),
		slots:                  postgres.NewSlotRepo(pool),
		locationTypes:          postgres.NewLocationTypeRepo(pool),
		rules:                  postgres.NewPlacementRuleRepo(pool),
		structures:             postgres.NewFixedStructureRepo(pool),
		crossAisles:            postgres.NewCrossAisleRepo(pool),
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
func setupServiceTelemetry(logger *slog.Logger, serviceName string) (func(context.Context), error) {
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
