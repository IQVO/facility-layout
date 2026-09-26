// Command facility is the composition root: it wires env config into
// adapters, adapters into use cases, and use cases into the HTTP router.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	inboundhttp "github.com/claudioed/facility-layout/internal/adapters/inbound/http"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/bootretry"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/telemetry"
	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/shared"
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

	// Observability is wired before any adapter is built, so a failure in
	// the database or migrations is itself traced and logged with the
	// right service identity. An unreachable Collector is not an error:
	// the OTLP exporters are non-blocking, and telemetry is dropped rather
	// than the service failing to start.
	shutdownTelemetry, err := telemetry.Setup(
		context.Background(),
		serviceName,
		resolveServiceVersion(),
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

	locationMetrics, err := telemetry.NewLocationMetrics()
	if err != nil {
		return err
	}

	logger.Info("telemetry configured",
		"service_name", serviceName,
		"service_version", resolveServiceVersion(),
		"environment", telemetry.Environment(),
		"otlp_endpoint", getenv("OTEL_EXPORTER_OTLP_ENDPOINT", telemetry.DefaultOTLPEndpoint),
	)

	httpAddr := getenv("HTTP_ADDR", ":8080")
	databaseURL := os.Getenv("DATABASE_URL")
	migrationsPath := getenv("MIGRATIONS_PATH", "migrations")
	eventPublisher := getenv("EVENT_PUBLISHER", "")
	kafkaBrokers := getenv("KAFKA_BROKERS", "localhost:9092")

	adapters, closeAdapters, err := buildAdapters(publisherConfig{
		databaseURL:    databaseURL,
		migrationsPath: migrationsPath,
		eventPublisher: eventPublisher,
		kafkaBrokers:   kafkaBrokers,
	}, logger)
	if err != nil {
		return err
	}
	defer closeAdapters()

	httpServer := &http.Server{
		Addr:              httpAddr,
		Handler:           inboundhttp.NewRouter(newServer(adapters, memory.SystemClock{}, locationMetrics), logger, inboundhttp.WithServiceName(serviceName)),
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

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
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
}

// newServer wires every use case over the chosen adapters. It is the one
// place in the codebase that knows about all three layers at once.
func newServer(a adapterSet, clock ports.Clock, locationMetrics ports.LocationMetrics) *inboundhttp.Server {
	return &inboundhttp.Server{
		RegisterSite: &usecases.RegisterSite{Sites: a.sites, Events: a.publisher, Clock: clock},
		GetSite:      &usecases.GetSite{Sites: a.sites},
		ListSites:    &usecases.ListSites{Sites: a.sites},

		RegisterZone: &usecases.RegisterZone{Sites: a.sites, Zones: a.zones, Events: a.publisher, Clock: clock},
		GetZone:      &usecases.GetZone{Zones: a.zones},
		ListZones:    &usecases.ListZones{Sites: a.sites, Zones: a.zones},

		RegisterAisle: &usecases.RegisterAisle{Zones: a.zones, Aisles: a.aisles, Events: a.publisher, Clock: clock},
		GetAisle:      &usecases.GetAisle{Aisles: a.aisles},
		ListAisles:    &usecases.ListAisles{Zones: a.zones, Aisles: a.aisles},

		RegisterLocationType: &usecases.RegisterLocationType{LocationTypes: a.locationTypes, Events: a.publisher, Clock: clock},
		GetLocationType:      &usecases.GetLocationType{LocationTypes: a.locationTypes},
		ListLocationTypes:    &usecases.ListLocationTypes{LocationTypes: a.locationTypes},

		DefinePlacementRule: &usecases.DefinePlacementRule{LocationTypes: a.locationTypes, Rules: a.rules, Events: a.publisher, Clock: clock},
		GetPlacementRule:    &usecases.GetPlacementRule{Rules: a.rules},
		ListPlacementRules:  &usecases.ListPlacementRules{Rules: a.rules},

		RegisterLocationSlot: &usecases.RegisterLocationSlot{
			Sites: a.sites, Zones: a.zones, Aisles: a.aisles, Slots: a.slots,
			LocationTypes: a.locationTypes, Rules: a.rules, Events: a.publisher, Clock: clock,
			Metrics: locationMetrics,
		},
		GetLocationSlot:           &usecases.GetLocationSlot{Slots: a.slots},
		GetLocationClassification: &usecases.GetLocationClassification{Slots: a.slots, Zones: a.zones},
		ListLocationsByRole:       &usecases.ListLocationsByRole{Sites: a.sites, Zones: a.zones, Slots: a.slots},
		DecommissionLocationSlot:  &usecases.DecommissionLocationSlot{Slots: a.slots, Events: a.publisher, Clock: clock},
		ImportFacilityLayout: &usecases.ImportFacilityLayout{
			Sites: a.sites, Zones: a.zones, Aisles: a.aisles, Slots: a.slots,
			LocationTypes: a.locationTypes, Rules: a.rules, Events: a.publisher, Clock: clock,
			Metrics: locationMetrics,
		},

		GetSiteLayout: &usecases.GetSiteLayout{Sites: a.sites, Zones: a.zones, Aisles: a.aisles, Slots: a.slots, Structures: a.structures},
		GetZoneGrid:   &usecases.GetZoneGrid{Zones: a.zones, Aisles: a.aisles, Slots: a.slots},

		SetLocationGeometry:    &usecases.SetLocationGeometry{Slots: a.slots, Events: a.publisher, Clock: clock},
		SetAisleGeometry:       &usecases.SetAisleGeometry{Aisles: a.aisles, Events: a.publisher, Clock: clock},
		RegisterFixedStructure: &usecases.RegisterFixedStructure{Sites: a.sites, Structures: a.structures, Events: a.publisher, Clock: clock},
		ListFixedStructures:    &usecases.ListFixedStructures{Sites: a.sites, Structures: a.structures},

		RegisterCrossAisle: &usecases.RegisterCrossAisle{
			Zones: a.zones, Aisles: a.aisles, CrossAisles: a.crossAisles, Events: a.publisher, Clock: clock,
		},
		GetZoneTravelGraph: &usecases.GetZoneTravelGraph{
			Zones: a.zones, Aisles: a.aisles, Slots: a.slots, CrossAisles: a.crossAisles,
		},
		EstimateTravelDistance: &usecases.EstimateTravelDistance{
			Zones: a.zones, Aisles: a.aisles, Slots: a.slots, CrossAisles: a.crossAisles,
		},
	}
}

// publisherConfig carries the composition-root inputs that decide which
// repositories and which EventPublisher buildAdapters wires.
type publisherConfig struct {
	databaseURL    string
	migrationsPath string
	// eventPublisher selects the outbound EventPublisher: "kafka" publishes
	// the Published Language to the integration topic; "" (default) uses the
	// Postgres outbox when a database is configured, or the log publisher when
	// running purely in-memory.
	eventPublisher string
	kafkaBrokers   string
}

// buildAdapters wires the Postgres adapters when DATABASE_URL is set, or
// falls back to the in-memory adapters for local development without a
// database. The EventPublisher is chosen independently: EVENT_PUBLISHER=kafka
// selects the Kafka integration publisher regardless of the repository choice,
// so the Published Language reaches the broker whether the store is Postgres
// or in-memory.
func buildAdapters(cfg publisherConfig, logger *slog.Logger) (adapterSet, func(), error) {
	noop := func() {}

	// The Kafka publishers, when selected, are independent of the store, so
	// they are built once here and folded into every branch's cleanup. When
	// EVENT_PUBLISHER=kafka the composition root fans out to BOTH the
	// integration topic (warehouse.facility.events, ADR-0009) and the analytics
	// topic (warehouse.facility.analytics, ADR-0010), so the OLTP integration
	// stream and the analytical read-model stream stay independent.
	kafkaEnabled := cfg.eventPublisher == "kafka"
	var (
		kafkaPublisher     *kafka.Publisher
		analyticsPublisher *kafka.AnalyticsPublisher
		fanOut             ports.EventPublisher
	)
	if kafkaEnabled {
		brokers := strings.Split(cfg.kafkaBrokers, ",")
		kafkaPublisher = kafka.NewPublisher(brokers, uuidLike)
		analyticsPublisher = kafka.NewAnalyticsPublisher(brokers, uuidLike)
		fanOut = fanOutPublisher{kafkaPublisher, analyticsPublisher}
		logger.Info("event publisher configured", "publisher", "kafka",
			"integration_topic", kafka.Topic, "analytics_topic", kafka.AnalyticsTopic, "brokers", brokers)
	}
	closeKafka := func() {
		if kafkaPublisher != nil {
			_ = kafkaPublisher.Close()
		}
		if analyticsPublisher != nil {
			_ = analyticsPublisher.Close()
		}
	}

	if cfg.databaseURL == "" {
		logger.Info("database url not configured; using in-memory adapters")
		pub := ports.EventPublisher(events.NewLogPublisher(logger))
		closeFn := noop
		if kafkaEnabled {
			pub = fanOut
			closeFn = closeKafka
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
		}, closeFn, nil
	}

	ctx := context.Background()

	// Retried, because in this fleet EVERY injected pod's first outbound TCP
	// dial is reset ~10s after the app starts (Istio native sidecars;
	// holdApplicationUntilProxyStarts is a no-op for them). A single attempt
	// turns that known, transient condition into CrashLoopBackOff. The retry
	// does not weaken the fail-closed rule: after the budget is exhausted
	// this still refuses to boot, reporting the real underlying error.
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error {
		return postgres.RunMigrations(cfg.databaseURL, cfg.migrationsPath)
	}); err != nil {
		return adapterSet{}, noop, err
	}

	pool, err := postgres.NewPool(ctx, cfg.databaseURL)
	if err != nil {
		return adapterSet{}, noop, err
	}
	// pgxpool does not itself dial until first use, so without this the
	// first real failure would surface inside a request instead of at boot.
	if err := bootretry.Retry(ctx, logger, "ping database", func() error {
		return pool.Ping(ctx)
	}); err != nil {
		pool.Close()
		return adapterSet{}, noop, err
	}

	// Default (no EVENT_PUBLISHER) keeps the Postgres outbox; kafka overrides it.
	pub := ports.EventPublisher(postgres.NewEventPublisher(pool))
	closeFn := pool.Close
	if kafkaEnabled {
		pub = fanOut
		closeFn = func() {
			closeKafka()
			pool.Close()
		}
	}

	return adapterSet{
		sites:         postgres.NewSiteRepo(pool),
		zones:         postgres.NewZoneRepo(pool),
		aisles:        postgres.NewAisleRepo(pool),
		slots:         postgres.NewSlotRepo(pool),
		locationTypes: postgres.NewLocationTypeRepo(pool),
		rules:         postgres.NewPlacementRuleRepo(pool),
		structures:    postgres.NewFixedStructureRepo(pool),
		crossAisles:   postgres.NewCrossAisleRepo(pool),
		publisher:     pub,
	}, closeFn, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// uuidLike mints the event_id stamped on each published integration event.
func uuidLike() string {
	return uuid.NewString()
}

// fanOutPublisher forwards every domain event to each wrapped EventPublisher in
// order, so a single EVENT_PUBLISHER=kafka run publishes to BOTH the integration
// topic and the analytics topic. It is a composition-root concern (ADR-0010): a
// publish failure on any target aborts and is returned, so the caller sees the
// first error rather than silently dropping a stream.
type fanOutPublisher []ports.EventPublisher

// Publish forwards event to every wrapped publisher, stopping at the first error.
func (f fanOutPublisher) Publish(ctx context.Context, event shared.DomainEvent) error {
	for _, p := range f {
		if err := p.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

// resolveServiceVersion reports this build's version: the ldflags-injected
// value when the binary was stamped, else SERVICE_VERSION, else "dev".
func resolveServiceVersion() string {
	if serviceVersion != "" {
		return serviceVersion
	}
	return getenv("SERVICE_VERSION", "dev")
}
