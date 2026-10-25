package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// heartbeatBatch bounds how many sites one pass re-asserts. The fleet has a
// few dozen sites; a pass that hits the bound simply continues on the next
// tick (the sites it did not reach are still due).
const heartbeatBatch = 500

// HeartbeatResult reports what one heartbeat pass did.
type HeartbeatResult struct {
	// Published is the number of SiteCapabilityChanged heartbeats written.
	Published int
	// Skipped is the number of due candidates that were not heartbeated: a
	// concurrent writer got there first (version guard, or another replica
	// already raised the heartbeat) or the site no longer qualified.
	Skipped int
}

// PublishSiteCapabilityHeartbeats is the periodic freshness heartbeat of
// ADR-0041. A downstream read model (network-inventory-planning) treats a
// site-capability fact older than its freshness window as stale, but
// SiteCapabilityChanged is raised only when the capability CHANGES
// (ADR-0035), so in a steady fleet every fact would age out. For every
// active site with a declared capability whose last heartbeat is at least
// Interval old, this re-publishes the CURRENT values as a new
// SiteCapabilityChanged with a fresh time (the consumers' as_of) and a
// revision advanced by one, through the transactional outbox, in the same
// unit of work as the site row.
//
// It never changes the PUT semantics: ChangeSiteTransferCapability still
// treats the value a site already holds as a no-op. Replicas may all run
// it: the "last heartbeat" column makes a site due once per Interval across
// the fleet, and the site's version guard (ADR-0025) turns the residual
// race into ports.ErrConcurrentModification for the loser, whose outbox
// row rolls back with it.
type PublishSiteCapabilityHeartbeats struct {
	Sites  ports.SiteRepo
	Due    ports.SiteCapabilityHeartbeatSource
	Events ports.EventPublisher
	Clock  ports.Clock
	// UnitOfWork brackets Save + Publish atomically (ADR-0018, optional).
	UnitOfWork ports.UnitOfWork
	// Interval is the heartbeat period; zero or less disables the use case.
	Interval time.Duration
}

// Execute runs one pass. A failure on one site does not stop the others; all
// failures are returned joined after the pass.
func (uc *PublishSiteCapabilityHeartbeats) Execute(ctx context.Context) (HeartbeatResult, error) {
	var res HeartbeatResult
	if uc.Interval <= 0 {
		return res, nil
	}
	codes, err := uc.Due.ListCapabilityHeartbeatDue(ctx, uc.Clock.Now().Add(-uc.Interval), heartbeatBatch)
	if err != nil {
		return res, fmt.Errorf("list sites due for a capability heartbeat: %w", err)
	}
	var errs []error
	for _, code := range codes {
		published, err := uc.heartbeat(ctx, code)
		switch {
		case errors.Is(err, ports.ErrConcurrentModification):
			res.Skipped++
		case err != nil:
			errs = append(errs, fmt.Errorf("capability heartbeat for site %s: %w", code, err))
		case published:
			res.Published++
		default:
			res.Skipped++
		}
	}
	return res, errors.Join(errs...)
}

// heartbeat re-asserts one site's capability; false means it was not due or
// no longer qualified when loaded.
func (uc *PublishSiteCapabilityHeartbeats) heartbeat(ctx context.Context, code string) (bool, error) {
	s, err := uc.Sites.FindByCode(ctx, code)
	if err != nil || s == nil {
		return false, err
	}
	now := uc.Clock.Now()
	if !s.HeartbeatTransferCapability(now, uc.Interval) {
		return false, nil
	}
	err = atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
		if err := uc.Sites.Save(ctx, s); err != nil {
			return err
		}
		current := s.TransferCapability()
		return uc.Events.Publish(ctx, shared.NewSiteCapabilityChanged(now, s.Code(),
			current.OriginEnabled(), current.DestinationEnabled(), s.CapabilityRevision()))
	})
	if err != nil {
		return false, err
	}
	return true, nil
}
