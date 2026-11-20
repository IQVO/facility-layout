package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/facility-layout/internal/application/ports"
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

// errorCategory ties one typed sentinel error to both facets of its RFC 7807
// mapping: the HTTP status code and the fixed (type, title) pair. The table
// below is the single source of this adapter's error contract; it is scanned
// in declaration order with errors.Is, so a wrapped error still maps through
// its sentinel exactly as the per-error switch arms did before.
// Problem titles reused by several catalog rows below; the slug+title pair
// is fixed per problem type, so several sentinels share one title.
const (
	msgAlreadyDecommissioned = "This structure is already decommissioned"
	msgMalformedLocationCode = "Malformed location code"
	msgInvalidSiteCode       = "Invalid site code"
	msgInvalidZoneCode       = "Invalid area or zone code"
	msgInvalidAisleCode      = "Invalid aisle code"
	msgInvalidLocationType   = "Invalid location type"
)

type errorCategory struct {
	err     error
	status  int
	problem problemInfo
}

// errorCategories enumerates every typed domain/application error this
// adapter can surface, grouped by status code:
//
//   - 404: the named site/zone/aisle/slot/type/rule does not exist.
//   - 409: a genuine state conflict — a code already taken, a parent that
//     exists but is no longer Active, a slot already decommissioned.
//   - 422: syntactically fine but semantically invalid — a non-positive
//     capacity, an unknown enum value, a PlacementRule violation.
//   - 400: malformed input — a location code that is not seven [A-Z0-9]
//     segments, a missing required field, a body that is not JSON.
//
// Sentinels that share a status share its problem "type" slug when the RFC
// 7807 contract treats them as one category (e.g. every
// already-decommissioned sentinel maps to "already-decommissioned"), and
// keep distinct slugs otherwise.
var errorCategories = []errorCategory{
	// 404 Not Found: the named resource does not exist.
	{usecases.ErrSiteNotFound, http.StatusNotFound, problemInfo{"site-not-found", "Site not found", classRetryable}},
	{usecases.ErrZoneNotFound, http.StatusNotFound, problemInfo{"zone-not-found", "Zone not found", classRetryable}},
	{usecases.ErrAisleNotFound, http.StatusNotFound, problemInfo{"aisle-not-found", "Aisle not found", classRetryable}},
	{usecases.ErrLocationSlotNotFound, http.StatusNotFound, problemInfo{"location-slot-not-found", "Location slot not found", classRetryable}},
	{usecases.ErrLocationTypeNotFound, http.StatusNotFound, problemInfo{"location-type-not-found", "Location type not found", classRetryable}},
	{usecases.ErrPlacementRuleNotFound, http.StatusNotFound, problemInfo{"placement-rule-not-found", "Placement rule not found", classRetryable}},

	// 409 Conflict: a code already taken.
	{usecases.ErrDuplicateSite, http.StatusConflict, problemInfo{"duplicate-site-code", "A site with this code already exists", classFinal}},
	{usecases.ErrDuplicateZone, http.StatusConflict, problemInfo{"duplicate-zone", "A zone with this area and zone code already exists in this site", classFinal}},
	{usecases.ErrDuplicateAisle, http.StatusConflict, problemInfo{"duplicate-aisle", "An aisle with this code already exists in this zone", classFinal}},
	{usecases.ErrDuplicateLocationType, http.StatusConflict, problemInfo{"duplicate-location-type", "A location type with this name already exists", classFinal}},
	{usecases.ErrDuplicatePlacementRule, http.StatusConflict, problemInfo{"duplicate-placement-rule", "A placement rule with this id already exists", classFinal}},
	{usecases.ErrDuplicateLocationCode, http.StatusConflict, problemInfo{"duplicate-location-code", "A location slot with this code already exists", classFinal}},
	{usecases.ErrGLNAlreadyAssigned, http.StatusConflict, problemInfo{"gln-already-assigned", "This GLN is already assigned to another location", classFinal}},
	{usecases.ErrDuplicateFixedStructure, http.StatusConflict, problemInfo{"duplicate-fixed-structure", "A fixed structure with this id already exists", classFinal}},
	{usecases.ErrDuplicateCrossAisle, http.StatusConflict, problemInfo{"duplicate-cross-aisle", "A cross-aisle between these aisles at this bay already exists", classFinal}},

	// 409 Conflict: a parent that exists but is no longer Active.
	{usecases.ErrSiteNotActive, http.StatusConflict, problemInfo{"site-not-active", "Site is not active", classFinal}},
	{site.ErrSiteDecommissioned, http.StatusConflict, problemInfo{"site-not-active", "Site is not active", classFinal}},
	{usecases.ErrZoneNotActive, http.StatusConflict, problemInfo{"zone-not-active", "Zone is not active", classFinal}},
	{usecases.ErrAisleNotActive, http.StatusConflict, problemInfo{"aisle-not-active", "Aisle is not active", classFinal}},

	// 409 Conflict: already decommissioned.
	{aisle.ErrAlreadyDecommissioned, http.StatusConflict, problemInfo{slugAlreadyDecommissioned, msgAlreadyDecommissioned, classFinal}},
	{aisle.ErrAisleDecommissioned, http.StatusConflict, problemInfo{slugAlreadyDecommissioned, msgAlreadyDecommissioned, classFinal}},
	{aisle.ErrCrossAisleAlreadyDecommissioned, http.StatusConflict, problemInfo{slugAlreadyDecommissioned, msgAlreadyDecommissioned, classFinal}},
	{site.ErrAlreadyDecommissioned, http.StatusConflict, problemInfo{slugAlreadyDecommissioned, msgAlreadyDecommissioned, classFinal}},
	{zone.ErrAlreadyDecommissioned, http.StatusConflict, problemInfo{slugAlreadyDecommissioned, msgAlreadyDecommissioned, classFinal}},
	{slot.ErrAlreadyDecommissioned, http.StatusConflict, problemInfo{slugAlreadyDecommissioned, msgAlreadyDecommissioned, classFinal}},
	{slot.ErrSlotDecommissioned, http.StatusConflict, problemInfo{slugAlreadyDecommissioned, msgAlreadyDecommissioned, classFinal}},

	// 409 Conflict: optimistic-concurrency guard (ADR-0025) — another
	// writer modified the row between this caller's load and its Save.
	// Distinct from every other 409 above: the request was not rejected on
	// its merits; the caller should re-fetch and retry.
	{ports.ErrConcurrentModification, http.StatusConflict, problemInfo{"concurrent-modification", "The resource was modified by another writer; re-fetch and retry", classRetryable}},

	// 422 Unprocessable Entity: semantically invalid values and rule
	// violations.
	{placement.ErrPlacementRuleViolated, http.StatusUnprocessableEntity, problemInfo{"placement-rule-violated", "Location type is not legal in this zone", classRetryable}},
	{shared.ErrInvalidMaxWeight, http.StatusUnprocessableEntity, problemInfo{"invalid-max-weight", "Capacity max weight must be greater than zero", classFinal}},
	{shared.ErrInvalidMaxVolume, http.StatusUnprocessableEntity, problemInfo{"invalid-max-volume", "Capacity max volume must be greater than zero", classFinal}},
	{shared.ErrUnknownTemperatureClass, http.StatusUnprocessableEntity, problemInfo{"unknown-temperature-class", "Unknown temperature class", classFinal}},
	{shared.ErrUnknownDirection, http.StatusUnprocessableEntity, problemInfo{"unknown-direction", "Unknown aisle direction", classFinal}},
	{shared.ErrUnknownHandlingUnit, http.StatusUnprocessableEntity, problemInfo{"unknown-handling-unit", "Handling unit must be one of EACH, CASE, PALLET", classFinal}},
	{shared.ErrInvalidGLN, http.StatusUnprocessableEntity, problemInfo{"invalid-gln", "GLN must be 13 digits ending in the GS1 mod-10 check digit", classFinal}},
	{shared.ErrUnknownSpotKind, http.StatusUnprocessableEntity, problemInfo{"unknown-spot-kind", "Spot kind must be one of PARKING, HOLDING, REEFER_PLUG, HAZMAT, STAGING_LANE", classFinal}},
	{slot.ErrSpotKindNotAllowed, http.StatusUnprocessableEntity, problemInfo{"spot-kind-not-allowed", "spotKind may only be set on a Yard location", classRetryable}},
	{shared.ErrUnknownStatus, http.StatusUnprocessableEntity, problemInfo{"unknown-status", "Unknown lifecycle status", classFinal}},
	{shared.ErrInvalidZ, http.StatusUnprocessableEntity, problemInfo{"invalid-z", "Z coordinate must not be negative", classFinal}},
	{shared.ErrInvalidDimensions, http.StatusUnprocessableEntity, problemInfo{"invalid-dimensions", "Width, depth, and height must all be greater than zero", classFinal}},
	{shared.ErrSegmentEndpointsNotReal, http.StatusUnprocessableEntity, problemInfo{"segment-endpoints-not-real", "Centreline endpoints must both be real points", classFinal}},
	{shared.ErrSegmentStartEndEqual, http.StatusUnprocessableEntity, problemInfo{"segment-start-end-equal", "Centreline start and end must differ", classFinal}},
	{placement.ErrUnknownEffect, http.StatusUnprocessableEntity, problemInfo{"unknown-placement-effect", "Unknown placement rule effect", classFinal}},
	{placement.ErrEmptyPredicate, http.StatusUnprocessableEntity, problemInfo{"empty-zone-predicate", "Placement rule predicate constrains nothing", classFinal}},
	{placement.ErrUnknownLocationRole, http.StatusUnprocessableEntity, problemInfo{"unknown-location-role", "Unknown location role", classFinal}},
	{site.ErrInvalidSiteRole, http.StatusUnprocessableEntity, problemInfo{"invalid-site-role", "Site role must be one of the known site roles", classFinal}},
	{slot.ErrUnknownDockFlow, http.StatusUnprocessableEntity, problemInfo{"unknown-dock-flow", "Unknown dock flow", classFinal}},
	{slot.ErrUnknownActivity, http.StatusUnprocessableEntity, problemInfo{"unknown-activity", "Unknown work center activity", classFinal}},
	{slot.ErrDockFlowRequired, http.StatusUnprocessableEntity, problemInfo{"dock-flow-required", "A Dock location requires a dockFlow", classRetryable}},
	{slot.ErrWorkCenterActivitiesRequired, http.StatusUnprocessableEntity, problemInfo{"work-center-activities-required", "A WorkCenter location requires at least one activity", classRetryable}},
	{slot.ErrFunctionalAttributesNotAllowed, http.StatusUnprocessableEntity, problemInfo{"functional-attributes-not-allowed", "dockFlow and activities may only be set on a Dock or WorkCenter location", classRetryable}},
	{slot.ErrNegativePickSequence, http.StatusUnprocessableEntity, problemInfo{"negative-pick-sequence", "Pick sequence must not be negative", classFinal}},
	{aisle.ErrNegativeSequenceHint, http.StatusUnprocessableEntity, problemInfo{"negative-sequence-hint", "Aisle sequence hint must not be negative", classFinal}},
	{slot.ErrZoneMismatch, http.StatusUnprocessableEntity, problemInfo{"zone-mismatch", "Zone attributes do not match the location code's zone", classFinal}},
	{structure.ErrUnknownKind, http.StatusUnprocessableEntity, problemInfo{"unknown-fixed-structure-kind", "Unknown fixed structure kind", classFinal}},
	{structure.ErrEmptyFootprint, http.StatusUnprocessableEntity, problemInfo{"empty-fixed-structure-footprint", "Fixed structure requires a real footprint", classFinal}},
	{aisle.ErrCrossAisleSameAisle, http.StatusUnprocessableEntity, problemInfo{"cross-aisle-same-aisle", "Cross-aisle must connect two distinct aisles", classFinal}},
	{usecases.ErrCrossAisleAisleMismatch, http.StatusUnprocessableEntity, problemInfo{"cross-aisle-aisle-mismatch", "Cross-aisle aisles must both belong to the named zone", classRetryable}},
	{usecases.ErrNoRouteBetweenZones, http.StatusUnprocessableEntity, problemInfo{"no-route-between-zones", "No route: the two locations are in different zones", classRetryable}},
	{travel.ErrNoRoute, http.StatusUnprocessableEntity, problemInfo{"no-route", "No route exists between these two waypoints", classRetryable}},
	{travel.ErrUnknownNode, http.StatusUnprocessableEntity, problemInfo{"unknown-travel-waypoint", "The travel graph has no waypoint for this aisle/bay", classRetryable}},
	{zone.ErrInvalidPitch, http.StatusUnprocessableEntity, problemInfo{"invalid-pitch", "Bay pitch and level pitch must both be greater than zero", classFinal}},

	// 400 Bad Request: malformed input.
	{shared.ErrMalformedLocationCode, http.StatusBadRequest, problemInfo{slugMalformedLocationCode, msgMalformedLocationCode, classFinal}},
	{shared.ErrEmptyLocationSegment, http.StatusBadRequest, problemInfo{slugMalformedLocationCode, msgMalformedLocationCode, classFinal}},
	{shared.ErrInvalidLocationSegment, http.StatusBadRequest, problemInfo{slugMalformedLocationCode, msgMalformedLocationCode, classFinal}},
	{site.ErrEmptySiteCode, http.StatusBadRequest, problemInfo{slugInvalidSiteCode, msgInvalidSiteCode, classFinal}},
	{site.ErrInvalidSiteCode, http.StatusBadRequest, problemInfo{slugInvalidSiteCode, msgInvalidSiteCode, classFinal}},
	{zone.ErrEmptySiteCode, http.StatusBadRequest, problemInfo{slugInvalidSiteCode, msgInvalidSiteCode, classFinal}},
	{site.ErrEmptySiteName, http.StatusBadRequest, problemInfo{"empty-site-name", "Site name must not be empty", classFinal}},
	{zone.ErrEmptyAreaCode, http.StatusBadRequest, problemInfo{slugInvalidZoneCode, msgInvalidZoneCode, classFinal}},
	{zone.ErrEmptyZoneCode, http.StatusBadRequest, problemInfo{slugInvalidZoneCode, msgInvalidZoneCode, classFinal}},
	{zone.ErrInvalidCode, http.StatusBadRequest, problemInfo{slugInvalidZoneCode, msgInvalidZoneCode, classFinal}},
	{aisle.ErrEmptyZoneID, http.StatusBadRequest, problemInfo{slugInvalidAisleCode, msgInvalidAisleCode, classFinal}},
	{aisle.ErrEmptyAisleCode, http.StatusBadRequest, problemInfo{slugInvalidAisleCode, msgInvalidAisleCode, classFinal}},
	{aisle.ErrInvalidAisleCode, http.StatusBadRequest, problemInfo{slugInvalidAisleCode, msgInvalidAisleCode, classFinal}},
	{placement.ErrEmptyLocationTypeName, http.StatusBadRequest, problemInfo{slugInvalidLocationType, msgInvalidLocationType, classFinal}},
	{placement.ErrEmptyRuleID, http.StatusBadRequest, problemInfo{"empty-placement-rule-id", "Placement rule id must not be empty", classFinal}},
	{placement.ErrEmptyRuleLocationType, http.StatusBadRequest, problemInfo{slugInvalidLocationType, msgInvalidLocationType, classFinal}},
	{slot.ErrMissingLocationCode, http.StatusBadRequest, problemInfo{"missing-location-code", "Location code is required", classFinal}},
	{slot.ErrMissingLocationType, http.StatusBadRequest, problemInfo{slugInvalidLocationType, msgInvalidLocationType, classFinal}},
	{structure.ErrEmptyID, http.StatusBadRequest, problemInfo{"empty-fixed-structure-id", "Fixed structure requires an id", classFinal}},
	{structure.ErrEmptySiteCode, http.StatusBadRequest, problemInfo{"empty-fixed-structure-site-code", "Fixed structure must be scoped to a site code", classFinal}},
	{structure.ErrEmptyLabel, http.StatusBadRequest, problemInfo{"empty-fixed-structure-label", "Fixed structure requires a label", classFinal}},
	{structure.ErrEmptyKind, http.StatusBadRequest, problemInfo{"empty-fixed-structure-kind", "Fixed structure requires a kind", classFinal}},
	{aisle.ErrCrossAisleEmptyZoneID, http.StatusBadRequest, problemInfo{"empty-cross-aisle-zone-id", "Cross-aisle must be scoped to a zone id", classFinal}},
	{aisle.ErrCrossAisleEmptyFromAisle, http.StatusBadRequest, problemInfo{"empty-cross-aisle-from-aisle", "Cross-aisle requires a from-aisle code", classFinal}},
	{aisle.ErrCrossAisleEmptyToAisle, http.StatusBadRequest, problemInfo{"empty-cross-aisle-to-aisle", "Cross-aisle requires a to-aisle code", classFinal}},
	{aisle.ErrCrossAisleEmptyBay, http.StatusBadRequest, problemInfo{"empty-cross-aisle-bay", "Cross-aisle requires a bay", classFinal}},
	{usecases.ErrEmptyImport, http.StatusBadRequest, problemInfo{"empty-import", "Facility layout import must contain at least one row", classFinal}},
	{errMissingGeometryField, http.StatusBadRequest, problemInfo{"missing-geometry-field", "Geometry coordinates and dimensions must be numbers, not null", classFinal}},
	{errMissingSequenceHint, http.StatusBadRequest, problemInfo{"missing-sequence-hint", "Aisle sequenceHint is required", classFinal}},
	{errMissingTransferCapabilityField, http.StatusBadRequest, problemInfo{"missing-transfer-capability-field", "transferOriginEnabled and transferDestinationEnabled are both required booleans", classFinal}},
}

