package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
	"github.com/claudioed/facility-layout/internal/domain/slot"
	"github.com/claudioed/facility-layout/internal/domain/structure"
	"github.com/claudioed/facility-layout/internal/domain/travel"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

// statusFor maps a typed domain/application error to an HTTP status code.
//
//   - 404: the named site/zone/aisle/slot/type/rule does not exist.
//   - 409: a genuine state conflict — a code already taken, a parent that
//     exists but is no longer Active, a slot already decommissioned.
//   - 422: syntactically fine but semantically invalid — a non-positive
//     capacity, an unknown enum value, a PlacementRule violation.
//   - 400: malformed input — a location code that is not seven [A-Z0-9]
//     segments, a missing required field, a body that is not JSON.
func statusFor(err error) int {
	switch {
	case errors.Is(err, usecases.ErrSiteNotFound),
		errors.Is(err, usecases.ErrZoneNotFound),
		errors.Is(err, usecases.ErrAisleNotFound),
		errors.Is(err, usecases.ErrLocationSlotNotFound),
		errors.Is(err, usecases.ErrLocationTypeNotFound),
		errors.Is(err, usecases.ErrPlacementRuleNotFound):
		return http.StatusNotFound

	case errors.Is(err, usecases.ErrDuplicateSite),
		errors.Is(err, usecases.ErrDuplicateZone),
		errors.Is(err, usecases.ErrDuplicateAisle),
		errors.Is(err, usecases.ErrDuplicateLocationType),
		errors.Is(err, usecases.ErrDuplicatePlacementRule),
		errors.Is(err, usecases.ErrDuplicateLocationCode),
		errors.Is(err, usecases.ErrDuplicateFixedStructure),
		errors.Is(err, usecases.ErrDuplicateCrossAisle),
		errors.Is(err, usecases.ErrSiteNotActive),
		errors.Is(err, usecases.ErrZoneNotActive),
		errors.Is(err, usecases.ErrAisleNotActive),
		errors.Is(err, site.ErrAlreadyDecommissioned),
		errors.Is(err, zone.ErrAlreadyDecommissioned),
		errors.Is(err, aisle.ErrAlreadyDecommissioned),
		errors.Is(err, aisle.ErrAisleDecommissioned),
		errors.Is(err, aisle.ErrCrossAisleAlreadyDecommissioned),
		errors.Is(err, slot.ErrAlreadyDecommissioned),
		errors.Is(err, slot.ErrSlotDecommissioned):
		return http.StatusConflict

	case errors.Is(err, placement.ErrPlacementRuleViolated),
		errors.Is(err, shared.ErrInvalidMaxWeight),
		errors.Is(err, shared.ErrInvalidMaxVolume),
		errors.Is(err, shared.ErrUnknownTemperatureClass),
		errors.Is(err, shared.ErrUnknownDirection),
		errors.Is(err, shared.ErrUnknownStatus),
		errors.Is(err, shared.ErrInvalidZ),
		errors.Is(err, shared.ErrInvalidDimensions),
		errors.Is(err, shared.ErrSegmentEndpointsNotReal),
		errors.Is(err, shared.ErrSegmentStartEndEqual),
		errors.Is(err, placement.ErrUnknownEffect),
		errors.Is(err, placement.ErrEmptyPredicate),
		errors.Is(err, placement.ErrUnknownLocationRole),
		errors.Is(err, slot.ErrUnknownDockFlow),
		errors.Is(err, slot.ErrUnknownActivity),
		errors.Is(err, slot.ErrDockFlowRequired),
		errors.Is(err, slot.ErrWorkCenterActivitiesRequired),
		errors.Is(err, slot.ErrFunctionalAttributesNotAllowed),
		errors.Is(err, slot.ErrNegativePickSequence),
		errors.Is(err, aisle.ErrNegativeSequenceHint),
		errors.Is(err, slot.ErrZoneMismatch),
		errors.Is(err, structure.ErrUnknownKind),
		errors.Is(err, structure.ErrEmptyFootprint),
		errors.Is(err, aisle.ErrCrossAisleSameAisle),
		errors.Is(err, usecases.ErrCrossAisleAisleMismatch),
		errors.Is(err, usecases.ErrNoRouteBetweenZones),
		errors.Is(err, travel.ErrNoRoute),
		errors.Is(err, travel.ErrUnknownNode),
		errors.Is(err, zone.ErrInvalidPitch):
		return http.StatusUnprocessableEntity

	case errors.Is(err, shared.ErrMalformedLocationCode),
		errors.Is(err, shared.ErrEmptyLocationSegment),
		errors.Is(err, shared.ErrInvalidLocationSegment),
		errors.Is(err, site.ErrEmptySiteCode),
		errors.Is(err, site.ErrInvalidSiteCode),
		errors.Is(err, site.ErrEmptySiteName),
		errors.Is(err, zone.ErrEmptySiteCode),
		errors.Is(err, zone.ErrEmptyAreaCode),
		errors.Is(err, zone.ErrEmptyZoneCode),
		errors.Is(err, zone.ErrInvalidCode),
		errors.Is(err, aisle.ErrEmptyZoneID),
		errors.Is(err, aisle.ErrEmptyAisleCode),
		errors.Is(err, aisle.ErrInvalidAisleCode),
		errors.Is(err, placement.ErrEmptyLocationTypeName),
		errors.Is(err, placement.ErrEmptyRuleID),
		errors.Is(err, placement.ErrEmptyRuleLocationType),
		errors.Is(err, slot.ErrMissingLocationCode),
		errors.Is(err, slot.ErrMissingLocationType),
		errors.Is(err, structure.ErrEmptyID),
		errors.Is(err, structure.ErrEmptySiteCode),
		errors.Is(err, structure.ErrEmptyLabel),
		errors.Is(err, aisle.ErrCrossAisleEmptyZoneID),
		errors.Is(err, aisle.ErrCrossAisleEmptyFromAisle),
		errors.Is(err, aisle.ErrCrossAisleEmptyToAisle),
		errors.Is(err, aisle.ErrCrossAisleEmptyBay),
		errors.Is(err, usecases.ErrEmptyImport),
		errors.Is(err, errMissingGeometryField),
		errors.Is(err, errMissingSequenceHint):
		return http.StatusBadRequest

	default:
		return http.StatusInternalServerError
	}
}

