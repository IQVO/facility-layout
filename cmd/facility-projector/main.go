// Command facility-projector is the WRITER composition root of the
// facility-layout "Layout Catalog Growth & Change" data product. It consumes the
// analytics Kafka topic, projects each catalog-change event into the analytical
// Postgres database via the idempotent PostgresProjection, and serves only a
// health endpoint on an admin port. It is the single writer of the analytical
// database and serves no reports; the reader (cmd/facility-reports) is a
// separate deployable (ADR-0010).
//
// The process wires the same OTel telemetry as cmd/facility (ADR-0012): the
// chart injects OTEL_* env into this pod, and its admin server is traced and
// metered via otelchi/otelchimetric under its own service name.
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
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	inboundhttp "github.com/claudioed/facility-layout/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/facility-layout/internal/adapters/inbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/bootretry"
	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/telemetry"
	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultServiceName is the OTel service name reported when
// OTEL_SERVICE_NAME is unset (the chart sets it to "facility-projector").
const defaultServiceName = "facility-projector"

func main() {
	if err := run(); err != nil {
		slog.Error("facility-projector exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	serviceName := getenv("OTEL_SERVICE_NAME", defaultServiceName)

	logger := telemetry.NewLogger(os.Stdout, getenv("LOG_LEVEL", "info"), serviceName)
	slog.SetDefault(logger)

	shutdownTelemetry, err := setupTelemetry(serviceName)
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

	rootCtx := context.Background()

	// Fail fast, before any connection and before the admin listener binds:
	// a missing mandatory variable is a boot error that names it (ADR 0039).
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	adminAddr := getenv("ADMIN_ADDR", ":8091")
	analyticsURL := cfg.analyticsURL
	kafkaBrokers := cfg.brokers
	migrationsPath := getenv("ANALYTICS_MIGRATIONS_PATH", "migrations/analytics")

	pool, err := openAnalyticsPool(rootCtx, logger, analyticsURL, migrationsPath)
	if err != nil {
		return err
	}
	// pool.Close runs LAST (ADR-0020 §graceful shutdown, mirroring
	// order-management's ADR-0025 verbatim): registered here, near the
	// top of run(), so by defer's LIFO order it runs AFTER the consumer's
	// own deferred Close below, once the shutdown sequence has already
	// waited for the consumer's Run goroutine to actually finish.
	defer pool.Close()

	projection := analyticsstore.NewPostgresProjection(pool)
	consumed := analyticsstore.NewConsumedEventsRepo(pool)
	// The DLQ writer is the fleet-shared durable one (outbound/kafka's
	// NewDeadLetterWriter: RequireAll + Hash + 10ms), injected through
	// WithDLQWriter because the inbound consumer package cannot import an
	// outbound one (arch fitness rule). ADR-0020: a DLQ publish must not
	// report success before the broker stores the message — the source
	// offset is committed right after, so RequireNone (kafka-go's default)
	// would silently lose the poison message.
	consumer := inboundkafka.NewAnalyticsConsumer(kafkaBrokers, outboundkafka.AnalyticsTopic, cfg.group, projection, consumed, logger,
		inboundkafka.WithDLQWriter(outboundkafka.NewDeadLetterWriter(kafkaBrokers, outboundkafka.AnalyticsTopic+".dlq")))
	defer func() {
		if err := consumer.Close(); err != nil {
			logger.Error("error closing analytics consumer", "error", err)
		}
	}()

	// readiness backs GET /readyz (ADR-0020 §graceful shutdown, ADR 0039):
	// the drain gate, flipped as the FIRST step of the shutdown sequence
	// below, AND a Postgres ping AND a Kafka metadata check. /healthz is
	// unaffected -- it stays a pure liveness signal.
	readiness := newReadiness(pool.Ping, outboundkafka.NewBrokerCheck(kafkaBrokers).Check)

	srv := &http.Server{Addr: adminAddr, Handler: newAdminMux(readiness, serviceName), ReadHeaderTimeout: 5 * time.Second}

	go func() {
		logger.Info("projector admin server listening", "addr", adminAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("projector admin server failed", "error", err)
		}
	}()

	consumerCtx, cancelConsumer := context.WithCancel(context.Background())
	defer cancelConsumer()
	consumerDone := startConsumer(consumerCtx, logger, consumer, kafkaBrokers, cfg.group)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	// Graceful shutdown (ADR-0020, mirroring order-management's ADR-0025
	// §graceful shutdown verbatim), in order:
	//  1. Flip readiness to not-ready FIRST.
	//  2. Stop accepting new admin-server connections and drain
	//     in-flight requests, bounded by shutdownCtx.
	//  3. Stop the analytics consumer's loop cleanly: cancel its context
	//     (no new message is fetched/handled after this) and wait,
	//     bounded by the SAME shutdownCtx, for it to actually finish
	//     in-flight work (a message already being handled commits its
	//     offset, or dead-letters it, before Run returns) rather than
	//     merely asking it to stop and moving on.
	//  4. Only THEN do the deferred consumer.Close()/pool.Close() calls
	//     (registered earlier in this function), so by defer's LIFO
	//     order pool.Close() -- which closes the analytics pgx pool --
	//     runs LAST of all, after the consumer has already stopped
	//     touching it.
	readiness.SetNotReady()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := srv.Shutdown(ctx)

	cancelConsumer()
	select {
	case <-consumerDone:
	case <-ctx.Done():
		logger.Warn("analytics consumer did not stop before the shutdown deadline")
	}

	return shutdownErr
}

// startConsumer runs the analytics consumer's Run loop on ctx and
// returns a channel closed once Run has actually returned -- including
// having committed (or dead-lettered, ADR-0020 §DLQ) the offset for
// whatever message it was mid-handling when ctx is cancelled -- so
// graceful shutdown can wait for a REAL stop, not just fire-and-forget
// the cancel.
func startConsumer(ctx context.Context, logger *slog.Logger, consumer *inboundkafka.AnalyticsConsumer, brokers []string, group string) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		logger.Info("analytics consumer starting", "topic", outboundkafka.AnalyticsTopic, "group", group, "brokers", brokers)
		if err := consumer.Run(ctx); err != nil {
			logger.Error("analytics consumer stopped", "error", err)
		}
	}()
	return done
}

