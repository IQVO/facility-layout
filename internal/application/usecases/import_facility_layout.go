package usecases

import (
	"context"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

// ImportRow is one fully-specified row of a facility layout export: a whole
// Site/Area/Zone/Aisle/Bay/Level/Position address plus the LocationType for
// the slot at it. Structural parents named by a row are created on first
// sight; a row that names an existing parent reuses it (it never mutates
// one, so a row cannot silently redefine a zone's temperature class).
//
// DockFlow and Activities are optional (ADR-0016): they matter only when
// the row's LocationType has role Dock or WorkCenter respectively, and are
// ignored (must be empty) for every other role.
//
// XM/YM/ZM/WidthM/DepthM/HeightM/PickSequence are optional (ADR-0017): a
// row that supplies none of them registers a slot with no geometry, exactly
// as before this ADR. Supplying X/Y/Z requires supplying all three
// dimensions too (all-or-nothing, same rule the domain and schema enforce).
type ImportRow struct {
	SiteCode         string
	SiteName         string
	AreaCode         string
	ZoneCode         string
	TemperatureClass shared.TemperatureClass
	Hazmat           bool
	AisleCode        string
	SequenceHint     int
	Direction        shared.Direction
	Bay              string
	Level            string
	Position         string
	LocationType     string
	DockFlow         string
	Activities       []string
	MaxWeightKg      float64
	MaxVolumeM3      float64
	XM               *float64
	YM               *float64
	ZM               *float64
	WidthM           *float64
	DepthM           *float64
	HeightM          *float64
	PickSequence     *int
}

// ImportRowResult is the per-row outcome of a bulk import.
type ImportRowResult struct {
	Index        int
	LocationCode string
	Succeeded    bool
	Error        string
}

// ImportReport is the full outcome of a bulk import: every row is
// processed, and the report says exactly which rows failed and why. A
// 500-row export with 3 bad rows still creates the other 497.
type ImportReport struct {
	RowsSubmitted int
	SlotsImported int
	RowsRejected  int
	Results       []ImportRowResult
}

// ImportFacilityLayout bulk-registers sites, zones, aisles and slots from a
// structured list in one call. Validation is atomic per row and partial
// success is reported per row, rather than aborting the whole import on the
// first bad row. This is the bootstrap mechanism for loading a real
// building's layout from a CSV/JSON export.
type ImportFacilityLayout struct {
	Sites         ports.SiteRepo
	Zones         ports.ZoneRepo
	Aisles        ports.AisleRepo
	Slots         ports.SlotRepo
	LocationTypes ports.LocationTypeRepo
	Rules         ports.PlacementRuleRepo
	Events        ports.EventPublisher
	Clock         ports.Clock
	// Metrics is passed straight through to the per-row slot registration,
	// so an imported slot is counted exactly like a hand-registered one.
	Metrics ports.LocationMetrics
	// UnitOfWork brackets each row's Save(s) + Publish(es) atomically
	// (ADR-0018, optional), and is passed through to the per-row
	// RegisterLocationSlot/SetLocationGeometry use cases so their own
	// writes join the same scope.
	UnitOfWork ports.UnitOfWork
}

// Execute processes every row and returns the full report. It publishes the
// per-entity registration events as structure is created, plus exactly one
// FacilityLayoutImported carrying the import counts.
func (uc *ImportFacilityLayout) Execute(ctx context.Context, rows []ImportRow) (*ImportReport, error) {
	if len(rows) == 0 {
		return nil, ErrEmptyImport
	}

	report := &ImportReport{RowsSubmitted: len(rows), Results: make([]ImportRowResult, 0, len(rows))}

	for i, row := range rows {
		code, err := uc.importRow(ctx, row)
		result := ImportRowResult{Index: i, LocationCode: code}
		if err != nil {
			result.Error = err.Error()
			report.RowsRejected++
		} else {
			result.Succeeded = true
			report.SlotsImported++
		}
		report.Results = append(report.Results, result)
	}

	event := shared.NewFacilityLayoutImported(uc.Clock.Now(), report.RowsSubmitted, report.SlotsImported, report.RowsRejected)
	if err := atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
		return uc.Events.Publish(ctx, event)
	}); err != nil {
		return nil, err
	}
	return report, nil
}

