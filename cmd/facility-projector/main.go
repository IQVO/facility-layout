// Command facility-projector is the WRITER composition root of the
// facility-layout "Layout Catalog Growth & Change" data product. It consumes the
// analytics Kafka topic, projects each catalog-change event into the analytical
// Postgres database via the idempotent PostgresProjection, and serves only a
// health endpoint on an admin port. It is the single writer of the analytical
// database and serves no reports; the reader (cmd/facility-reports) is a
// separate deployable (ADR-0010).
//
// Consistent with the rest of the analytics pipeline, this process is
// trace-free: facility-layout has no observability/OTel package for it.
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

	inboundkafka "github.com/claudioed/facility-layout/internal/adapters/inbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/bootretry"
	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
)

// errMissingAnalyticsURL is returned when ANALYTICS_DATABASE_URL is unset: the
// projector is the writer of the analytical database and cannot start without it.
var errMissingAnalyticsURL = errors.New("ANALYTICS_DATABASE_URL is required")

func main() {
	if err := run(); err != nil {
		slog.Error("facility-projector exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	rootCtx := context.Background()

	adminAddr := getenv("ADMIN_ADDR", ":8091")
	analyticsURL := os.Getenv("ANALYTICS_DATABASE_URL")
	if analyticsURL == "" {
		return errMissingAnalyticsURL
	}
	kafkaBrokers := strings.Split(getenv("KAFKA_BROKERS", "localhost:9092"), ",")
	migrationsPath := getenv("ANALYTICS_MIGRATIONS_PATH", "migrations/analytics")

	// The projector owns the analytical schema: run its migrations on start.
	// Retried, because in this fleet EVERY injected pod's first outbound TCP
	// dial is reset ~10s after the app starts (Istio native sidecars). A
	// single attempt turns that known, transient condition into
	// CrashLoopBackOff; the retry still refuses to boot once the budget is
	// exhausted, reporting the real underlying error.
	if err := bootretry.Retry(rootCtx, logger, "run analytics migrations", func() error {
		return postgres.RunMigrations(analyticsURL, migrationsPath)
	}); err != nil {
		return err
	}

	pool, err := analyticsstore.NewPool(rootCtx, analyticsURL)
	if err != nil {
		return err
	}
	// pool.Close runs LAST (ADR-0020 §graceful shutdown, mirroring
	// order-management's ADR-0025 verbatim): registered here, near the
	// top of run(), so by defer's LIFO order it runs AFTER the consumer's
	// own deferred Close below, once the shutdown sequence has already
	// waited for the consumer's Run goroutine to actually finish.
	defer pool.Close()
	if err := bootretry.Retry(rootCtx, logger, "ping analytics database", func() error {
		return pool.Ping(rootCtx)
	}); err != nil {
		return err
	}

	projection := analyticsstore.NewPostgresProjection(pool)
	consumed := analyticsstore.NewConsumedEventsRepo(pool)
	consumer := inboundkafka.NewAnalyticsConsumer(kafkaBrokers, outboundkafka.AnalyticsTopic, projection, consumed, logger)
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

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if notReady.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"not_ready"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	srv := &http.Server{Addr: adminAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

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

// newLogger builds a JSON slog logger at the given level. The analytics
// processes log structured JSON but do not wire OTel, so this is a plain handler.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
