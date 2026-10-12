package usecases

import (
	"context"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

// ChangeSiteTransferCapability is the command behind
// PUT /sites/{siteCode}/transfer-capability (ADR-0035): it replaces a
// site's transfer capability with a full snapshot and, when that produces
// a new capability revision, raises SiteCapabilityChanged through the
// outbox in the same unit of work as the site row.
//
// The site's version guard (ADR-0025, extended by ADR-0035) makes two
// concurrent changes of the same site serialise: the loser's Save returns
// ports.ErrConcurrentModification, its outbox row rolls back with it, and
// no two published snapshots ever share a capability_revision.
type ChangeSiteTransferCapability struct {
	Sites  ports.SiteRepo
	Events ports.EventPublisher
	Clock  ports.Clock
	// UnitOfWork brackets Save + Publish atomically (ADR-0018, optional).
	UnitOfWork ports.UnitOfWork
}

// Execute applies the snapshot and returns the site as it now stands. An
// identical snapshot is an idempotent no-op: nothing is saved or published
// and the current site is returned unchanged.
func (uc *ChangeSiteTransferCapability) Execute(ctx context.Context, siteCode string, capability site.TransferCapability) (*site.Site, error) {
	s, err := uc.Sites.FindByCode(ctx, siteCode)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrSiteNotFound
	}
	changed, err := s.ChangeTransferCapability(capability)
	if err != nil {
		return nil, err
	}
	if !changed {
		return s, nil
	}
	err = atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
		if err := uc.Sites.Save(ctx, s); err != nil {
			return err
		}
		current := s.TransferCapability()
		return uc.Events.Publish(ctx, shared.NewSiteCapabilityChanged(uc.Clock.Now(), s.Code(),
			current.OriginEnabled(), current.DestinationEnabled(), s.CapabilityRevision()))
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}
