// Command facility-reports is the READER composition root of the
// facility-layout "Layout Catalog Growth & Change" data product. It opens the
// analytical Postgres database over a read-only pool and serves the
// catalog-growth report and its freshness over REST. It writes nothing: the
// writer (cmd/facility-projector) is a separate deployable and owns the schema
// (ADR-0010).
//
// The process wires the same OTel telemetry as cmd/facility (ADR-0012): the
// chart injects OTEL_* env into this pod, so its HTTP router is traced and
// metered via otelchi/otelchimetric under its own service name.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	inboundhttp "github.com/claudioed/facility-layout/internal/adapters/inbound/http"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/bootretry"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/telemetry"
)

// errMissingAnalyticsURL is returned when ANALYTICS_DATABASE_URL is unset.
var errMissingAnalyticsURL = errors.New("ANALYTICS_DATABASE_URL is required")

// defaultServiceName is the OTel service name reported when
// OTEL_SERVICE_NAME is unset (the chart sets it to "facility-reports").
const defaultServiceName = "facility-reports"

func main() {
	if err := run(); err != nil {
		slog.Error("facility-reports exited with error", "error", err)
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

	httpAddr := getenv("HTTP_ADDR", ":8092")
	analyticsURL := os.Getenv("ANALYTICS_DATABASE_URL")
	if analyticsURL == "" {
		return errMissingAnalyticsURL
	}

	// Read-only pool: even a bug in the reader cannot mutate the read model, on
	// top of the read-only database role ANALYTICS_DATABASE_URL should use.
	pool, err := analyticsstore.NewReadOnlyPool(rootCtx, analyticsURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	// Retried, because in this fleet EVERY injected pod's first outbound TCP
	// dial is reset ~10s after the app starts (Istio native sidecars). A
	// single attempt turns that known, transient condition into
	// CrashLoopBackOff; the retry still refuses to boot once the budget is
	// exhausted, reporting the real underlying error.
	if err := bootretry.Retry(rootCtx, logger, "ping analytics database", func() error {
		return pool.Ping(rootCtx)
	}); err != nil {
		return err
	}

	handlers := &inboundhttp.ReportsHandlers{Store: analyticsstore.NewPostgresReport(pool)}
	// readiness backs GET /readyz (ADR-0020 §graceful shutdown): flipped
	// to not-ready as the FIRST step of the shutdown sequence below,
	// before the HTTP server itself stops accepting connections.
	readiness := &inboundhttp.Readiness{}
	handlers.Readiness = readiness
	router := inboundhttp.NewReportsRouter(handlers, logger, inboundhttp.WithServiceName(serviceName))

	srv := &http.Server{Addr: httpAddr, Handler: router, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		logger.Info("reports server listening", "addr", httpAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("reports server failed", "error", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	// Graceful shutdown (ADR-0020, mirroring order-management's ADR-0025
	// §graceful shutdown verbatim): flip readiness to not-ready FIRST,
	// then drain in-flight HTTP requests, then (via the deferred
	// pool.Close() registered above) close the read-only pgx pool LAST.
	// This process has no Kafka consumer/producer to stop -- it is a
	// pure read-only reader over Postgres.
	readiness.SetNotReady()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