// categoryFor returns the first category whose sentinel matches err via
// errors.Is. ok is false for unmapped errors, which surface as 500
// internal-error.
func categoryFor(err error) (errorCategory, bool) {
	for _, c := range errorCategories {
		if errors.Is(err, c.err) {
			return c, true
		}
	}
	return errorCategory{}, false
}

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
	if c, ok := categoryFor(err); ok {
		return c.status
	}
	return http.StatusInternalServerError
}

// problemBaseURI is the namespace for this service's RFC 7807 "type" URIs.
// It does not need to resolve to a real page — it's an identifier, unique
// per distinct error category in this service.
const problemBaseURI = "https://errors.facility-layout.warehouse-systems.dev/"

// problemInfo is the fixed, category-level (type, title) pair for an RFC
// 7807 problem response. slug becomes the last path segment of "type";
// title is a fixed human string for the category (the dynamic detail comes
// from err.Error() at write time, not from this table). class is the replay
// class of the problem type (replay_class.go, ADR 0046): what the
// Idempotency-Key middleware does with this answer when the same request is
// retried under the same key. Every entry declares one; there is no default.
type problemInfo struct {
	slug  string
	title string
	class replayClass
}

// problemFor maps a typed domain/application error to its RFC 7807
// (type, title) pair, mirroring statusFor's groupings one-for-one.
func problemFor(err error) problemInfo {
	if c, ok := categoryFor(err); ok {
		return c.problem
	}
	return problemInternalError
}
