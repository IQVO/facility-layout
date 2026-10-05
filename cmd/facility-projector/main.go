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
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	inboundkafka "github.com/claudioed/facility-layout/internal/adapters/inbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/bootretry"
	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/telemetry"
	"github.com/jackc/pgx/v5/pgxpool"
)

// errMissingAnalyticsURL is returned when ANALYTICS_DATABASE_URL is unset: the
// projector is the writer of the analytical database and cannot start without it.
var errMissingAnalyticsURL = errors.New("ANALYTICS_DATABASE_URL is required")

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

	rootCtx := context.Background()

	adminAddr := getenv("ADMIN_ADDR", ":8091")
	analyticsURL := os.Getenv("ANALYTICS_DATABASE_URL")
	if analyticsURL == "" {
		return errMissingAnalyticsURL
	}
	kafkaBrokers := strings.Split(getenv("KAFKA_BROKERS", "localhost:9092"), ",")
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
	consumer := inboundkafka.NewAnalyticsConsumer(kafkaBrokers, outboundkafka.AnalyticsTopic, projection, consumed, logger,
		inboundkafka.WithDLQWriter(outboundkafka.NewDeadLetterWriter(kafkaBrokers, outboundkafka.AnalyticsTopic+".dlq")))
	defer func() {
		if err := consumer.Close(); err != nil {
			logger.Error("error closing analytics consumer", "error", err)
		}
	}()

	// notReady backs GET /readyz (ADR-0020 §graceful shutdown): flipped
	// to 1 as the FIRST step of the shutdown sequence below, before the
	// admin server itself stops accepting connections, so a Kubernetes
	// readinessProbe has a chance to observe the flip and stop routing
	// new traffic during the drain window that follows. /healthz is
	// unaffected -- it stays a pure liveness signal, never flipped by
	// shutdown.
	var notReady atomic.Bool

	srv := &http.Server{Addr: adminAddr, Handler: newAdminMux(&notReady, serviceName), ReadHeaderTimeout: 5 * time.Second}

	go func() {
		logger.Info("projector admin server listening", "addr", adminAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("projector admin server failed", "error", err)
		}
	}()

	consumerCtx, cancelConsumer := context.WithCancel(context.Background())
	defer cancelConsumer()
	// consumerDone closes once the consumer's Run goroutine has actually
	// returned -- including having committed (or dead-lettered, ADR-0020
	// §DLQ) the offset for whatever message it was mid-handling when
	// cancelConsumer is called -- so graceful shutdown can wait for a REAL
	// stop, not just fire-and-forget the cancel.
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		logger.Info("analytics consumer starting", "topic", outboundkafka.AnalyticsTopic, "group", inboundkafka.AnalyticsConsumerGroup, "brokers", kafkaBrokers)
		if err := consumer.Run(consumerCtx); err != nil {
			logger.Error("analytics consumer stopped", "error", err)
		}
	}()

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
	notReady.Store(true)

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

// newAdminMux builds the projector's admin endpoints behind the same
// otelchi tracing and otelchimetric HTTP metrics the main router carries
// (ADR-0012 — the chart injects OTEL_* env into this pod): /healthz is a
// pure liveness signal, never flipped by shutdown; /readyz mirrors notReady
// so a Kubernetes readinessProbe observes the shutdown flip (ADR-0020).
func newAdminMux(notReady *atomic.Bool, serviceName string) http.Handler {
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
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if notReady.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"not_ready"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
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
