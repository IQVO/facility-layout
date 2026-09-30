// Package telemetry is the outbound adapter for OpenTelemetry: it builds
// the tracer, meter and logger providers, exports them over OTLP/gRPC to a
// Collector, and adapts the application's metric ports onto the OTel Meter
// API.
//
// It sits in the same tier as outbound/postgres and outbound/events: the
// domain and application layers never import it. The composition root wires
// Setup into startup, and the metric recorder into the use cases that own
// the business events being measured.
//
// A missing Collector must never break the service. Every exporter here is
// created non-blocking (no grpc.WithBlock dial option), so an unreachable
// endpoint degrades to "telemetry silently dropped", never to "the service
// will not start" or "requests hang".
package telemetry

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	logglobal "go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// DefaultOTLPEndpoint is the OTel Collector's standard gRPC receiver
// address. It is what Setup falls back to when no endpoint is configured.
const DefaultOTLPEndpoint = "localhost:4317"

// DefaultEnvironment is the deployment.environment.name reported when the
// ENVIRONMENT variable is unset.
const DefaultEnvironment = "local"

// exportTimeout caps a single OTLP export attempt. It is deliberately
// shorter than a typical Kubernetes termination grace period so a dead
// Collector cannot stretch out the pod's shutdown.
const exportTimeout = 5 * time.Second

// Setup builds and installs the global OpenTelemetry providers for all
// three signals — traces, metrics and logs — each exporting over OTLP/gRPC
// to otlpEndpoint (host:port, no scheme; DefaultOTLPEndpoint when empty).
// It also installs the W3C trace-context propagator and starts the Go
// runtime metrics collector.
//
// The returned shutdown func flushes and closes every provider; call it
// from the graceful-shutdown path. It is safe to call once.
//
// Setup never blocks on the Collector being reachable.
func Setup(ctx context.Context, serviceName, serviceVersion, otlpEndpoint string) (func(context.Context) error, error) {
	otlpEndpoint = normalizeEndpoint(otlpEndpoint)

	// Route the SDK's own diagnostics through slog before anything can
	// produce one, so the process never emits a non-JSON log line.
	otel.SetLogger(logr.New(NewLogSink(slog.Default())))

	res, err := newResource(serviceName, serviceVersion)
	if err != nil {
		return nil, err
	}

	var shutdowns []func(context.Context) error
	// shutdown flushes and closes every provider. A Collector that is down
	// makes the final flush fail, and that is logged rather than returned:
	// a service must not report an unclean exit just because its telemetry
	// backend was unreachable. Bound the context you pass in — the flush
	// otherwise retries for as long as the exporters are willing to.
	shutdown := func(ctx context.Context) error {
		for _, fn := range shutdowns {
			if err := fn(ctx); err != nil {
				slog.WarnContext(ctx, "telemetry provider shutdown did not flush cleanly", "error", err)
			}
		}
		return nil
	}
	// Any failure after a provider is already built must still tear that
	// provider down, or Setup leaks a background exporter goroutine.
	fail := func(err error) (func(context.Context) error, error) {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), exportTimeout)
		defer cancel()
		_ = shutdown(shutdownCtx)
		return nil, err
	}

	tracerProvider, err := newTracerProvider(ctx, res, otlpEndpoint)
	if err != nil {
		return fail(err)
	}
	shutdowns = append(shutdowns, tracerProvider.Shutdown)

	meterProvider, err := newMeterProvider(ctx, res, otlpEndpoint)
	if err != nil {
		return fail(err)
	}
	shutdowns = append(shutdowns, meterProvider.Shutdown)

	loggerProvider, err := newLoggerProvider(ctx, res, otlpEndpoint)
	if err != nil {
		return fail(err)
	}
	shutdowns = append(shutdowns, loggerProvider.Shutdown)

	installGlobals(tracerProvider, meterProvider, loggerProvider)

	if err := runtime.Start(runtime.WithMeterProvider(meterProvider)); err != nil {
		return fail(err)
	}

	return shutdown, nil
}

// newTracerProvider builds the trace signal's OTLP/gRPC exporter and batched
// tracer provider. The exporter is non-blocking: an unreachable Collector
// degrades to "spans silently dropped", never to a setup failure.
func newTracerProvider(ctx context.Context, res *resource.Resource, otlpEndpoint string) (*sdktrace.TracerProvider, error) {
	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(otlpEndpoint),
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithTimeout(exportTimeout),
	)
	if err != nil {
		return nil, err
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	), nil
}

// newMeterProvider builds the metric signal's OTLP/gRPC exporter and periodic
// reader, under the same non-blocking reachability contract as the trace
// exporter.
func newMeterProvider(ctx context.Context, res *resource.Resource, otlpEndpoint string) (*sdkmetric.MeterProvider, error) {
	metricExporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(otlpEndpoint),
		otlpmetricgrpc.WithInsecure(),
		otlpmetricgrpc.WithTimeout(exportTimeout),
	)
	if err != nil {
		return nil, err
	}
	return sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithResource(res),
	), nil
}

// newLoggerProvider builds the log signal's OTLP/gRPC exporter and batched
// log provider, under the same non-blocking reachability contract as the
// trace exporter.
func newLoggerProvider(ctx context.Context, res *resource.Resource, otlpEndpoint string) (*sdklog.LoggerProvider, error) {
	logExporter, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpoint(otlpEndpoint),
		otlploggrpc.WithInsecure(),
		otlploggrpc.WithTimeout(exportTimeout),
	)
	if err != nil {
		return nil, err
	}
	return sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	), nil
}

// installGlobals registers the built providers and the W3C trace-context
// propagator as the process-wide OTel globals, and routes the SDK's export
// errors to debug-level logging.
func installGlobals(tracerProvider *sdktrace.TracerProvider, meterProvider *sdkmetric.MeterProvider, loggerProvider *sdklog.LoggerProvider) {
	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	logglobal.SetLoggerProvider(loggerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	// Export failures are expected and harmless when no Collector is
	// running, so they are logged at debug rather than crashing the
	// service or flooding the log at error level.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		slog.Debug("opentelemetry error", "error", err)
	}))
}

// newResource describes this process to the Collector: which service it is,
// which build, and which environment it is deployed into.
func newResource(serviceName, serviceVersion string) (*resource.Resource, error) {
	return resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
			semconv.DeploymentEnvironmentNameKey.String(Environment()),
		),
	)
}

// normalizeEndpoint reduces an OTLP endpoint to the host:port form the
// gRPC exporters want. Collector endpoints are quoted both ways in the
// wild — "otel-collector:4317" and "http://otel-collector:4317" — and the
// OTel env-var convention is the URL form, so both are accepted here
// rather than making the operator guess which one this service wants.
func normalizeEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return DefaultOTLPEndpoint
	}
	for _, scheme := range []string{"http://", "https://"} {
		if trimmed, ok := strings.CutPrefix(endpoint, scheme); ok {
			endpoint = trimmed
			break
		}
	}
	return strings.TrimSuffix(endpoint, "/")
}

// Environment reports the deployment environment name from the ENVIRONMENT
// variable, defaulting to DefaultEnvironment.
func Environment() string {
	if v := os.Getenv("ENVIRONMENT"); v != "" {
		return v
	}
	return DefaultEnvironment
}
