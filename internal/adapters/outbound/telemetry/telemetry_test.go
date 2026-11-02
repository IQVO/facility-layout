package telemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/telemetry"
)

// The single most important property of this adapter: a missing Collector
// degrades to "telemetry dropped", never to "the service will not start".
// If a blocking dial option is ever added, Setup will hang here and the
// test times out rather than the behaviour reaching production.
func TestSetupDoesNotBlockOnAnUnreachableCollector(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	var shutdown func(context.Context)

	go func() {
		var err error
		// Port 1 is reserved and nothing listens on it.
		shutdown, err = telemetry.Setup(ctx, "facility-layout", "test", "127.0.0.1:1")
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Setup with no collector running must not error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Setup blocked waiting for a collector; the OTLP exporters must be non-blocking")
	}

	if shutdown == nil {
		t.Fatal("Setup returned a nil shutdown func")
	}

	// A shutdown that cannot reach a Collector must still complete
	// promptly: the flush failing is telemetry's problem (logged inside
	// shutdown), never an unclean exit.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelShutdown()

	shutdownDone := make(chan struct{}, 1)
	go func() { shutdown(shutdownCtx); shutdownDone <- struct{}{} }()

	select {
	case <-shutdownDone:
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown hung with no collector running")
	}
}

func TestSetupDefaultsTheEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	shutdown, err := telemetry.Setup(ctx, "facility-layout", "test", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelShutdown()
		shutdown(shutdownCtx)
	})
}

func TestEnvironment(t *testing.T) {
	t.Setenv("ENVIRONMENT", "")
	if got := telemetry.Environment(); got != telemetry.DefaultEnvironment {
		t.Fatalf("expected %q with ENVIRONMENT unset, got %q", telemetry.DefaultEnvironment, got)
	}

	t.Setenv("ENVIRONMENT", "staging")
	if got := telemetry.Environment(); got != "staging" {
		t.Fatalf("expected %q, got %q", "staging", got)
	}
}

// An operator may write the Collector endpoint either way; both must reach
// the same gRPC target rather than one of them silently failing.
func TestSetupAcceptsBothEndpointForms(t *testing.T) {
	for _, endpoint := range []string{"127.0.0.1:1", "http://127.0.0.1:1", "https://127.0.0.1:1/", "  "} {
		t.Run(endpoint, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			shutdown, err := telemetry.Setup(ctx, "facility-layout", "test", endpoint)
			if err != nil {
				t.Fatalf("Setup(%q) errored: %v", endpoint, err)
			}
			shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancelShutdown()
			shutdown(shutdownCtx)
		})
	}
}
