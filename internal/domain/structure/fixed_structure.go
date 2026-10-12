// Package structure holds the FixedStructure aggregate: a site-scoped
// physical obstacle (a wall, column, office, conveyor, or other fixed
// object) drawn on the warehouse map. It is not a location stock can
// occupy — it exists purely so a floor plan can be rendered to scale and
// so the travel graph can, in a later phase, treat it as an obstacle.
package structure

import (
	"errors"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

var (
	// ErrEmptySiteCode is returned when a structure is not scoped to a site.
	ErrEmptySiteCode = errors.New("fixed structure must be scoped to a site code")
	// ErrEmptyID is returned when a structure is built without an id.
	ErrEmptyID = errors.New("fixed structure requires an id")
	// ErrEmptyFootprint is returned when a structure is built without a
	// real (non-zero) footprint.
	ErrEmptyFootprint = errors.New("fixed structure requires a real footprint")
	// ErrEmptyLabel is returned when a structure is built without a label.
	ErrEmptyLabel = errors.New("fixed structure requires a label")
	// ErrEmptyKind is returned when a structure is built without a kind: the
	// member is required, so its absence is a malformed request rather than an
	// unrecognised value.
	ErrEmptyKind = errors.New("fixed structure requires a kind")
	// ErrUnknownKind is returned when Kind does not name one of the known
	// structure kinds.
	ErrUnknownKind = errors.New("unknown fixed structure kind")
)

// Kind is what a FixedStructure represents.
type Kind string

const (
	// Wall is a solid barrier: no aisle or slot may overlap it.
	Wall Kind = "Wall"
	// Column is a structural support post.
	Column Kind = "Column"
	// Office is an enclosed room (e.g. a mezzanine office).
	Office Kind = "Office"
	// Conveyor is a fixed conveyor line.
	Conveyor Kind = "Conveyor"
	// Other is any fixed obstacle not covered by the above.
	Other Kind = "Other"
)

// ParseKind validates a raw string against the known Kind values.
func ParseKind(raw string) (Kind, error) {
	if raw == "" {
		return "", ErrEmptyKind
	}
	switch Kind(raw) {
	case Wall, Column, Office, Conveyor, Other:
		return Kind(raw), nil
	default:
		return "", ErrUnknownKind
	}
}

// FixedStructure is a site-scoped physical obstacle drawn on the warehouse
// map: its identity is an opaque id (minted by the use case, not derived
// from any coded location — a wall has no LocationCode).
type FixedStructure struct {
	id        string
	siteCode  string
	kind      Kind
	footprint shared.Rect
	label     string
}

// NewFixedStructure validates and constructs a FixedStructure. id is
// minted by the use case (a UUID, the same pattern PlacementRule ids
// follow) before this constructor runs — it is never derived here.
func NewFixedStructure(id, siteCode string, kind Kind, footprint shared.Rect, label string) (*FixedStructure, error) {
	if id == "" {
		return nil, ErrEmptyID
	}
	if siteCode == "" {
		return nil, ErrEmptySiteCode
	}
	if _, err := ParseKind(string(kind)); err != nil {
		return nil, err
	}
	if footprint.IsZero() {
		return nil, ErrEmptyFootprint
	}
	if label == "" {
		return nil, ErrEmptyLabel
	}
	return &FixedStructure{id: id, siteCode: siteCode, kind: kind, footprint: footprint, label: label}, nil
}

// RehydrateFixedStructure rebuilds a FixedStructure from persisted state.
// Persistence adapters only.
func RehydrateFixedStructure(id, siteCode string, kind Kind, footprint shared.Rect, label string) *FixedStructure {
	return &FixedStructure{id: id, siteCode: siteCode, kind: kind, footprint: footprint, label: label}
}

// ID returns the structure's opaque identity.
func (f *FixedStructure) ID() string { return f.id }

// SiteCode returns the code of the Site this structure is scoped to.
func (f *FixedStructure) SiteCode() string { return f.siteCode }

// Kind returns what kind of obstacle this structure represents.
func (f *FixedStructure) Kind() Kind { return f.kind }

// Footprint returns the structure's rectangular position and extent.
func (f *FixedStructure) Footprint() shared.Rect { return f.footprint }

// Label returns the structure's human-readable label, e.g. "North wall"
// or "Mezzanine office".
func (f *FixedStructure) Label() string { return f.label }
