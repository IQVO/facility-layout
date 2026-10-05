// Package usecases implements the application's use cases: one struct per
// use case, depending only on the domain and on application/ports. The
// orchestration this bounded context actually owns lives here — resolving
// the Site -> Zone -> Aisle chain of custody before a LocationSlot is
// allowed to exist, and assembling the drawable read models.
package usecases

import "errors"

var (
	// ErrSiteNotFound is returned when a referenced site does not exist.
	ErrSiteNotFound = errors.New("site not found")
	// ErrZoneNotFound is returned when a referenced zone does not exist.
	ErrZoneNotFound = errors.New("zone not found")
	// ErrAisleNotFound is returned when a referenced aisle does not exist.
	ErrAisleNotFound = errors.New("aisle not found")
	// ErrLocationSlotNotFound is returned when a referenced slot does not exist.
	ErrLocationSlotNotFound = errors.New("location slot not found")
	// ErrLocationTypeNotFound is returned when a referenced location type
	// does not exist.
	ErrLocationTypeNotFound = errors.New("location type not found")
	// ErrPlacementRuleNotFound is returned when a referenced placement rule
	// does not exist.
	ErrPlacementRuleNotFound = errors.New("placement rule not found")

	// ErrSiteNotActive is returned when structure is registered against a
	// site that is not Active.
	ErrSiteNotActive = errors.New("site is not active")
	// ErrZoneNotActive is returned when structure is registered against a
	// zone that is not Active.
	ErrZoneNotActive = errors.New("zone is not active")
	// ErrAisleNotActive is returned when a slot is registered against an
	// aisle that is not Active.
	ErrAisleNotActive = errors.New("aisle is not active")

	// ErrDuplicateSite is returned when a site code is already taken.
	ErrDuplicateSite = errors.New("a site with this code already exists")
	// ErrDuplicateZone is returned when a zone id is already taken.
	ErrDuplicateZone = errors.New("a zone with this area and zone code already exists in this site")
	// ErrDuplicateAisle is returned when an aisle id is already taken.
	ErrDuplicateAisle = errors.New("an aisle with this code already exists in this zone")
	// ErrDuplicateLocationType is returned when a location type name is
	// already taken.
	ErrDuplicateLocationType = errors.New("a location type with this name already exists")
	// ErrDuplicatePlacementRule is returned when a rule id is already taken.
	ErrDuplicatePlacementRule = errors.New("a placement rule with this id already exists")
	// ErrDuplicateFixedStructure is returned when a fixed structure id is
	// already taken.
	ErrDuplicateFixedStructure = errors.New("a fixed structure with this id already exists")
	// ErrDuplicateLocationCode is returned when a location code is already
	// registered — including when the existing slot is Decommissioned,
	// because decommission is one-way and a retired code is never
	// resurrected by re-registration.
	ErrDuplicateLocationCode = errors.New("a location slot with this code already exists")

	// ErrEmptyImport is returned when a bulk import carries no rows.
	ErrEmptyImport = errors.New("facility layout import must contain at least one row")

	// ErrCrossAisleAisleMismatch is returned when a cross-aisle names an
	// aisle that does not belong to the zone it is being registered
	// against.
	ErrCrossAisleAisleMismatch = errors.New("cross-aisle aisles must both belong to the named zone")
	// ErrDuplicateCrossAisle is returned when a cross-aisle connecting
	// the same two aisles at the same bay already exists in the zone.
	ErrDuplicateCrossAisle = errors.New("a cross-aisle between these aisles at this bay already exists")
	// ErrNoRouteBetweenZones is returned when a distance is requested
	// between two location codes in different zones and the estimate the
	// ADR allows — real position geometry on BOTH endpoints — is not
	// available: the travel graph is per-zone and cross-aisles are
	// zone-scoped, so without geometry there is nothing honest to report.
	ErrNoRouteBetweenZones = errors.New("no route: the two locations are in different zones and at least one has no recorded position geometry")
)
