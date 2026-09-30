package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// AisleRepo is a pgxpool-backed implementation of ports.AisleRepo.
type AisleRepo struct {
	pool *pgxpool.Pool
}

// NewAisleRepo builds an AisleRepo over pool.
func NewAisleRepo(pool *pgxpool.Pool) *AisleRepo {
	return &AisleRepo{pool: pool}
}

const aisleColumns = `zone_id, aisle_code, sequence_hint, direction, status,
	centreline_start_x_m, centreline_start_y_m, centreline_start_z_m,
	centreline_end_x_m, centreline_end_y_m, centreline_end_z_m`

// Save upserts the aisle.
func (r *AisleRepo) Save(ctx context.Context, a *aisle.Aisle) error {
	centreline := a.Centreline()
	_, err := querierFrom(ctx, r.pool).Exec(ctx, `
		INSERT INTO aisles (
			id, zone_id, aisle_code, sequence_hint, direction, status,
			centreline_start_x_m, centreline_start_y_m, centreline_start_z_m,
			centreline_end_x_m, centreline_end_y_m, centreline_end_z_m
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (id) DO UPDATE SET
			sequence_hint = EXCLUDED.sequence_hint,
			direction = EXCLUDED.direction,
			status = EXCLUDED.status,
			centreline_start_x_m = EXCLUDED.centreline_start_x_m,
			centreline_start_y_m = EXCLUDED.centreline_start_y_m,
			centreline_start_z_m = EXCLUDED.centreline_start_z_m,
			centreline_end_x_m = EXCLUDED.centreline_end_x_m,
			centreline_end_y_m = EXCLUDED.centreline_end_y_m,
			centreline_end_z_m = EXCLUDED.centreline_end_z_m
	`, a.ID(), a.ZoneID(), a.AisleCode(), a.SequenceHint(), string(a.Direction()), string(a.Status()),
		nullableSegmentStartX(centreline), nullableSegmentStartY(centreline), nullableSegmentStartZ(centreline),
		nullableSegmentEndX(centreline), nullableSegmentEndY(centreline), nullableSegmentEndZ(centreline))
	return err
}

// FindByID returns the aisle, or (nil, nil) when it does not exist.
func (r *AisleRepo) FindByID(ctx context.Context, id string) (*aisle.Aisle, error) {
	row := querierFrom(ctx, r.pool).QueryRow(ctx, `SELECT `+aisleColumns+` FROM aisles WHERE id = $1`, id)
	a, err := scanAisle(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

// ListByZone returns every aisle in a zone, in walk order.
func (r *AisleRepo) ListByZone(ctx context.Context, zoneID string) ([]*aisle.Aisle, error) {
	rows, err := querierFrom(ctx, r.pool).Query(ctx, `SELECT `+aisleColumns+`
		FROM aisles WHERE zone_id = $1 ORDER BY sequence_hint, aisle_code`, zoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*aisle.Aisle, 0)
	for rows.Next() {
		a, err := scanAisle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// aisleScanner is the shared shape of pgx.Row and pgx.Rows.
type aisleScanner interface {
	Scan(dest ...any) error
}

func scanAisle(row aisleScanner) (*aisle.Aisle, error) {
	var zoneID, aisleCode, direction, status string
	var sequenceHint int
	var startXM, startYM, startZM, endXM, endYM, endZM *float64

	if err := row.Scan(&zoneID, &aisleCode, &sequenceHint, &direction, &status,
		&startXM, &startYM, &startZM, &endXM, &endYM, &endZM); err != nil {
		return nil, err
	}

	dir, err := shared.ParseDirection(direction)
	if err != nil {
		return nil, err
	}
	st, err := shared.ParseStatus(status)
	if err != nil {
		return nil, err
	}
	centreline, err := segmentFromColumns(startXM, startYM, startZM, endXM, endYM, endZM)
	if err != nil {
		return nil, err
	}
	return aisle.RehydrateAisle(zoneID, aisleCode, sequenceHint, dir, st, centreline), nil
}

// nullableSegmentStartX/Y/Z and nullableSegmentEndX/Y/Z return centreline's
// respective endpoint coordinate as a pointer, or nil when no centreline
// has ever been set (ADR-0017) — the all-or-nothing shape the
// 0003_geometry check constraint enforces.
func nullableSegmentStartX(centreline shared.Segment) *float64 {
	if centreline.IsZero() {
		return nil
	}
	v := centreline.Start().XM()
	return &v
}

func nullableSegmentStartY(centreline shared.Segment) *float64 {
	if centreline.IsZero() {
		return nil
	}
	v := centreline.Start().YM()
	return &v
}

func nullableSegmentStartZ(centreline shared.Segment) *float64 {
	if centreline.IsZero() {
		return nil
	}
	v := centreline.Start().ZM()
	return &v
}

func nullableSegmentEndX(centreline shared.Segment) *float64 {
	if centreline.IsZero() {
		return nil
	}
	v := centreline.End().XM()
	return &v
}

func nullableSegmentEndY(centreline shared.Segment) *float64 {
	if centreline.IsZero() {
		return nil
	}
	v := centreline.End().YM()
	return &v
}

func nullableSegmentEndZ(centreline shared.Segment) *float64 {
	if centreline.IsZero() {
		return nil
	}
	v := centreline.End().ZM()
	return &v
}

// segmentFromColumns rebuilds an aisle's centreline Segment from the
// possibly-all-NULL stored columns. The 0003_geometry check constraint
// guarantees the six columns are either all NULL or all set, so seeing
// startXM non-nil is sufficient to know the rest are too.
func segmentFromColumns(startXM, startYM, startZM, endXM, endYM, endZM *float64) (shared.Segment, error) {
	if startXM == nil {
		return shared.Segment{}, nil
	}
	start, err := shared.NewPoint3D(*startXM, *startYM, *startZM)
	if err != nil {
		return shared.Segment{}, err
	}
	end, err := shared.NewPoint3D(*endXM, *endYM, *endZM)
	if err != nil {
		return shared.Segment{}, err
	}
	return shared.NewSegment(start, end)
}
