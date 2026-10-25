package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

// SiteRepo is a pgxpool-backed implementation of ports.SiteRepo.
type SiteRepo struct {
	pool *pgxpool.Pool
}

// NewSiteRepo builds a SiteRepo over pool.
func NewSiteRepo(pool *pgxpool.Pool) *SiteRepo {
	return &SiteRepo{pool: pool}
}

const siteColumns = `code, name, status, transfer_origin_enabled, transfer_destination_enabled, capability_revision, version, site_role`

// Save upserts the site, version-guarded against a concurrent writer
// (ADR-0025, extended to Site by ADR-0035): on an existing row it only
// applies when the row's current version still matches s.Version(), and
// always advances the row by exactly one version. Two concurrent transfer
// capability changes therefore cannot both commit the same
// capability_revision: the loser gets ports.ErrConcurrentModification and
// its SiteCapabilityChanged outbox row rolls back with it. The same guard
// serialises role changes (ADR-0037).
func (r *SiteRepo) Save(ctx context.Context, s *site.Site) error {
	capability := s.TransferCapability()
	tag, err := querierFrom(ctx, r.pool).Exec(ctx, `
		INSERT INTO sites (`+siteColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, 1, $8)
		ON CONFLICT (code) DO UPDATE SET
			name = EXCLUDED.name,
			site_role = EXCLUDED.site_role,
			status = EXCLUDED.status,
			transfer_origin_enabled = EXCLUDED.transfer_origin_enabled,
			transfer_destination_enabled = EXCLUDED.transfer_destination_enabled,
			capability_revision = EXCLUDED.capability_revision,
			version = sites.version + 1
		WHERE sites.version = $7
	`, s.Code(), s.Name(), string(s.Status()),
		capability.OriginEnabled(), capability.DestinationEnabled(), s.CapabilityRevision(), s.Version(), string(s.Role()))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// The ON CONFLICT arm matched an existing row whose version no
		// longer equals the one s was loaded at.
		return ports.ErrConcurrentModification
	}
	return nil
}

// FindByCode returns the site, or (nil, nil) when it does not exist.
func (r *SiteRepo) FindByCode(ctx context.Context, code string) (*site.Site, error) {
	row := querierFrom(ctx, r.pool).QueryRow(ctx, `SELECT `+siteColumns+` FROM sites WHERE code = $1`, code)
	s, err := scanSite(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return s, err
}

// List returns every site, ordered by code.
func (r *SiteRepo) List(ctx context.Context) ([]*site.Site, error) {
	rows, err := querierFrom(ctx, r.pool).Query(ctx, `SELECT `+siteColumns+` FROM sites ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*site.Site, 0)
	for rows.Next() {
		s, err := scanSite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// scanSite rehydrates one sites row selected with siteColumns.
func scanSite(row pgx.Row) (*site.Site, error) {
	var (
		code, name, status, role   string
		originEnabled, destEnabled bool
		capabilityRevision         int64
		version                    int
	)
	if err := row.Scan(&code, &name, &status, &originEnabled, &destEnabled, &capabilityRevision, &version, &role); err != nil {
		return nil, err
	}
	parsed, err := shared.ParseStatus(status)
	if err != nil {
		return nil, err
	}
	parsedRole, err := site.ParseRole(role)
	if err != nil {
		return nil, err
	}
	return site.RehydrateSite(code, name, parsedRole, parsed, site.NewTransferCapability(originEnabled, destEnabled), capabilityRevision, version), nil
}
