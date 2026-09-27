package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

// Save upserts the site.
func (r *SiteRepo) Save(ctx context.Context, s *site.Site) error {
	_, err := querierFrom(ctx, r.pool).Exec(ctx, `
		INSERT INTO sites (code, name, status)
		VALUES ($1, $2, $3)
		ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name, status = EXCLUDED.status
	`, s.Code(), s.Name(), string(s.Status()))
	return err
}

// FindByCode returns the site, or (nil, nil) when it does not exist.
func (r *SiteRepo) FindByCode(ctx context.Context, code string) (*site.Site, error) {
	var name, status string
	err := querierFrom(ctx, r.pool).QueryRow(ctx, `SELECT name, status FROM sites WHERE code = $1`, code).Scan(&name, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	parsed, err := shared.ParseStatus(status)
	if err != nil {
		return nil, err
	}
	return site.RehydrateSite(code, name, parsed), nil
}

// List returns every site, ordered by code.
func (r *SiteRepo) List(ctx context.Context) ([]*site.Site, error) {
	rows, err := querierFrom(ctx, r.pool).Query(ctx, `SELECT code, name, status FROM sites ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*site.Site, 0)
	for rows.Next() {
		var code, name, status string
		if err := rows.Scan(&code, &name, &status); err != nil {
			return nil, err
		}
		parsed, err := shared.ParseStatus(status)
		if err != nil {
			return nil, err
		}
		out = append(out, site.RehydrateSite(code, name, parsed))
	}
	return out, rows.Err()
}
