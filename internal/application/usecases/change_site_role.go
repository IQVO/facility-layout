package usecases

import (
	"context"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

// ChangeSiteRole is the command behind PUT /sites/{siteCode}/role
// (ADR-0037): it sets a site's role in the fulfilment network and, when that
// changes anything, raises SiteRoleChanged through the outbox in the same
// unit of work as the site row.
//
// The site's version guard (ADR-0025, extended to Site by ADR-0035) makes
// two concurrent changes of the same site serialise: the loser's Save
// returns ports.ErrConcurrentModification and its outbox row rolls back
// with it. A role change never touches the transfer capability.
type ChangeSiteRole struct {
	Sites  ports.SiteRepo
	Events ports.EventPublisher
	Clock  ports.Clock
	// UnitOfWork brackets Save + Publish atomically (ADR-0018, optional).
	UnitOfWork ports.UnitOfWork
}

// Execute applies the role and returns the site as it now stands. The
// current role is an idempotent no-op: nothing is saved or published.
func (uc *ChangeSiteRole) Execute(ctx context.Context, siteCode string, role site.Role) (*site.Site, error) {
	s, err := uc.Sites.FindByCode(ctx, siteCode)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrSiteNotFound
	}
	previous, changed, err := s.ChangeRole(role)
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
		return uc.Events.Publish(ctx, shared.NewSiteRoleChanged(uc.Clock.Now(), s.Code(), string(s.Role()), string(previous)))
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// ListSiteRoleTransferDefaults is the read behind
// GET /site-roles/transfer-defaults (ADR-0037): the role -> default transfer
// capability table, so the warehouse-infra seed and operators read one source
// of truth instead of hard-coding it. It reads no repository.
type ListSiteRoleTransferDefaults struct{}

// Execute returns one row per assignable role, in display order.
func (uc *ListSiteRoleTransferDefaults) Execute(_ context.Context) []site.RoleTransferDefault {
	return site.TransferDefaults()
}
