package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/slot"
)

// SlotRepo is a pgxpool-backed implementation of ports.SlotRepo. The
// LocationCode's seven segments are stored as real columns alongside the
// canonical string form, so the zone-grid read model can be answered with
// an indexed query rather than by parsing codes at read time.
type SlotRepo struct {
	pool *pgxpool.Pool
}

// NewSlotRepo builds a SlotRepo over pool.
func NewSlotRepo(pool *pgxpool.Pool) *SlotRepo {
	return &SlotRepo{pool: pool}
}

const slotColumns = `code, site_segment, area_segment, zone_segment, aisle_segment,
	bay_segment, level_segment, position_segment, location_type, role, dock_flow, activities,
	max_weight_kg, max_volume_m3, status, x_m, y_m, z_m, width_m, depth_m, height_m, pick_sequence`

// Save upserts the slot.
func (r *SlotRepo) Save(ctx context.Context, s *slot.LocationSlot) error {
	code := s.Code()
	functional := s.Functional()
	position := s.Position()
	dimensions := s.Dimensions()
	_, err := querierFrom(ctx, r.pool).Exec(ctx, `
		INSERT INTO location_slots (
			code, site_segment, area_segment, zone_segment, aisle_segment,
			bay_segment, level_segment, position_segment,
			zone_id, aisle_id, location_type, role, dock_flow, activities,
			max_weight_kg, max_volume_m3, status,
			x_m, y_m, z_m, width_m, depth_m, height_m, pick_sequence
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
		ON CONFLICT (code) DO UPDATE SET
			location_type = EXCLUDED.location_type,
			role = EXCLUDED.role,
			dock_flow = EXCLUDED.dock_flow,
			activities = EXCLUDED.activities,
			max_weight_kg = EXCLUDED.max_weight_kg,
			max_volume_m3 = EXCLUDED.max_volume_m3,
			status = EXCLUDED.status,
			x_m = EXCLUDED.x_m,
			y_m = EXCLUDED.y_m,
			z_m = EXCLUDED.z_m,
			width_m = EXCLUDED.width_m,
			depth_m = EXCLUDED.depth_m,
			height_m = EXCLUDED.height_m,
			pick_sequence = EXCLUDED.pick_sequence
	`,
		code.String(), code.Site(), code.Area(), code.Zone(), code.Aisle(),
		code.Bay(), code.Level(), code.Position(),
		code.ZoneID(), code.AisleID(), s.LocationType(), string(s.Role()),
		nullableDockFlow(functional), activityColumn(functional),
		nullableFloat(s.Capacity()), nullableVolume(s.Capacity()), string(s.Status()),
		nullablePositionX(position), nullablePositionY(position), nullablePositionZ(position),
		nullableDimensionWidth(dimensions), nullableDimensionDepth(dimensions), nullableDimensionHeight(dimensions),
		s.PickSequence())
	return err
}