// setupTelemetry configures OTel for this process and returns the
// shutdown func; split out of run() purely to keep run()'s shutdown
// sequence readable as one block.
func setupTelemetry(serviceName string) (func(context.Context) error, error) {
	return telemetry.Setup(
		context.Background(),
		serviceName,
		getenv("SERVICE_VERSION", "dev"),
		getenv("OTEL_EXPORTER_OTLP_ENDPOINT", telemetry.DefaultOTLPEndpoint),
	)
}

// newReadiness builds the projector's /readyz gate: the drain gate AND a
// Postgres ping AND a Kafka metadata check, cached for a few seconds.
func newReadiness(pingPostgres, checkKafka func(context.Context) error) *inboundhttp.Readiness {
	return inboundhttp.NewReadiness(0, 0,
		inboundhttp.Dependency{Name: "postgres", Check: pingPostgres},
		inboundhttp.Dependency{Name: "kafka", Check: checkKafka},
	)
}

// newAdminMux builds the projector's admin endpoints behind the same
// otelchi tracing and otelchimetric HTTP metrics the main router carries
// (ADR-0012 — the chart injects OTEL_* env into this pod): /healthz is a
// pure liveness signal, never flipped by shutdown; /readyz is the drain gate
// plus the dependency checks, so a Kubernetes readinessProbe observes both the
// shutdown flip (ADR-0020) and an unreachable Postgres or Kafka (ADR 0039).
func newAdminMux(readiness *inboundhttp.Readiness, serviceName string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(serviceName, otelchi.WithChiRoutes(r)))
	metricCfg := otelchimetric.NewBaseConfig(serviceName)
	r.Use(otelchimetric.NewServerRequestDuration(metricCfg))
	r.Use(otelchimetric.NewServerActiveRequests(metricCfg))
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	r.Get("/readyz", inboundhttp.HandleReadyz(readiness))
	return r
}

// openAnalyticsPool runs the projector-owned analytical schema migrations,
// opens the pool, and verifies it with a ping — each under boot retry:
// in this fleet EVERY injected pod's first outbound TCP dial is reset ~10s
// after the app starts (Istio native sidecars). A single attempt turns that
// known, transient condition into CrashLoopBackOff; the retry still refuses
// to boot once the budget is exhausted, reporting the real underlying error.
func openAnalyticsPool(ctx context.Context, logger *slog.Logger, analyticsURL, migrationsPath string) (*pgxpool.Pool, error) {
	if err := bootretry.Retry(ctx, logger, "run analytics migrations", func() error {
		return postgres.RunMigrations(analyticsURL, migrationsPath)
	}); err != nil {
		return nil, err
	}

	pool, err := analyticsstore.NewPool(ctx, analyticsURL)
	if err != nil {
		return nil, err
	}
	if err := bootretry.Retry(ctx, logger, "ping analytics database", func() error {
		return pool.Ping(ctx)
	}); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
