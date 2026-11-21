// Package zone holds the Zone aggregate: a behavioral classification scoped
// to a Site. It bundles the Area and Zone segments of a LocationCode into
// one aggregate, because a bare Area carries no rules of its own — every
// PlacementRule is keyed by the behavioral Zone, and a Zone is only
// meaningful inside its Area.
package zone

import (
	"errors"
	"fmt"
	"strings"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

var (
	// ErrEmptySiteCode is returned when a zone is not scoped to a site.
	ErrEmptySiteCode = errors.New("zone must be scoped to a site code")
	// ErrEmptyAreaCode is returned when the area code is empty.
	ErrEmptyAreaCode = errors.New("area code must not be empty")
	// ErrEmptyZoneCode is returned when the zone code is empty.
	ErrEmptyZoneCode = errors.New("zone code must not be empty")
	// ErrInvalidCode is returned when a site/area/zone code contains a
	// character outside [A-Z0-9].
	ErrInvalidCode = errors.New("zone codes must contain only uppercase letters and digits")
	// ErrAlreadyDecommissioned is returned when decommissioning a zone that
	// is already decommissioned.
	ErrAlreadyDecommissioned = errors.New("zone is already decommissioned")
	// ErrInvalidPitch is returned when a bayPitchM/levelPitchM override is
	// not strictly positive.
	ErrInvalidPitch = errors.New("bay pitch and level pitch must both be greater than zero")
)

// DefaultBayPitchM is the fallback distance, in metres, between adjacent
// bays on an aisle when a zone has not set its own bayPitchM (ADR-0017).
// Used only by the travel graph's estimated (no-geometry) fallback.
const DefaultBayPitchM = 1.2

// DefaultLevelPitchM is the fallback vertical distance, in metres, between
// adjacent levels when a zone has not set its own levelPitchM (ADR-0017).
// Used only by the travel graph's estimated (no-geometry) fallback.
const DefaultLevelPitchM = 1.5

// Zone is a behavioral classification within a Site's area: ambient,
// chilled, frozen, hazmat, forward-pick, reserve. It is not cosmetic — its
// TemperatureClass and Hazmat flag are what PlacementRules match on, and
// its identity is the Site/Area/Zone prefix of every LocationCode inside it.
// Since ADR-0017 it may also carry optional bayPitchM/levelPitchM overrides
// consumed only by the travel graph's estimated-distance fallback when
// aisle/slot geometry is missing; DefaultBayPitchM/DefaultLevelPitchM apply
// when unset.
type Zone struct {
	siteCode         string
	areaCode         string
	zoneCode         string
	temperatureClass shared.TemperatureClass
	hazmat           bool
	status           shared.Status
	bayPitchM        float64
	levelPitchM      float64
	// gln is the zone's OPTIONAL GS1 Global Location Number (ADR-0045);
	// "" means none recorded.
	gln shared.GLN
}

// NewZone validates and constructs an Active Zone scoped to siteCode.
func NewZone(siteCode, areaCode, zoneCode string, temperatureClass shared.TemperatureClass, hazmat bool) (*Zone, error) {
	if siteCode == "" {
		return nil, ErrEmptySiteCode
	}
	if areaCode == "" {
		return nil, ErrEmptyAreaCode
	}
	if zoneCode == "" {
		return nil, ErrEmptyZoneCode
	}
	for _, code := range []string{siteCode, areaCode, zoneCode} {
		if err := validateCode(code); err != nil {
			return nil, err
		}
	}
	if _, err := shared.ParseTemperatureClass(string(temperatureClass)); err != nil {
		return nil, err
	}
	return &Zone{
		siteCode:         siteCode,
		areaCode:         areaCode,
		zoneCode:         zoneCode,
		temperatureClass: temperatureClass,
		hazmat:           hazmat,
		status:           shared.Active,
	}, nil
}

// RehydrateZone rebuilds a Zone from persisted state. bayPitchM/levelPitchM
// are 0 when never set (ADR-0017) — use BayPitchM()/LevelPitchM() to read
// the effective value including the default. Persistence adapters only.
func RehydrateZone(siteCode, areaCode, zoneCode string, temperatureClass shared.TemperatureClass, hazmat bool, status shared.Status, bayPitchM, levelPitchM float64) *Zone {
	return &Zone{
		siteCode:         siteCode,
		areaCode:         areaCode,
		zoneCode:         zoneCode,
		temperatureClass: temperatureClass,
		hazmat:           hazmat,
		status:           status,
		bayPitchM:        bayPitchM,
		levelPitchM:      levelPitchM,
	}
}

func validateCode(code string) error {
	for _, r := range code {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		return fmt.Errorf("%w: %q", ErrInvalidCode, code)
	}
	return nil
}

// GLN returns the zone's GS1 Global Location Number, or "" when none was
// recorded (ADR-0045).
func (z *Zone) GLN() shared.GLN { return z.gln }

// AssignGLN records the zone's GS1 Global Location Number, validating the
// 13-digit GS1 mod-10 rule (shared.ErrInvalidGLN). Whether another location
// already holds the number is the use case's question, not the aggregate's.
func (z *Zone) AssignGLN(gln shared.GLN) error {
	if _, err := shared.ParseGLN(string(gln)); err != nil {
		return err
	}
	z.gln = gln
	return nil
}

// WithPersistedGLN restores the GLN read from storage onto a rehydrated zone
// without re-validating it. "" means none recorded. Persistence adapters only.
func (z *Zone) WithPersistedGLN(gln shared.GLN) *Zone {
	z.gln = gln
	return z
}

// ID returns the zone's identity: SITE-AREA-ZONE, e.g. "WH1-STOR-AMB". It
// is exactly the first three segments of every LocationCode inside it.
func (z *Zone) ID() string {
	return strings.Join([]string{z.siteCode, z.areaCode, z.zoneCode}, "-")
}

// SiteCode returns the code of the Site this zone is scoped to.
func (z *Zone) SiteCode() string { return z.siteCode }

// AreaCode returns the coarse functional area segment, e.g. "STOR".
func (z *Zone) AreaCode() string { return z.areaCode }

// ZoneCode returns the behavioral zone segment, e.g. "AMB".
func (z *Zone) ZoneCode() string { return z.zoneCode }

// TemperatureClass returns the zone's thermal class.
func (z *Zone) TemperatureClass() shared.TemperatureClass { return z.temperatureClass }

// Hazmat reports whether the zone is rated for hazardous materials.
func (z *Zone) Hazmat() bool { return z.hazmat }

// Status returns the zone's lifecycle status.
func (z *Zone) Status() shared.Status { return z.status }

// IsActive reports whether new structure may be registered against this zone.
func (z *Zone) IsActive() bool { return z.status == shared.Active }

// Decommission permanently retires the zone. One-way.
func (z *Zone) Decommission() error {
	if z.status == shared.Decommissioned {
		return ErrAlreadyDecommissioned
	}
	z.status = shared.Decommissioned
	return nil
}

// BayPitchM returns the distance, in metres, between adjacent bays on an
// aisle in this zone, for use by the travel graph's estimated-distance
// fallback (ADR-0017). Returns DefaultBayPitchM when the zone has not set
// its own value.
func (z *Zone) BayPitchM() float64 {
	if z.bayPitchM <= 0 {
		return DefaultBayPitchM
	}
	return z.bayPitchM
}

// LevelPitchM returns the vertical distance, in metres, between adjacent
// levels in this zone, for use by the travel graph's estimated-distance
// fallback (ADR-0017). Returns DefaultLevelPitchM when the zone has not
// set its own value.
func (z *Zone) LevelPitchM() float64 {
	if z.levelPitchM <= 0 {
		return DefaultLevelPitchM
	}
	return z.levelPitchM
}

// SetPitch records explicit bayPitchM/levelPitchM overrides for this
// zone's travel-graph fallback (ADR-0017). Both must be strictly positive.
func (z *Zone) SetPitch(bayPitchM, levelPitchM float64) error {
	if bayPitchM <= 0 || levelPitchM <= 0 {
		return ErrInvalidPitch
	}
	z.bayPitchM = bayPitchM
	z.levelPitchM = levelPitchM
	return nil
}