// FindByCode returns the slot, or (nil, nil) when it does not exist.
func (r *SlotRepo) FindByCode(ctx context.Context, code shared.LocationCode) (*slot.LocationSlot, error) {
	row := querierFrom(ctx, r.pool).QueryRow(ctx, `SELECT `+slotColumns+` FROM location_slots WHERE code = $1`, code.String())

	s, err := scanSlot(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

// ListByAisle returns every slot in an aisle, ordered bay -> level -> position.
func (r *SlotRepo) ListByAisle(ctx context.Context, aisleID string) ([]*slot.LocationSlot, error) {
	return r.list(ctx, `SELECT `+slotColumns+` FROM location_slots WHERE aisle_id = $1
		ORDER BY bay_segment, level_segment, position_segment`, aisleID)
}

// ListByZone returns every slot in a zone, ordered aisle -> bay -> level -> position.
func (r *SlotRepo) ListByZone(ctx context.Context, zoneID string) ([]*slot.LocationSlot, error) {
	return r.list(ctx, `SELECT `+slotColumns+` FROM location_slots WHERE zone_id = $1
		ORDER BY aisle_segment, bay_segment, level_segment, position_segment`, zoneID)
}

func (r *SlotRepo) list(ctx context.Context, query, arg string) ([]*slot.LocationSlot, error) {
	rows, err := querierFrom(ctx, r.pool).Query(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*slot.LocationSlot, 0)
	for rows.Next() {
		s, err := scanSlot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// scanner is the shared shape of pgx.Row and pgx.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanSlot(row scanner) (*slot.LocationSlot, error) {
	var raw, site, area, zoneSeg, aisleSeg, bay, level, position, locationType, role, status string
	var dockFlow *string
	var activities []string
	var maxWeightKg, maxVolumeM3 *float64
	var xM, yM, zM, widthM, depthM, heightM *float64
	var pickSequence *int

	if err := row.Scan(&raw, &site, &area, &zoneSeg, &aisleSeg, &bay, &level, &position,
		&locationType, &role, &dockFlow, &activities, &maxWeightKg, &maxVolumeM3, &status,
		&xM, &yM, &zM, &widthM, &depthM, &heightM, &pickSequence); err != nil {
		return nil, err
	}

	code, err := shared.NewLocationCode(site, area, zoneSeg, aisleSeg, bay, level, position)
	if err != nil {
		return nil, err
	}
	capacity, err := capacityFromNullable(maxWeightKg, maxVolumeM3)
	if err != nil {
		return nil, err
	}
	st, err := shared.ParseStatus(status)
	if err != nil {
		return nil, err
	}
	locationRole, err := placement.ParseLocationRole(role)
	if err != nil {
		return nil, err
	}
	functional, err := functionalFromColumns(locationRole, dockFlow, activities)
	if err != nil {
		return nil, err
	}
	position3D, dimensions, err := geometryFromColumns(xM, yM, zM, widthM, depthM, heightM)
	if err != nil {
		return nil, err
	}
	return slot.RehydrateLocationSlot(code, locationType, locationRole, functional, capacity, st, position3D, dimensions, pickSequence), nil
}

// nullableDockFlow returns functional's DockFlow as a pointer, or nil when
// it is empty — the shape for every slot whose role is not Dock.
func nullableDockFlow(functional slot.FunctionalAttributes) *string {
	if functional.DockFlow() == "" {
		return nil
	}
	flow := string(functional.DockFlow())
	return &flow
}

// activityColumn returns functional's Activities as a plain string slice
// for the TEXT[] column, or nil when there are none — the shape for every
// slot whose role is not WorkCenter.
func activityColumn(functional slot.FunctionalAttributes) []string {
	activities := functional.Activities()
	if len(activities) == 0 {
		return nil
	}
	out := make([]string, 0, len(activities))
	for _, a := range activities {
		out = append(out, string(a))
	}
	return out
}

// functionalFromColumns rebuilds FunctionalAttributes from the possibly-NULL
// stored dockFlow/activities columns, for the given role.
func functionalFromColumns(role placement.LocationRole, dockFlow *string, activities []string) (slot.FunctionalAttributes, error) {
	var flow slot.DockFlow
	if dockFlow != nil {
		flow = slot.DockFlow(*dockFlow)
	}
	var parsed []slot.Activity
	for _, raw := range activities {
		parsed = append(parsed, slot.Activity(raw))
	}
	return slot.NewFunctionalAttributes(role, flow, parsed)
}

// nullablePositionX/Y/Z return position's respective coordinate as a
// pointer, or nil when no geometry has ever been set (ADR-0017) — the
// all-or-nothing shape the 0003_geometry check constraint enforces.
func nullablePositionX(position shared.Point3D) *float64 {
	if position.IsZero() {
		return nil
	}
	v := position.XM()
	return &v
}

func nullablePositionY(position shared.Point3D) *float64 {
	if position.IsZero() {
		return nil
	}
	v := position.YM()
	return &v
}

func nullablePositionZ(position shared.Point3D) *float64 {
	if position.IsZero() {
		return nil
	}
	v := position.ZM()
	return &v
}

// nullableDimensionWidth/Depth/Height return dimensions's respective extent
// as a pointer, or nil when no geometry has ever been set (ADR-0017).
func nullableDimensionWidth(dimensions shared.Dimensions) *float64 {
	if dimensions.IsZero() {
		return nil
	}
	v := dimensions.WidthM()
	return &v
}

func nullableDimensionDepth(dimensions shared.Dimensions) *float64 {
	if dimensions.IsZero() {
		return nil
	}
	v := dimensions.DepthM()
	return &v
}

func nullableDimensionHeight(dimensions shared.Dimensions) *float64 {
	if dimensions.IsZero() {
		return nil
	}
	v := dimensions.HeightM()
	return &v
}

// geometryFromColumns rebuilds a slot's Point3D/Dimensions from the
// possibly-all-NULL stored columns. The 0003_geometry check constraint
// guarantees the six columns are either all NULL or all set, so seeing xM
// non-nil is sufficient to know the rest are too.
func geometryFromColumns(xM, yM, zM, widthM, depthM, heightM *float64) (shared.Point3D, shared.Dimensions, error) {
	if xM == nil {
		return shared.Point3D{}, shared.Dimensions{}, nil
	}
	position, err := shared.NewPoint3D(*xM, *yM, *zM)
	if err != nil {
		return shared.Point3D{}, shared.Dimensions{}, err
	}
	dimensions, err := shared.NewDimensions(*widthM, *depthM, *heightM)
	if err != nil {
		return shared.Point3D{}, shared.Dimensions{}, err
	}
	return position, dimensions, nil
}
