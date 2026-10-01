# Structured logging

facility-layout logs with Go's standard library `log/slog`, emitting JSON to
stdout. There is no third-party logging dependency.

## Why `log/slog`

- **Zero new dependency.** `log/slog` shipped in Go 1.21 and this service is
  already on a current toolchain; pulling in zerolog or zap would add
  dependency weight for capability the standard library already provides.
- **Ecosystem standard.** `log/slog` is the shape the wider Go ecosystem
  (including `net/http`, `database/sql` drivers, and most middleware) is
  converging on for structured, leveled logging.
- **OTel-bridged.** `log/slog`'s `Handler` interface is exactly what the
  OpenTelemetry Go SDK's log bridge
  (`go.opentelemetry.io/contrib/bridges/otelslog`) expects — and this
  service now uses it: the same records go to stdout as JSON *and* to the
  Collector over OTLP.

This mirrors the logging decision made across the sibling warehouse-systems
services.

## Configuration

| Env var     | Values                              | Default | Effect                       |
|-------------|--------------------------------------|---------|-------------------------------|
| `LOG_LEVEL` | `debug`, `info`, `warn`, `error` (case-insensitive) | `info`  | Minimum level emitted |

Unrecognized values fall back to `info`.

## Output shape

Every log line is a single JSON object written to stdout via
`slog.NewJSONHandler`, e.g.:

```json
{"time":"2026-08-23T12:00:00Z","level":"INFO","msg":"http server listening","addr":":8080"}
{"time":"2026-08-23T12:00:01Z","level":"INFO","msg":"http request","method":"POST","path":"/sites","status":201,"duration_ms":4,"bytes":128,"request_id":"abc123","trace_id":"69b0970c53414467b350b36f7a1b04ac","span_id":"27d2246a94c0462b"}
{"time":"2026-08-23T12:00:01Z","level":"INFO","msg":"domain event published","event_name":"SiteRegistered","event_type":"com.warehouse.wms.facility-layout.site.SiteRegistered","payload":{"...":"..."}}
```

## Where it's wired in

- `cmd/facility/main.go` builds the process-wide `*slog.Logger` via
  `telemetry.NewLogger(os.Stdout, LOG_LEVEL, serviceName)` as the first step
  of `run()` and calls `slog.SetDefault(logger)` so any stdlib/third-party
  code that logs through `slog.Default()` picks it up too.
- `internal/adapters/outbound/telemetry/slogotel.go` is what that
  constructor builds: a JSON handler to stdout, fanned out to the
  OpenTelemetry log bridge, wrapped in a handler that stamps `trace_id` and
  `span_id` onto any record emitted inside an active span. See the
  [Observability](README.md#observability) section of the README.
- `internal/adapters/inbound/http/logging.go` provides `RequestLogger`, chi
  middleware that logs method, path, status, duration, response size, and
  the chi request ID for every HTTP request (`Error` level for 5xx
  responses, `Info` otherwise). It is installed in `NewRouter` right after
  `middleware.RequestID` and before `middleware.Recoverer`.
- `internal/adapters/outbound/events/log_publisher.go`'s `LogPublisher`
  (the default `EventPublisher` when no database is configured) logs each
  published domain event as a structured `slog.Info` call instead of a
  formatted string.