// importRow validates and applies a single row. It returns the row's
// location code (best-effort, for the report) alongside any failure.
func (uc *ImportFacilityLayout) importRow(ctx context.Context, row ImportRow) (string, error) {
	code, err := shared.NewLocationCode(row.SiteCode, row.AreaCode, row.ZoneCode, row.AisleCode, row.Bay, row.Level, row.Position)
	if err != nil {
		return "", err
	}

	if err := uc.ensureSite(ctx, row); err != nil {
		return code.String(), err
	}
	if err := uc.ensureZone(ctx, row, code.ZoneID()); err != nil {
		return code.String(), err
	}
	if err := uc.ensureAisle(ctx, row, code.ZoneID(), code.AisleID()); err != nil {
		return code.String(), err
	}

	capacityOverride := shared.Capacity{}
	if row.MaxWeightKg != 0 || row.MaxVolumeM3 != 0 {
		capacityOverride, err = shared.NewCapacity(row.MaxWeightKg, row.MaxVolumeM3)
		if err != nil {
			return code.String(), err
		}
	}

	register := &RegisterLocationSlot{
		Sites:         uc.Sites,
		Zones:         uc.Zones,
		Aisles:        uc.Aisles,
		Slots:         uc.Slots,
		LocationTypes: uc.LocationTypes,
		Rules:         uc.Rules,
		Events:        uc.Events,
		Clock:         uc.Clock,
		Metrics:       uc.Metrics,
		UnitOfWork:    uc.UnitOfWork,
	}
	if _, err := register.Execute(ctx, code, row.LocationType, capacityOverride, row.DockFlow, row.Activities); err != nil {
		return code.String(), err
	}

	if row.XM != nil {
		position, err := shared.NewPoint3D(*row.XM, deref(row.YM), deref(row.ZM))
		if err != nil {
			return code.String(), err
		}
		dimensions, err := shared.NewDimensions(deref(row.WidthM), deref(row.DepthM), deref(row.HeightM))
		if err != nil {
			return code.String(), err
		}
		geometry := &SetLocationGeometry{Slots: uc.Slots, Events: uc.Events, Clock: uc.Clock, UnitOfWork: uc.UnitOfWork}
		if _, err := geometry.Execute(ctx, code, position, dimensions, row.PickSequence); err != nil {
			return code.String(), err
		}
	}
	return code.String(), nil
}

// deref returns *p, or the zero value when p is nil — used for the
// geometry columns that must all be present together (ADR-0017's
// all-or-nothing rule), so a nil here would already be a caller error the
// NewPoint3D/NewDimensions validation below catches.
func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func (uc *ImportFacilityLayout) ensureSite(ctx context.Context, row ImportRow) error {
	existing, err := uc.Sites.FindByCode(ctx, row.SiteCode)
	if err != nil {
		return err
	}
	if existing != nil {
		if !existing.IsActive() {
			return ErrSiteNotActive
		}
		return nil
	}

	name := row.SiteName
	if name == "" {
		name = row.SiteCode
	}
	s, err := site.NewSite(row.SiteCode, name)
	if err != nil {
		return err
	}
	return atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
		if err := uc.Sites.Save(ctx, s); err != nil {
			return err
		}
		return uc.Events.Publish(ctx, shared.NewSiteRegistered(uc.Clock.Now(), s.Code(), s.Name()))
	})
}

func (uc *ImportFacilityLayout) ensureZone(ctx context.Context, row ImportRow, zoneID string) error {
	existing, err := uc.Zones.FindByID(ctx, zoneID)
	if err != nil {
		return err
	}
	if existing != nil {
		if !existing.IsActive() {
			return ErrZoneNotActive
		}
		return nil
	}

	z, err := zone.NewZone(row.SiteCode, row.AreaCode, row.ZoneCode, row.TemperatureClass, row.Hazmat)
	if err != nil {
		return err
	}
	return atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
		if err := uc.Zones.Save(ctx, z); err != nil {
			return err
		}
		event := shared.NewZoneRegistered(uc.Clock.Now(), z.ID(), z.SiteCode(), z.AreaCode(), z.ZoneCode(), z.TemperatureClass(), z.Hazmat())
		return uc.Events.Publish(ctx, event)
	})
}

func (uc *ImportFacilityLayout) ensureAisle(ctx context.Context, row ImportRow, zoneID, aisleID string) error {
	existing, err := uc.Aisles.FindByID(ctx, aisleID)
	if err != nil {
		return err
	}
	if existing != nil {
		if !existing.IsActive() {
			return ErrAisleNotActive
		}
		return nil
	}

	direction := row.Direction
	if direction == "" {
		direction = shared.TwoWay
	}
	a, err := aisle.NewAisle(zoneID, row.AisleCode, row.SequenceHint, direction)
	if err != nil {
		return err
	}
	return atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
		if err := uc.Aisles.Save(ctx, a); err != nil {
			return err
		}
		event := shared.NewAisleRegistered(uc.Clock.Now(), a.ID(), a.ZoneID(), a.AisleCode(), a.SequenceHint(), a.Direction())
		return uc.Events.Publish(ctx, event)
	})
}
