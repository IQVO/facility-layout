package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// GLNRegistry is a pgxpool-backed implementation of ports.GLNRegistry: the
// location_glns table, whose primary key is the GLN itself, so one number can
// identify at most one location across sites, zones and slots (ADR-0045).
type GLNRegistry struct {
	pool *pgxpool.Pool
}

// NewGLNRegistry builds a GLNRegistry over pool.
func NewGLNRegistry(pool *pgxpool.Pool) *GLNRegistry {
	return &GLNRegistry{pool: pool}
}

// Claim inserts (gln, owner). Run inside the owner's unit of work, a
// concurrent claim of the same number blocks on the primary key until the
// first transaction ends: if it commits the second sees the conflict and
// reports false, if it rolls back the second wins. A repeat claim by the same
// owner is a no-op that reports true.
func (r *GLNRegistry) Claim(ctx context.Context, gln shared.GLN, owner string) (bool, error) {
	tag, err := querierFrom(ctx, r.pool).Exec(ctx,
		`INSERT INTO location_glns (gln, owner) VALUES ($1, $2) ON CONFLICT (gln) DO NOTHING`,
		string(gln), owner)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	var holder string
	if err := querierFrom(ctx, r.pool).QueryRow(ctx, `SELECT owner FROM location_glns WHERE gln = $1`, string(gln)).Scan(&holder); err != nil {
		return false, err
	}
	return holder == owner, nil
}

// nullableGLN maps the optional GLN to its nullable column: NULL when none
// was ever recorded (ADR-0045).
func nullableGLN(gln shared.GLN) *string {
	if gln.IsZero() {
		return nil
	}
	v := string(gln)
	return &v
}
