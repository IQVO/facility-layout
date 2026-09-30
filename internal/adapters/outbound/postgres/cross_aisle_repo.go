package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/facility-layout/internal/domain/aisle"
)

// CrossAisleRepo is a pgxpool-backed implementation of ports.CrossAisleRepo.
type CrossAisleRepo struct {
	pool *pgxpool.Pool
}

// NewCrossAisleRepo builds a CrossAisleRepo over pool.
func NewCrossAisleRepo(pool *pgxpool.Pool) *CrossAisleRepo {
	return &CrossAisleRepo{pool: pool}
}

// Save upserts the cross-aisle.
func (r *CrossAisleRepo) Save(ctx context.Context, c *aisle.CrossAisle) error {
	_, err := querierFrom(ctx, r.pool).Exec(ctx, `
		INSERT INTO cross_aisles (zone_id, from_aisle, to_aisle, at_bay, decommissioned)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (zone_id, from_aisle, to_aisle, at_bay) DO UPDATE SET
			decommissioned = EXCLUDED.decommissioned
	`, c.ZoneID(), c.FromAisle(), c.ToAisle(), c.AtBay(), !c.IsActive())
	return err
}

// FindByAisles returns the cross-aisle connecting fromAisle and toAisle at
// atBay within zoneID (checked in both orientations, since the connection
// is symmetric), or (nil, nil) when none exists.
func (r *CrossAisleRepo) FindByAisles(ctx context.Context, zoneID, fromAisle, toAisle, atBay string) (*aisle.CrossAisle, error) {
	var decommissioned bool
	var storedFrom, storedTo string
	err := querierFrom(ctx, r.pool).QueryRow(ctx, `
		SELECT from_aisle, to_aisle, decommissioned FROM cross_aisles
		WHERE zone_id = $1 AND at_bay = $2
			AND ((from_aisle = $3 AND to_aisle = $4) OR (from_aisle = $4 AND to_aisle = $3))
	`, zoneID, atBay, fromAisle, toAisle).Scan(&storedFrom, &storedTo, &decommissioned)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return aisle.RehydrateCrossAisle(zoneID, storedFrom, storedTo, atBay, decommissioned), nil
}

// ListByZone returns every cross-aisle in a zone, ordered by from_aisle,
// to_aisle, at_bay.
func (r *CrossAisleRepo) ListByZone(ctx context.Context, zoneID string) ([]*aisle.CrossAisle, error) {
	rows, err := querierFrom(ctx, r.pool).Query(ctx, `
		SELECT from_aisle, to_aisle, at_bay, decommissioned FROM cross_aisles
		WHERE zone_id = $1 ORDER BY from_aisle, to_aisle, at_bay
	`, zoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*aisle.CrossAisle, 0)
	for rows.Next() {
		var fromAisle, toAisle, atBay string
		var decommissioned bool
		if err := rows.Scan(&fromAisle, &toAisle, &atBay, &decommissioned); err != nil {
			return nil, err
		}
		out = append(out, aisle.RehydrateCrossAisle(zoneID, fromAisle, toAisle, atBay, decommissioned))
	}
	return out, rows.Err()
}
