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
//	SITE_CAPABILITY_HEARTBEAT_INTERVAL   site capability heartbeat period (default 5m, 0 disables; ADR 0041)
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
	"syscall"
	"time"

	"github.com/claudioed/facility-layout/internal/adapters/inbound/heartbeat"
	inboundhttp "github.com/claudioed/facility-layout/internal/adapters/inbound/http"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
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
	defer flushTelemetry(shutdownTelemetry)

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

	// The site capability heartbeat (ADR 0041); interval 0 disables it.
	heartbeatDone := startBackground(sigCtx, logger, capabilityHeartbeat(adapters, system.Clock{}, cfg.siteCapabilityHeartbeatInterval, logger), "site capability heartbeat running", errCh)
	defer func() { <-heartbeatDone }()

	select {
	case err := <-errCh:
		return err
	case <-sigCtx.Done():
	}

	return gracefulShutdown(logger, httpServer, readiness, stopRelay, relayDone)
}

// flushTelemetry flushes and stops the OTel providers within a 5 s budget,
// the last thing run() does.
func flushTelemetry(shutdown func(context.Context)) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdown(shutdownCtx)
}

// capabilityHeartbeat builds the ADR 0041 heartbeat loop over the wired
// adapters, or returns a nil worker (which startBackground skips) when the
// interval disables it.
func capabilityHeartbeat(a adapterSet, clock ports.Clock, interval time.Duration, logger *slog.Logger) interface {
	Run(context.Context) error
} {
	if interval <= 0 {
		logger.Info("site capability heartbeat disabled", "env", "SITE_CAPABILITY_HEARTBEAT_INTERVAL")
		return nil
	}
	return &heartbeat.Loop{
		Publisher: &usecases.PublishSiteCapabilityHeartbeats{
			Sites: a.sites, Due: a.capabilityHeartbeatDue, Events: a.publisher, Clock: clock,
			UnitOfWork: a.unitOfWork, Interval: interval,
		},
		Interval: interval,
		Logger:   logger,
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

// resolveServiceVersion reports this build's version: the ldflags-injected
// value when the binary was stamped, else SERVICE_VERSION, else "dev".
func resolveServiceVersion() string {
	if serviceVersion != "" {
		return serviceVersion
	}
	return getenv("SERVICE_VERSION", "dev")
}
