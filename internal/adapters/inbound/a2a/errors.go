package a2a

import (
	"errors"
	"log/slog"
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

// problemInfo is the fixed, category-level (slug, title) pair of one problem
// type. The dynamic detail comes from the error text at answer time.
type problemInfo struct {
	slug  string
	title string
}

// errorCategory ties one typed sentinel error to both facets of its problem:
// the HTTP-style status and the fixed (slug, title) pair.
type errorCategory struct {
	err     error
	status  int
	problem problemInfo
}

// errorCategories is this adapter's copy of the REST adapter's error table
// (internal/adapters/inbound/http/errors.go), in the same order, so a
// delegating agent sees the status and problem type a REST caller would. The
// copy keeps the adapters independent (this package depends on application
// and domain only); TestErrorTableMatchesRESTOneForOne fails when the two
// drift. Only the three REST-body-decoding sentinels are absent: no skill
// decodes a REST body. Sentinels a read skill cannot raise today stay in the
// table so a future skill inherits the full classification.
var errorCategories = []errorCategory{
	// 404 Not Found: the named resource does not exist.
	{usecases.ErrSiteNotFound, http.StatusNotFound, problemInfo{"site-not-found", "Site not found"}},
	{usecases.ErrZoneNotFound, http.StatusNotFound, problemInfo{"zone-not-found", "Zone not found"}},
	{usecases.ErrAisleNotFound, http.StatusNotFound, problemInfo{"aisle-not-found", "Aisle not found"}},
	{usecases.ErrLocationSlotNotFound, http.StatusNotFound, problemInfo{"location-slot-not-found", "Location slot not found"}},
	{usecases.ErrLocationTypeNotFound, http.StatusNotFound, problemInfo{"location-type-not-found", "Location type not found"}},
	{usecases.ErrPlacementRuleNotFound, http.StatusNotFound, problemInfo{"placement-rule-not-found", "Placement rule not found"}},

	// 409 Conflict: a code already taken.
	{usecases.ErrDuplicateSite, http.StatusConflict, problemInfo{"duplicate-site-code", "A site with this code already exists"}},
	{usecases.ErrDuplicateZone, http.StatusConflict, problemInfo{"duplicate-zone", "A zone with this area and zone code already exists in this site"}},
	{usecases.ErrDuplicateAisle, http.StatusConflict, problemInfo{"duplicate-aisle", "An aisle with this code already exists in this zone"}},
	{usecases.ErrDuplicateLocationType, http.StatusConflict, problemInfo{"duplicate-location-type", "A location type with this name already exists"}},
	{usecases.ErrDuplicatePlacementRule, http.StatusConflict, problemInfo{"duplicate-placement-rule", "A placement rule with this id already exists"}},
	{usecases.ErrDuplicateLocationCode, http.StatusConflict, problemInfo{"duplicate-location-code", "A location slot with this code already exists"}},
	{usecases.ErrDuplicateFixedStructure, http.StatusConflict, problemInfo{"duplicate-fixed-structure", "A fixed structure with this id already exists"}},
	{usecases.ErrDuplicateCrossAisle, http.StatusConflict, problemInfo{"duplicate-cross-aisle", "A cross-aisle between these aisles at this bay already exists"}},

	// 409 Conflict: a parent that exists but is no longer Active.
	{usecases.ErrSiteNotActive, http.StatusConflict, problemInfo{"site-not-active", "Site is not active"}},
	{site.ErrSiteDecommissioned, http.StatusConflict, problemInfo{"site-not-active", "Site is not active"}},
	{usecases.ErrZoneNotActive, http.StatusConflict, problemInfo{"zone-not-active", "Zone is not active"}},
	{usecases.ErrAisleNotActive, http.StatusConflict, problemInfo{"aisle-not-active", "Aisle is not active"}},

	// 409 Conflict: already decommissioned.
	{aisle.ErrAlreadyDecommissioned, http.StatusConflict, problemInfo{"already-decommissioned", "This structure is already decommissioned"}},
	{aisle.ErrAisleDecommissioned, http.StatusConflict, problemInfo{"already-decommissioned", "This structure is already decommissioned"}},
	{aisle.ErrCrossAisleAlreadyDecommissioned, http.StatusConflict, problemInfo{"already-decommissioned", "This structure is already decommissioned"}},
	{site.ErrAlreadyDecommissioned, http.StatusConflict, problemInfo{"already-decommissioned", "This structure is already decommissioned"}},
	{zone.ErrAlreadyDecommissioned, http.StatusConflict, problemInfo{"already-decommissioned", "This structure is already decommissioned"}},
	{slot.ErrAlreadyDecommissioned, http.StatusConflict, problemInfo{"already-decommissioned", "This structure is already decommissioned"}},
	{slot.ErrSlotDecommissioned, http.StatusConflict, problemInfo{"already-decommissioned", "This structure is already decommissioned"}},

	// 409 Conflict: optimistic-concurrency guard (ADR-0025) — another
	// writer modified the row between this caller's load and its Save.
	// Distinct from every other 409 above: the request was not rejected on
	// its merits; the caller should re-fetch and retry.
	{ports.ErrConcurrentModification, http.StatusConflict, problemInfo{"concurrent-modification", "The resource was modified by another writer; re-fetch and retry"}},

	// 422 Unprocessable Entity: semantically invalid values and rule
	// violations.
	{placement.ErrPlacementRuleViolated, http.StatusUnprocessableEntity, problemInfo{"placement-rule-violated", "Location type is not legal in this zone"}},
	{shared.ErrInvalidMaxWeight, http.StatusUnprocessableEntity, problemInfo{"invalid-max-weight", "Capacity max weight must be greater than zero"}},
	{shared.ErrInvalidMaxVolume, http.StatusUnprocessableEntity, problemInfo{"invalid-max-volume", "Capacity max volume must be greater than zero"}},
	{shared.ErrUnknownTemperatureClass, http.StatusUnprocessableEntity, problemInfo{"unknown-temperature-class", "Unknown temperature class"}},
	{shared.ErrUnknownDirection, http.StatusUnprocessableEntity, problemInfo{"unknown-direction", "Unknown aisle direction"}},
	{shared.ErrUnknownHandlingUnit, http.StatusUnprocessableEntity, problemInfo{"unknown-handling-unit", "Handling unit must be one of EACH, CASE, PALLET"}},
	{shared.ErrUnknownStatus, http.StatusUnprocessableEntity, problemInfo{"unknown-status", "Unknown lifecycle status"}},
	{shared.ErrInvalidZ, http.StatusUnprocessableEntity, problemInfo{"invalid-z", "Z coordinate must not be negative"}},
	{shared.ErrInvalidDimensions, http.StatusUnprocessableEntity, problemInfo{"invalid-dimensions", "Width, depth, and height must all be greater than zero"}},
	{shared.ErrSegmentEndpointsNotReal, http.StatusUnprocessableEntity, problemInfo{"segment-endpoints-not-real", "Centreline endpoints must both be real points"}},
	{shared.ErrSegmentStartEndEqual, http.StatusUnprocessableEntity, problemInfo{"segment-start-end-equal", "Centreline start and end must differ"}},
	{placement.ErrUnknownEffect, http.StatusUnprocessableEntity, problemInfo{"unknown-placement-effect", "Unknown placement rule effect"}},
	{placement.ErrEmptyPredicate, http.StatusUnprocessableEntity, problemInfo{"empty-zone-predicate", "Placement rule predicate constrains nothing"}},
	{placement.ErrUnknownLocationRole, http.StatusUnprocessableEntity, problemInfo{"unknown-location-role", "Unknown location role"}},
	{site.ErrInvalidSiteRole, http.StatusUnprocessableEntity, problemInfo{"invalid-site-role", "Site role must be one of the known site roles"}},
	{slot.ErrUnknownDockFlow, http.StatusUnprocessableEntity, problemInfo{"unknown-dock-flow", "Unknown dock flow"}},
	{slot.ErrUnknownActivity, http.StatusUnprocessableEntity, problemInfo{"unknown-activity", "Unknown work center activity"}},
	{slot.ErrDockFlowRequired, http.StatusUnprocessableEntity, problemInfo{"dock-flow-required", "A Dock location requires a dockFlow"}},
	{slot.ErrWorkCenterActivitiesRequired, http.StatusUnprocessableEntity, problemInfo{"work-center-activities-required", "A WorkCenter location requires at least one activity"}},
	{slot.ErrFunctionalAttributesNotAllowed, http.StatusUnprocessableEntity, problemInfo{"functional-attributes-not-allowed", "dockFlow and activities may only be set on a Dock or WorkCenter location"}},
	{slot.ErrNegativePickSequence, http.StatusUnprocessableEntity, problemInfo{"negative-pick-sequence", "Pick sequence must not be negative"}},
	{aisle.ErrNegativeSequenceHint, http.StatusUnprocessableEntity, problemInfo{"negative-sequence-hint", "Aisle sequence hint must not be negative"}},
	{slot.ErrZoneMismatch, http.StatusUnprocessableEntity, problemInfo{"zone-mismatch", "Zone attributes do not match the location code's zone"}},
	{structure.ErrUnknownKind, http.StatusUnprocessableEntity, problemInfo{"unknown-fixed-structure-kind", "Unknown fixed structure kind"}},
	{structure.ErrEmptyFootprint, http.StatusUnprocessableEntity, problemInfo{"empty-fixed-structure-footprint", "Fixed structure requires a real footprint"}},
	{aisle.ErrCrossAisleSameAisle, http.StatusUnprocessableEntity, problemInfo{"cross-aisle-same-aisle", "Cross-aisle must connect two distinct aisles"}},
	{usecases.ErrCrossAisleAisleMismatch, http.StatusUnprocessableEntity, problemInfo{"cross-aisle-aisle-mismatch", "Cross-aisle aisles must both belong to the named zone"}},
	{usecases.ErrNoRouteBetweenZones, http.StatusUnprocessableEntity, problemInfo{"no-route-between-zones", "No route: the two locations are in different zones"}},
	{travel.ErrNoRoute, http.StatusUnprocessableEntity, problemInfo{"no-route", "No route exists between these two waypoints"}},
	{travel.ErrUnknownNode, http.StatusUnprocessableEntity, problemInfo{"unknown-travel-waypoint", "The travel graph has no waypoint for this aisle/bay"}},
	{zone.ErrInvalidPitch, http.StatusUnprocessableEntity, problemInfo{"invalid-pitch", "Bay pitch and level pitch must both be greater than zero"}},

	// 400 Bad Request: malformed input.
	{shared.ErrMalformedLocationCode, http.StatusBadRequest, problemInfo{"malformed-location-code", "Malformed location code"}},
	{shared.ErrEmptyLocationSegment, http.StatusBadRequest, problemInfo{"malformed-location-code", "Malformed location code"}},
	{shared.ErrInvalidLocationSegment, http.StatusBadRequest, problemInfo{"malformed-location-code", "Malformed location code"}},
	{site.ErrEmptySiteCode, http.StatusBadRequest, problemInfo{"invalid-site-code", "Invalid site code"}},
	{site.ErrInvalidSiteCode, http.StatusBadRequest, problemInfo{"invalid-site-code", "Invalid site code"}},
	{zone.ErrEmptySiteCode, http.StatusBadRequest, problemInfo{"invalid-site-code", "Invalid site code"}},
	{site.ErrEmptySiteName, http.StatusBadRequest, problemInfo{"empty-site-name", "Site name must not be empty"}},
	{zone.ErrEmptyAreaCode, http.StatusBadRequest, problemInfo{"invalid-zone-code", "Invalid area or zone code"}},
	{zone.ErrEmptyZoneCode, http.StatusBadRequest, problemInfo{"invalid-zone-code", "Invalid area or zone code"}},
	{zone.ErrInvalidCode, http.StatusBadRequest, problemInfo{"invalid-zone-code", "Invalid area or zone code"}},
	{aisle.ErrEmptyZoneID, http.StatusBadRequest, problemInfo{"invalid-aisle-code", "Invalid aisle code"}},
	{aisle.ErrEmptyAisleCode, http.StatusBadRequest, problemInfo{"invalid-aisle-code", "Invalid aisle code"}},
	{aisle.ErrInvalidAisleCode, http.StatusBadRequest, problemInfo{"invalid-aisle-code", "Invalid aisle code"}},
	{placement.ErrEmptyLocationTypeName, http.StatusBadRequest, problemInfo{"invalid-location-type", "Invalid location type"}},
	{placement.ErrEmptyRuleID, http.StatusBadRequest, problemInfo{"empty-placement-rule-id", "Placement rule id must not be empty"}},
	{placement.ErrEmptyRuleLocationType, http.StatusBadRequest, problemInfo{"invalid-location-type", "Invalid location type"}},
	{slot.ErrMissingLocationCode, http.StatusBadRequest, problemInfo{"missing-location-code", "Location code is required"}},
	{slot.ErrMissingLocationType, http.StatusBadRequest, problemInfo{"invalid-location-type", "Invalid location type"}},
	{structure.ErrEmptyID, http.StatusBadRequest, problemInfo{"empty-fixed-structure-id", "Fixed structure requires an id"}},
	{structure.ErrEmptySiteCode, http.StatusBadRequest, problemInfo{"empty-fixed-structure-site-code", "Fixed structure must be scoped to a site code"}},
	{structure.ErrEmptyLabel, http.StatusBadRequest, problemInfo{"empty-fixed-structure-label", "Fixed structure requires a label"}},
	{structure.ErrEmptyKind, http.StatusBadRequest, problemInfo{"empty-fixed-structure-kind", "Fixed structure requires a kind"}},
	{aisle.ErrCrossAisleEmptyZoneID, http.StatusBadRequest, problemInfo{"empty-cross-aisle-zone-id", "Cross-aisle must be scoped to a zone id"}},
	{aisle.ErrCrossAisleEmptyFromAisle, http.StatusBadRequest, problemInfo{"empty-cross-aisle-from-aisle", "Cross-aisle requires a from-aisle code"}},
	{aisle.ErrCrossAisleEmptyToAisle, http.StatusBadRequest, problemInfo{"empty-cross-aisle-to-aisle", "Cross-aisle requires a to-aisle code"}},
	{aisle.ErrCrossAisleEmptyBay, http.StatusBadRequest, problemInfo{"empty-cross-aisle-bay", "Cross-aisle requires a bay"}},
	{usecases.ErrEmptyImport, http.StatusBadRequest, problemInfo{"empty-import", "Facility layout import must contain at least one row"}},
}

// categoryFor returns the first category whose sentinel matches err via
// errors.Is. ok is false for unmapped errors, which surface as 500.
func categoryFor(err error) (errorCategory, bool) {
	for _, c := range errorCategories {
		if errors.Is(err, c.err) {
			return c, true
		}
	}
	return errorCategory{}, false
}

// categoryForSlug returns the category a slug belongs to (the first entry
// declaring it). It lets the adapter's own argument checks reuse the REST
// classification of the slug instead of restating a status.
func categoryForSlug(slug string) (errorCategory, bool) {
	for _, c := range errorCategories {
		if c.problem.slug == slug {
			return c, true
		}
	}
	return errorCategory{}, false
}

// problemFromUseCase maps a use case or domain error to a problem answer,
// using the SAME classification as the REST adapter so a delegating agent
// sees the outcome a REST caller would: 404 not found, 409 conflict, 422
// semantically invalid, 400 malformed. Anything unrecognised is an internal
// failure, still answered as a problem (500) so the calling agent always
// receives a structured, machine-readable result; the cause is logged, never
// returned.
func problemFromUseCase(err error) *problem {
	if c, ok := categoryFor(err); ok {
		return &problem{Type: problemTypeBase + "/" + c.problem.slug, Title: c.problem.title, Detail: err.Error(), Status: c.status}
	}
	slog.Error("a2a skill failed", "error", err)
	return &problem{Type: problemTypeBase + "/internal-error", Title: "An unexpected internal error occurred", Detail: internalErrorDetail, Status: http.StatusInternalServerError}
}

// internalErrorDetail is all a caller learns about an unmapped error.
const internalErrorDetail = "the skill failed; see server logs"

// problemForSlug builds a problem for the adapter's own argument checks with
// the status and title REST reports for that slug.
func problemForSlug(slug, detail string) *problem {
	c, ok := categoryForSlug(slug)
	if !ok {
		// A slug missing from the table is a programming error caught by
		// TestArgumentSlugsExistInTheRESTTable; fail closed as a 400.
		return &problem{Type: problemTypeBase + "/" + slug, Title: "Invalid skill argument", Detail: detail, Status: http.StatusBadRequest}
	}
	return &problem{Type: problemTypeBase + "/" + slug, Title: c.problem.title, Detail: detail, Status: c.status}
}
