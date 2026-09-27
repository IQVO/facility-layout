package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

// ZoneRepo is a pgxpool-backed implementation of ports.ZoneRepo.
type ZoneRepo struct {
	pool *pgxpool.Pool
}

// NewZoneRepo builds a ZoneRepo over pool.
func NewZoneRepo(pool *pgxpool.Pool) *ZoneRepo {
	return &ZoneRepo{pool: pool}
}

// Save upserts the zone. bay_pitch_m/level_pitch_m persist as NULL when
// unset (ADR-0017) — 0 the domain sentinel for "use the default", never a
// literal zero pitch.
func (r *ZoneRepo) Save(ctx context.Context, z *zone.Zone) error {
	_, err := querierFrom(ctx, r.pool).Exec(ctx, `
		INSERT INTO zones (id, site_code, area_code, zone_code, temperature_class, hazmat, status, bay_pitch_m, level_pitch_m)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET
			temperature_class = EXCLUDED.temperature_class,
			hazmat = EXCLUDED.hazmat,
			status = EXCLUDED.status,
			bay_pitch_m = EXCLUDED.bay_pitch_m,
			level_pitch_m = EXCLUDED.level_pitch_m
	`, z.ID(), z.SiteCode(), z.AreaCode(), z.ZoneCode(), string(z.TemperatureClass()), z.Hazmat(), string(z.Status()),
		nullablePitch(rawBayPitch(z)), nullablePitch(rawLevelPitch(z)))
	return err
}

// FindByID returns the zone, or (nil, nil) when it does not exist.
func (r *ZoneRepo) FindByID(ctx context.Context, id string) (*zone.Zone, error) {
	var siteCode, areaCode, zoneCode, temperatureClass, status string
	var hazmat bool
	var bayPitchM, levelPitchM *float64
	err := querierFrom(ctx, r.pool).QueryRow(ctx, `
		SELECT site_code, area_code, zone_code, temperature_class, hazmat, status, bay_pitch_m, level_pitch_m
		FROM zones WHERE id = $1
	`, id).Scan(&siteCode, &areaCode, &zoneCode, &temperatureClass, &hazmat, &status, &bayPitchM, &levelPitchM)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return rehydrateZone(siteCode, areaCode, zoneCode, temperatureClass, hazmat, status, bayPitchM, levelPitchM)
}

// ListBySite returns every zone in a site, ordered by id.
func (r *ZoneRepo) ListBySite(ctx context.Context, siteCode string) ([]*zone.Zone, error) {
	rows, err := querierFrom(ctx, r.pool).Query(ctx, `
		SELECT site_code, area_code, zone_code, temperature_class, hazmat, status, bay_pitch_m, level_pitch_m
		FROM zones WHERE site_code = $1 ORDER BY id
	`, siteCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*zone.Zone, 0)
	for rows.Next() {
		var site, areaCode, zoneCode, temperatureClass, status string
		var hazmat bool
		var bayPitchM, levelPitchM *float64
		if err := rows.Scan(&site, &areaCode, &zoneCode, &temperatureClass, &hazmat, &status, &bayPitchM, &levelPitchM); err != nil {
			return nil, err
		}
		z, err := rehydrateZone(site, areaCode, zoneCode, temperatureClass, hazmat, status, bayPitchM, levelPitchM)
		if err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

func rehydrateZone(siteCode, areaCode, zoneCode, temperatureClass string, hazmat bool, status string, bayPitchM, levelPitchM *float64) (*zone.Zone, error) {
	tc, err := shared.ParseTemperatureClass(temperatureClass)
	if err != nil {
		return nil, err
	}
	st, err := shared.ParseStatus(status)
	if err != nil {
		return nil, err
	}
	return zone.RehydrateZone(siteCode, areaCode, zoneCode, tc, hazmat, st, floatOrZero(bayPitchM), floatOrZero(levelPitchM)), nil
}

// rawBayPitch/rawLevelPitch return 0 (persisted as NULL) when the zone is
// using the domain default rather than an explicit override — comparing
// against the exported default constants keeps this file from having to
// know the zone's private field state.
func rawBayPitch(z *zone.Zone) float64 {
	if z.BayPitchM() == zone.DefaultBayPitchM {
		return 0
	}
	return z.BayPitchM()
}

func rawLevelPitch(z *zone.Zone) float64 {
	if z.LevelPitchM() == zone.DefaultLevelPitchM {
		return 0
	}
	return z.LevelPitchM()
}

func floatOrZero(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

// nullablePitch renders a zero pitch (the domain default sentinel) as SQL
// NULL, so a zone that never set an override round-trips to "still using
// the default" rather than an explicit, wrong zero.
func nullablePitch(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}
