package bootretry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

var errDial = errors.New("read: connection reset by peer")

// The case this exists for: this fleet's Istio native sidecars reset every
// pod's FIRST outbound dial ~10s after the app starts. One attempt turns
// that transient into CrashLoopBackOff.
func TestRetry_SurvivesAFirstAttemptFailure(t *testing.T) {
	attempts := 0
	err := RetryWithDelay(context.Background(), quietLogger(), "run migrations", time.Millisecond, func() error {
		attempts++
		if attempts == 1 {
			return errDial
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retry returned %v, want nil after a recoverable first failure", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestRetry_SucceedsFirstTimeWithoutWaiting(t *testing.T) {
	attempts := 0
	start := time.Now()
	if err := Retry(context.Background(), quietLogger(), "ping", func() error {
		attempts++
		return nil
	}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	// A healthy start must not pay the backoff: this runs before the
	// service listens, so every second here is a second of boot latency.
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("a first-attempt success took %v; it must not sleep", elapsed)
	}
}

// The retry must NOT weaken the fail-closed rule: a genuinely unreachable
// dependency still refuses to boot, and still reports its real cause
// rather than a generic timeout.
func TestRetry_GivesUpAndReportsTheLastError(t *testing.T) {
	attempts := 0
	permanent := errors.New("password authentication failed")
	err := RetryWithDelay(context.Background(), quietLogger(), "run migrations", time.Millisecond, func() error {
		attempts++
		return permanent
	})
	if err == nil {
		t.Fatal("retry must fail when every attempt fails; a silent success would be a fallback to no database")
	}
	if !errors.Is(err, permanent) {
		t.Fatalf("err = %v, want it to wrap the real cause", err)
	}
	if attempts != BootRetries {
		t.Fatalf("attempts = %d, want the full budget of %d", attempts, BootRetries)
	}
}

// A shutdown during boot must abandon the retries rather than sleep out
// the whole budget while the kubelet waits to kill the process.
func TestRetry_StopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0

	done := make(chan error, 1)
	go func() {
		done <- RetryWithDelay(ctx, quietLogger(), "run migrations", time.Millisecond, func() error {
			attempts++
			if attempts == 1 {
				cancel()
			}
			return errDial
		})
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error when the context was cancelled mid-retry")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want it to report cancellation", err)
		}
		if attempts != 1 {
			t.Fatalf("attempts = %d, want the retry abandoned after the cancel", attempts)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retry ignored its cancelled context and kept sleeping")
	}
}

// The budget must outlast the ~10s first-dial reset, or the retry is
// decorative. Asserted arithmetically so tuning the constants cannot
// silently drop below the thing they exist to survive.
func TestRetryBudget_OutlastsTheFirstDialReset(t *testing.T) {
	total := time.Duration(0)
	delay := BootRetryDelay
	for i := 1; i < BootRetries; i++ {
		total += delay
		delay *= 2
	}
	if total < 15*time.Second {
		t.Fatalf("retry budget is %v; it must comfortably exceed the ~10s first-dial reset", total)
	}
}
