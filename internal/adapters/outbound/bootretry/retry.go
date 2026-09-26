// Package bootretry provides a shared boot-time retry helper for every
// composition root in this repo (cmd/facility, cmd/facility-projector,
// cmd/facility-reports, cmd/mcp).
//
// Every injected pod's FIRST outbound TCP dial (Postgres, Kafka) in this
// fleet's cluster fails with "read: connection reset by peer" ~10s after
// the app starts: Istio 1.30 runs native sidecars (istio-proxy as an init
// container with restartPolicy=Always), so `holdApplicationUntilProxyStarts`
// is a no-op and the app can start dialing before the sidecar's outbound
// listener has finished warming up. Without a retry, that single transient
// failure at boot makes a fail-fast binary exit 1, and kubelet restarts it
// — CrashLoopBackOff noise on nearly every rollout even though the second
// attempt (after the restart) always succeeds.
//
// Retrying the FIRST boot-time dial with a short exponential backoff makes
// the process survive the warm-up window itself, in-process, without
// weakening the fail-closed rule: a genuinely unreachable database still
// exhausts the retry budget and refuses to boot, reporting the real
// underlying error rather than a generic timeout.
//
// This mirrors the reference implementation shipped in the sibling
// network-fulfillment repo's cmd/netfulfil/main.go.
package bootretry

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// BootRetries and BootRetryDelay bound the startup retry budget.
//
// ~31s total (1+2+4+8+16), comfortably past the ~10s first-dial reset and
// still far inside the liveness probe's own tolerance, so a genuinely
// unreachable dependency still fails the pod rather than hanging it.
const (
	BootRetries    = 5
	BootRetryDelay = time.Second
)

// Retry runs op with the default boot-time backoff, returning the LAST
// error so a permanent failure still reports its real cause rather than a
// generic "timed out".
func Retry(ctx context.Context, logger *slog.Logger, what string, op func() error) error {
	return RetryWithDelay(ctx, logger, what, BootRetryDelay, op)
}

// RetryWithDelay is Retry with the base delay injected, so tests can
// exercise the give-up path without sleeping out the real ~31s budget.
func RetryWithDelay(ctx context.Context, logger *slog.Logger, what string, base time.Duration, op func() error) error {
	delay := base
	var err error
	for attempt := 1; attempt <= BootRetries; attempt++ {
		if err = op(); err == nil {
			if attempt > 1 {
				logger.Info("succeeded after retry", "op", what, "attempt", attempt)
			}
			return nil
		}
		if attempt == BootRetries {
			break
		}
		logger.Warn("retrying", "op", what, "attempt", attempt, "in", delay, "err", err)
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w", what, ctx.Err())
		case <-time.After(delay):
		}
		delay *= 2
	}
	return fmt.Errorf("%s (after %d attempts): %w", what, BootRetries, err)
}