// problemBaseURI is the namespace for this service's RFC 7807 "type" URIs.
// It does not need to resolve to a real page — it's an identifier, unique
// per distinct error category in this service.
const problemBaseURI = "https://errors.facility-layout.warehouse-systems.dev/"

// problemInfo is the fixed, category-level (type, title) pair for an RFC
// 7807 problem response. slug becomes the last path segment of "type";
// title is a fixed human string for the category (the dynamic detail comes
// from err.Error() at write time, not from this table).
type problemInfo struct {
	slug  string
	title string
}

// problemFor maps a typed domain/application error to its RFC 7807
// (type, title) pair, mirroring statusFor's groupings one-for-one.
func problemFor(err error) problemInfo {
	switch {
	case errors.Is(err, usecases.ErrSiteNotFound):
		return problemInfo{"site-not-found", "Site not found"}
	case errors.Is(err, usecases.ErrZoneNotFound):
		return problemInfo{"zone-not-found", "Zone not found"}
	case errors.Is(err, usecases.ErrAisleNotFound):
		return problemInfo{"aisle-not-found", "Aisle not found"}
	case errors.Is(err, usecases.ErrLocationSlotNotFound):
		return problemInfo{"location-slot-not-found", "Location slot not found"}
	case errors.Is(err, usecases.ErrLocationTypeNotFound):
		return problemInfo{"location-type-not-found", "Location type not found"}
	case errors.Is(err, usecases.ErrPlacementRuleNotFound):
		return problemInfo{"placement-rule-not-found", "Placement rule not found"}

	case errors.Is(err, usecases.ErrDuplicateSite):
		return problemInfo{"duplicate-site-code", "A site with this code already exists"}
	case errors.Is(err, usecases.ErrDuplicateZone):
		return problemInfo{"duplicate-zone", "A zone with this area and zone code already exists in this site"}
	case errors.Is(err, usecases.ErrDuplicateAisle):
		return problemInfo{"duplicate-aisle", "An aisle with this code already exists in this zone"}
	case errors.Is(err, usecases.ErrDuplicateLocationType):
		return problemInfo{"duplicate-location-type", "A location type with this name already exists"}
	case errors.Is(err, usecases.ErrDuplicatePlacementRule):
		return problemInfo{"duplicate-placement-rule", "A placement rule with this id already exists"}
	case errors.Is(err, usecases.ErrDuplicateLocationCode):
		return problemInfo{"duplicate-location-code", "A location slot with this code already exists"}
	case errors.Is(err, usecases.ErrDuplicateFixedStructure):
		return problemInfo{"duplicate-fixed-structure", "A fixed structure with this id already exists"}
	case errors.Is(err, usecases.ErrDuplicateCrossAisle):
		return problemInfo{"duplicate-cross-aisle", "A cross-aisle between these aisles at this bay already exists"}

	case errors.Is(err, usecases.ErrSiteNotActive):
		return problemInfo{"site-not-active", "Site is not active"}
	case errors.Is(err, usecases.ErrZoneNotActive):
		return problemInfo{"zone-not-active", "Zone is not active"}
	case errors.Is(err, usecases.ErrAisleNotActive):
		return problemInfo{"aisle-not-active", "Aisle is not active"}

	case errors.Is(err, aisle.ErrAlreadyDecommissioned),
		errors.Is(err, aisle.ErrAisleDecommissioned),
		errors.Is(err, aisle.ErrCrossAisleAlreadyDecommissioned),
		errors.Is(err, site.ErrAlreadyDecommissioned),
		errors.Is(err, zone.ErrAlreadyDecommissioned),
		errors.Is(err, slot.ErrAlreadyDecommissioned),
		errors.Is(err, slot.ErrSlotDecommissioned):
		return problemInfo{"already-decommissioned", "This structure is already decommissioned"}

	case errors.Is(err, placement.ErrPlacementRuleViolated):
		return problemInfo{"placement-rule-violated", "Location type is not legal in this zone"}
	case errors.Is(err, shared.ErrInvalidMaxWeight):
		return problemInfo{"invalid-max-weight", "Capacity max weight must be greater than zero"}
	case errors.Is(err, shared.ErrInvalidMaxVolume):
		return problemInfo{"invalid-max-volume", "Capacity max volume must be greater than zero"}
	case errors.Is(err, shared.ErrUnknownTemperatureClass):
		return problemInfo{"unknown-temperature-class", "Unknown temperature class"}
	case errors.Is(err, shared.ErrUnknownDirection):
		return problemInfo{"unknown-direction", "Unknown aisle direction"}
	case errors.Is(err, shared.ErrUnknownStatus):
		return problemInfo{"unknown-status", "Unknown lifecycle status"}
	case errors.Is(err, placement.ErrUnknownEffect):
		return problemInfo{"unknown-placement-effect", "Unknown placement rule effect"}
	case errors.Is(err, placement.ErrEmptyPredicate):
		return problemInfo{"empty-zone-predicate", "Placement rule predicate constrains nothing"}
	case errors.Is(err, placement.ErrUnknownLocationRole):
		return problemInfo{"unknown-location-role", "Unknown location role"}
	case errors.Is(err, slot.ErrUnknownDockFlow):
		return problemInfo{"unknown-dock-flow", "Unknown dock flow"}
	case errors.Is(err, slot.ErrUnknownActivity):
		return problemInfo{"unknown-activity", "Unknown work center activity"}
	case errors.Is(err, slot.ErrDockFlowRequired):
		return problemInfo{"dock-flow-required", "A Dock location requires a dockFlow"}
	case errors.Is(err, slot.ErrWorkCenterActivitiesRequired):
		return problemInfo{"work-center-activities-required", "A WorkCenter location requires at least one activity"}
	case errors.Is(err, slot.ErrFunctionalAttributesNotAllowed):
		return problemInfo{"functional-attributes-not-allowed", "dockFlow and activities may only be set on a Dock or WorkCenter location"}
	case errors.Is(err, aisle.ErrNegativeSequenceHint):
		return problemInfo{"negative-sequence-hint", "Aisle sequence hint must not be negative"}
	case errors.Is(err, slot.ErrZoneMismatch):
		return problemInfo{"zone-mismatch", "Zone attributes do not match the location code's zone"}
	case errors.Is(err, shared.ErrInvalidZ):
		return problemInfo{"invalid-z", "Z coordinate must not be negative"}
	case errors.Is(err, shared.ErrInvalidDimensions):
		return problemInfo{"invalid-dimensions", "Width, depth, and height must all be greater than zero"}
	case errors.Is(err, shared.ErrSegmentEndpointsNotReal):
		return problemInfo{"segment-endpoints-not-real", "Centreline endpoints must both be real points"}
	case errors.Is(err, shared.ErrSegmentStartEndEqual):
		return problemInfo{"segment-start-end-equal", "Centreline start and end must differ"}
	case errors.Is(err, slot.ErrNegativePickSequence):
		return problemInfo{"negative-pick-sequence", "Pick sequence must not be negative"}
	case errors.Is(err, structure.ErrUnknownKind):
		return problemInfo{"unknown-fixed-structure-kind", "Unknown fixed structure kind"}
	case errors.Is(err, structure.ErrEmptyFootprint):
		return problemInfo{"empty-fixed-structure-footprint", "Fixed structure requires a real footprint"}
	case errors.Is(err, aisle.ErrCrossAisleSameAisle):
		return problemInfo{"cross-aisle-same-aisle", "Cross-aisle must connect two distinct aisles"}
	case errors.Is(err, usecases.ErrCrossAisleAisleMismatch):
		return problemInfo{"cross-aisle-aisle-mismatch", "Cross-aisle aisles must both belong to the named zone"}
	case errors.Is(err, usecases.ErrNoRouteBetweenZones):
		return problemInfo{"no-route-between-zones", "No route: the two locations are in different zones"}
	case errors.Is(err, travel.ErrNoRoute):
		return problemInfo{"no-route", "No route exists between these two waypoints"}
	case errors.Is(err, travel.ErrUnknownNode):
		return problemInfo{"unknown-travel-waypoint", "The travel graph has no waypoint for this aisle/bay"}
	case errors.Is(err, zone.ErrInvalidPitch):
		return problemInfo{"invalid-pitch", "Bay pitch and level pitch must both be greater than zero"}

	case errors.Is(err, shared.ErrMalformedLocationCode),
		errors.Is(err, shared.ErrEmptyLocationSegment),
		errors.Is(err, shared.ErrInvalidLocationSegment):
		return problemInfo{"malformed-location-code", "Malformed location code"}
	case errors.Is(err, site.ErrEmptySiteCode), errors.Is(err, site.ErrInvalidSiteCode), errors.Is(err, zone.ErrEmptySiteCode):
		return problemInfo{"invalid-site-code", "Invalid site code"}
	case errors.Is(err, site.ErrEmptySiteName):
		return problemInfo{"empty-site-name", "Site name must not be empty"}
	case errors.Is(err, zone.ErrEmptyAreaCode), errors.Is(err, zone.ErrEmptyZoneCode), errors.Is(err, zone.ErrInvalidCode):
		return problemInfo{"invalid-zone-code", "Invalid area or zone code"}
	case errors.Is(err, aisle.ErrEmptyZoneID), errors.Is(err, aisle.ErrEmptyAisleCode), errors.Is(err, aisle.ErrInvalidAisleCode):
		return problemInfo{"invalid-aisle-code", "Invalid aisle code"}
	case errors.Is(err, placement.ErrEmptyLocationTypeName), errors.Is(err, slot.ErrMissingLocationType), errors.Is(err, placement.ErrEmptyRuleLocationType):
		return problemInfo{"invalid-location-type", "Invalid location type"}
	case errors.Is(err, placement.ErrEmptyRuleID):
		return problemInfo{"empty-placement-rule-id", "Placement rule id must not be empty"}
	case errors.Is(err, slot.ErrMissingLocationCode):
		return problemInfo{"missing-location-code", "Location code is required"}
	case errors.Is(err, structure.ErrEmptyID):
		return problemInfo{"empty-fixed-structure-id", "Fixed structure requires an id"}
	case errors.Is(err, structure.ErrEmptySiteCode):
		return problemInfo{"empty-fixed-structure-site-code", "Fixed structure must be scoped to a site code"}
	case errors.Is(err, structure.ErrEmptyLabel):
		return problemInfo{"empty-fixed-structure-label", "Fixed structure requires a label"}
	case errors.Is(err, aisle.ErrCrossAisleEmptyZoneID):
		return problemInfo{"empty-cross-aisle-zone-id", "Cross-aisle must be scoped to a zone id"}
	case errors.Is(err, aisle.ErrCrossAisleEmptyFromAisle):
		return problemInfo{"empty-cross-aisle-from-aisle", "Cross-aisle requires a from-aisle code"}
	case errors.Is(err, aisle.ErrCrossAisleEmptyToAisle):
		return problemInfo{"empty-cross-aisle-to-aisle", "Cross-aisle requires a to-aisle code"}
	case errors.Is(err, aisle.ErrCrossAisleEmptyBay):
		return problemInfo{"empty-cross-aisle-bay", "Cross-aisle requires a bay"}
	case errors.Is(err, usecases.ErrEmptyImport):
		return problemInfo{"empty-import", "Facility layout import must contain at least one row"}
	case errors.Is(err, errMissingGeometryField):
		return problemInfo{"missing-geometry-field", "Geometry coordinates and dimensions must be numbers, not null"}
	case errors.Is(err, errMissingSequenceHint):
		return problemInfo{"missing-sequence-hint", "Aisle sequenceHint is required"}

	default:
		return problemInfo{"internal-error", "An unexpected internal error occurred"}
	}
}
