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
	// GLNs claims the site's GS1 Global Location Number (ADR-0045).
	// Optional: required only when a request carries a GLN.
	GLNs ports.GLNRegistry
}

// Execute registers the site with its role (ADR-0037; UNSPECIFIED when the
// caller states none) and publishes SiteRegistered carrying that role.
func (uc *RegisterSite) Execute(ctx context.Context, code, name string, role site.Role) (*site.Site, error) {
	return uc.ExecuteWithGLN(ctx, code, name, role, "")
}

// ExecuteWithGLN is Execute plus the OPTIONAL GS1 Global Location Number
// (ADR-0045). An empty gln means none recorded; any other value must be a
// valid GLN (shared.ErrInvalidGLN) that no other location holds
// (ErrGLNAlreadyAssigned).
func (uc *RegisterSite) ExecuteWithGLN(ctx context.Context, code, name string, role site.Role, glnRaw string) (*site.Site, error) {
	gln, err := parseOptionalGLN(glnRaw)
	if err != nil {
		return nil, err
	}
	existing, err := uc.Sites.FindByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrDuplicateSite
	}

	s, err := site.NewSiteWithRole(code, name, role)
	if err != nil {
		return nil, err
	}
	if !gln.IsZero() {
		if err := s.AssignGLN(gln); err != nil {
			return nil, err
		}
	}
	err = atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
		if err := claimGLN(ctx, uc.GLNs, s.GLN(), siteGLNOwner(s.Code())); err != nil {
			return err
		}
		if err := uc.Sites.Save(ctx, s); err != nil {
			return err
		}
		return uc.Events.Publish(ctx, shared.NewSiteRegistered(uc.Clock.Now(), s.Code(), s.Name(), string(s.Role())).WithGLN(s.GLN()))
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
