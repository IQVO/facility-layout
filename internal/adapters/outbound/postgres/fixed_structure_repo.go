package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/structure"
)

// FixedStructureRepo is a pgxpool-backed implementation of
// ports.FixedStructureRepo.
type FixedStructureRepo struct {
	pool *pgxpool.Pool
}

// NewFixedStructureRepo builds a FixedStructureRepo over pool.
func NewFixedStructureRepo(pool *pgxpool.Pool) *FixedStructureRepo {
	return &FixedStructureRepo{pool: pool}
}

// Save upserts the structure.
func (r *FixedStructureRepo) Save(ctx context.Context, f *structure.FixedStructure) error {
	footprint := f.Footprint()
	_, err := querierFrom(ctx, r.pool).Exec(ctx, `
		INSERT INTO fixed_structures (id, site_code, kind, x_m, y_m, z_m, width_m, depth_m, height_m, label)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			kind = EXCLUDED.kind,
			x_m = EXCLUDED.x_m,
			y_m = EXCLUDED.y_m,
			z_m = EXCLUDED.z_m,
			width_m = EXCLUDED.width_m,
			depth_m = EXCLUDED.depth_m,
			height_m = EXCLUDED.height_m,
			label = EXCLUDED.label
	`, f.ID(), f.SiteCode(), string(f.Kind()),
		footprint.Origin().XM(), footprint.Origin().YM(), footprint.Origin().ZM(),
		footprint.Size().WidthM(), footprint.Size().DepthM(), footprint.Size().HeightM(),
		f.Label())
	return err
}

// FindByID returns the structure, or (nil, nil) when it does not exist.
func (r *FixedStructureRepo) FindByID(ctx context.Context, id string) (*structure.FixedStructure, error) {
	row := querierFrom(ctx, r.pool).QueryRow(ctx, `
		SELECT site_code, kind, x_m, y_m, z_m, width_m, depth_m, height_m, label
		FROM fixed_structures WHERE id = $1
	`, id)
	f, err := scanFixedStructure(id, row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

// ListBySite returns every structure in a site, ordered by id.
func (r *FixedStructureRepo) ListBySite(ctx context.Context, siteCode string) ([]*structure.FixedStructure, error) {
	rows, err := querierFrom(ctx, r.pool).Query(ctx, `
		SELECT id, site_code, kind, x_m, y_m, z_m, width_m, depth_m, height_m, label
		FROM fixed_structures WHERE site_code = $1 ORDER BY id
	`, siteCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*structure.FixedStructure, 0)
	for rows.Next() {
		var id, site, kind, label string
		var xM, yM, zM, widthM, depthM, heightM float64
		if err := rows.Scan(&id, &site, &kind, &xM, &yM, &zM, &widthM, &depthM, &heightM, &label); err != nil {
			return nil, err
		}
		f, err := rehydrateFixedStructure(id, site, kind, xM, yM, zM, widthM, depthM, heightM, label)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// fixedStructureScanner is the shared shape of pgx.Row and pgx.Rows for a
// single-row scan that already knows its id from the query argument.
type fixedStructureScanner interface {
	Scan(dest ...any) error
}

func scanFixedStructure(id string, row fixedStructureScanner) (*structure.FixedStructure, error) {
	var siteCode, kind, label string
	var xM, yM, zM, widthM, depthM, heightM float64
	if err := row.Scan(&siteCode, &kind, &xM, &yM, &zM, &widthM, &depthM, &heightM, &label); err != nil {
		return nil, err
	}
	return rehydrateFixedStructure(id, siteCode, kind, xM, yM, zM, widthM, depthM, heightM, label)
}

func rehydrateFixedStructure(id, siteCode, kind string, xM, yM, zM, widthM, depthM, heightM float64, label string) (*structure.FixedStructure, error) {
	k, err := structure.ParseKind(kind)
	if err != nil {
		return nil, err
	}
	origin, err := shared.NewPoint3D(xM, yM, zM)
	if err != nil {
		return nil, err
	}
	size, err := shared.NewDimensions(widthM, depthM, heightM)
	if err != nil {
		return nil, err
	}
	footprint, err := shared.NewRect(origin, size)
	if err != nil {
		return nil, err
	}
	return structure.RehydrateFixedStructure(id, siteCode, k, footprint, label), nil
}
