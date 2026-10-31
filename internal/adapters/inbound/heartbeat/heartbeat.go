// Package heartbeat is the inbound timer adapter that drives the periodic
// SiteCapabilityChanged heartbeat (ADR-0041): on a fixed cadence it runs the
// PublishSiteCapabilityHeartbeats use case, which writes the events through
// the transactional outbox.
package heartbeat

import (
	"context"
	"log/slog"
	"time"

	"github.com/claudioed/facility-layout/internal/application/usecases"
)

// ticksPerInterval is how many times per heartbeat interval the loop wakes.
// A site is due once its last heartbeat is a full interval old; waking only
// once per interval would let scheduling jitter skip a tick and double the
// gap. With five wakeups the worst gap between two heartbeats of a site is
// interval + interval/5 (ADR-0041), so an interval of at most half the
// consumer's freshness window always holds the window.
const ticksPerInterval = 5

// Publisher is the use case the loop drives.
type Publisher interface {
	Execute(ctx context.Context) (usecases.HeartbeatResult, error)
}

// Loop runs a Publisher every Interval/ticksPerInterval until its context
// ends. Interval is the heartbeat period handed to the use case.
type Loop struct {
	Publisher Publisher
	Interval  time.Duration
	Logger    *slog.Logger
}

// TickEvery is the wake-up period for a heartbeat interval.
func TickEvery(interval time.Duration) time.Duration {
	if tick := interval / ticksPerInterval; tick > 0 {
		return tick
	}
	return interval
}

// Run passes once immediately (a pod booting after an outage refreshes the
// stale facts without waiting) and then every tick until ctx is cancelled.
// An Interval of zero or less disables the loop: Run returns nil at once. It
// never returns a non-nil error: a failed pass is logged and retried on the
// next tick.
func (l *Loop) Run(ctx context.Context) error {
	if l.Interval <= 0 {
		return nil
	}
	logger := l.Logger
	if logger == nil {
		logger = slog.Default()
	}
	for {
		res, err := l.Publisher.Execute(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			logger.Error("site capability heartbeat failed", "error", err, "published", res.Published)
		case res.Published > 0:
			logger.Info("site capability heartbeat", "published", res.Published, "skipped", res.Skipped)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(TickEvery(l.Interval)):
		}
	}
}
