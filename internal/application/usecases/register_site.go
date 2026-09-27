package usecases

import (
	"context"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

// RegisterSite adds a physical facility to the warehouse map. Site code
// uniqueness is enforced here rather than in the aggregate: a single
// aggregate cannot see its siblings.
type RegisterSite struct {
	Sites  ports.SiteRepo
	Events ports.EventPublisher
	Clock  ports.Clock
	// UnitOfWork brackets Save + Publish atomically (ADR-0018). Optional:
	// a nil value means "no transactional backing" and the two calls run
	// back to back, which is exactly the in-memory / log-publisher dev
	// configuration.
	UnitOfWork ports.UnitOfWork
}

// Execute registers the site and publishes SiteRegistered.
func (uc *RegisterSite) Execute(ctx context.Context, code, name string) (*site.Site, error) {
	existing, err := uc.Sites.FindByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrDuplicateSite
	}

	s, err := site.NewSite(code, name)
	if err != nil {
		return nil, err
	}
	err = atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
		if err := uc.Sites.Save(ctx, s); err != nil {
			return err
		}
		return uc.Events.Publish(ctx, shared.NewSiteRegistered(uc.Clock.Now(), s.Code(), s.Name()))
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// GetSite reads one Site by its code.
type GetSite struct {
	Sites ports.SiteRepo
}

// Execute returns the site, or ErrSiteNotFound.
func (uc *GetSite) Execute(ctx context.Context, code string) (*site.Site, error) {
	s, err := uc.Sites.FindByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrSiteNotFound
	}
	return s, nil
}

// ListSites reads every registered Site.
type ListSites struct {
	Sites ports.SiteRepo
}

// Execute returns every site on the map.
func (uc *ListSites) Execute(ctx context.Context) ([]*site.Site, error) {
	return uc.Sites.List(ctx)
}
