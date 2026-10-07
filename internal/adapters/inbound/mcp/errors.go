package mcp

import (
	"errors"
	"fmt"
	"log/slog"

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

// Fleet tool-error convention (ADR-0033). A tool error is "<slug>: <detail>":
// the slug is the stable problem-type slug the REST adapter reports for the
// same condition (the last segment of its RFC 7807 "type" URI,
// internal/adapters/inbound/http/errors.go), so a caller can tell a rejection
// of its input from an internal failure without parsing prose. Anything that
// is not a typed domain error becomes `internal-error` and its detail is
// withheld. Success payloads are unchanged.

// Slugs the tools raise for their own argument checks, where no domain
// sentinel is involved. Each is a slug REST also uses.
const (
	slugInvalidSiteCode     = "invalid-site-code"
	slugInvalidZoneCode     = "invalid-zone-code"
	slugMissingLocationCode = "missing-location-code"
	slugInvalidReportQuery  = "invalid-report-query"
	slugInternalError       = "internal-error"
)

// internalErrorDetail is all a caller learns about an unmapped error.
const internalErrorDetail = "an unexpected internal error occurred"

type errorEntry struct {
	target error
	slug   string
}

// errorCatalog maps each typed domain/application error to its REST slug. It
// mirrors the REST table one-for-one (TestSlugTableMatchesRESTOneForOne fails
// when the two drift); only the two sentinels REST raises while decoding a
// request body are absent, since no tool decodes one.
var errorCatalog = []errorEntry{
	// 404 Not Found: the named resource does not exist.
	{usecases.ErrSiteNotFound, "site-not-found"},
	{usecases.ErrZoneNotFound, "zone-not-found"},
	{usecases.ErrAisleNotFound, "aisle-not-found"},
	{usecases.ErrLocationSlotNotFound, "location-slot-not-found"},
	{usecases.ErrLocationTypeNotFound, "location-type-not-found"},
	{usecases.ErrPlacementRuleNotFound, "placement-rule-not-found"},

	// 409 Conflict: a code already taken.
	{usecases.ErrDuplicateSite, "duplicate-site-code"},
	{usecases.ErrDuplicateZone, "duplicate-zone"},
	{usecases.ErrDuplicateAisle, "duplicate-aisle"},
	{usecases.ErrDuplicateLocationType, "duplicate-location-type"},
	{usecases.ErrDuplicatePlacementRule, "duplicate-placement-rule"},
	{usecases.ErrDuplicateLocationCode, "duplicate-location-code"},
	{usecases.ErrDuplicateFixedStructure, "duplicate-fixed-structure"},
	{usecases.ErrDuplicateCrossAisle, "duplicate-cross-aisle"},

	// 409 Conflict: a parent that exists but is no longer Active.
	{usecases.ErrSiteNotActive, "site-not-active"},
	{usecases.ErrZoneNotActive, "zone-not-active"},
	{usecases.ErrAisleNotActive, "aisle-not-active"},

	// 409 Conflict: already decommissioned.
	{aisle.ErrAlreadyDecommissioned, "already-decommissioned"},
	{aisle.ErrAisleDecommissioned, "already-decommissioned"},
	{aisle.ErrCrossAisleAlreadyDecommissioned, "already-decommissioned"},
	{site.ErrAlreadyDecommissioned, "already-decommissioned"},
	{zone.ErrAlreadyDecommissioned, "already-decommissioned"},
	{slot.ErrAlreadyDecommissioned, "already-decommissioned"},
	{slot.ErrSlotDecommissioned, "already-decommissioned"},

	// 409 Conflict: optimistic-concurrency guard (ADR-0025)
	{ports.ErrConcurrentModification, "concurrent-modification"},

	// 422 Unprocessable Entity: semantically invalid values and rule
	{placement.ErrPlacementRuleViolated, "placement-rule-violated"},
	{shared.ErrInvalidMaxWeight, "invalid-max-weight"},
	{shared.ErrInvalidMaxVolume, "invalid-max-volume"},
	{shared.ErrUnknownTemperatureClass, "unknown-temperature-class"},
	{shared.ErrUnknownDirection, "unknown-direction"},
	{shared.ErrUnknownStatus, "unknown-status"},
	{shared.ErrInvalidZ, "invalid-z"},
	{shared.ErrInvalidDimensions, "invalid-dimensions"},
	{shared.ErrSegmentEndpointsNotReal, "segment-endpoints-not-real"},
	{shared.ErrSegmentStartEndEqual, "segment-start-end-equal"},
	{placement.ErrUnknownEffect, "unknown-placement-effect"},
	{placement.ErrEmptyPredicate, "empty-zone-predicate"},
	{placement.ErrUnknownLocationRole, "unknown-location-role"},
	{slot.ErrUnknownDockFlow, "unknown-dock-flow"},
	{slot.ErrUnknownActivity, "unknown-activity"},
	{slot.ErrDockFlowRequired, "dock-flow-required"},
	{slot.ErrWorkCenterActivitiesRequired, "work-center-activities-required"},
	{slot.ErrFunctionalAttributesNotAllowed, "functional-attributes-not-allowed"},
	{slot.ErrNegativePickSequence, "negative-pick-sequence"},
	{aisle.ErrNegativeSequenceHint, "negative-sequence-hint"},
	{slot.ErrZoneMismatch, "zone-mismatch"},
	{structure.ErrUnknownKind, "unknown-fixed-structure-kind"},
	{structure.ErrEmptyFootprint, "empty-fixed-structure-footprint"},
	{aisle.ErrCrossAisleSameAisle, "cross-aisle-same-aisle"},
	{usecases.ErrCrossAisleAisleMismatch, "cross-aisle-aisle-mismatch"},
	{usecases.ErrNoRouteBetweenZones, "no-route-between-zones"},
	{travel.ErrNoRoute, "no-route"},
	{travel.ErrUnknownNode, "unknown-travel-waypoint"},
	{zone.ErrInvalidPitch, "invalid-pitch"},

	// 400 Bad Request: malformed input.
	{shared.ErrMalformedLocationCode, "malformed-location-code"},
	{shared.ErrEmptyLocationSegment, "malformed-location-code"},
	{shared.ErrInvalidLocationSegment, "malformed-location-code"},
	{site.ErrEmptySiteCode, "invalid-site-code"},
	{site.ErrInvalidSiteCode, "invalid-site-code"},
	{zone.ErrEmptySiteCode, "invalid-site-code"},
	{site.ErrEmptySiteName, "empty-site-name"},
	{zone.ErrEmptyAreaCode, "invalid-zone-code"},
	{zone.ErrEmptyZoneCode, "invalid-zone-code"},
	{zone.ErrInvalidCode, "invalid-zone-code"},
	{aisle.ErrEmptyZoneID, "invalid-aisle-code"},
	{aisle.ErrEmptyAisleCode, "invalid-aisle-code"},
	{aisle.ErrInvalidAisleCode, "invalid-aisle-code"},
	{placement.ErrEmptyLocationTypeName, "invalid-location-type"},
	{placement.ErrEmptyRuleID, "empty-placement-rule-id"},
	{placement.ErrEmptyRuleLocationType, "invalid-location-type"},
	{slot.ErrMissingLocationCode, "missing-location-code"},
	{slot.ErrMissingLocationType, "invalid-location-type"},
	{structure.ErrEmptyID, "empty-fixed-structure-id"},
	{structure.ErrEmptySiteCode, "empty-fixed-structure-site-code"},
	{structure.ErrEmptyLabel, "empty-fixed-structure-label"},
	{aisle.ErrCrossAisleEmptyZoneID, "empty-cross-aisle-zone-id"},
	{aisle.ErrCrossAisleEmptyFromAisle, "empty-cross-aisle-from-aisle"},
	{aisle.ErrCrossAisleEmptyToAisle, "empty-cross-aisle-to-aisle"},
	{aisle.ErrCrossAisleEmptyBay, "empty-cross-aisle-bay"},
	{usecases.ErrEmptyImport, "empty-import"},
}

// slugFor returns the REST slug for a typed domain error. ok is false for an
// untyped error.
func slugFor(err error) (slug string, ok bool) {
	for _, entry := range errorCatalog {
		if errors.Is(err, entry.target) {
			return entry.slug, true
		}
	}
	return "", false
}

// toolErr is a tool-level error already in "<slug>: <detail>" form.
type toolErr struct {
	slug   string
	detail string
}

func (e *toolErr) Error() string { return fmt.Sprintf("%s: %s", e.slug, e.detail) }

// toolError builds a tool-level error "<slug>: <detail>". Returned from a
// handler it becomes an isError tool result, never a transport failure.
func toolError(slug, detail string) error {
	return &toolErr{slug: slug, detail: detail}
}

// mapError turns a handler error into the error the caller sees. An error
// already built by toolError passes through; a typed domain error is prefixed
// with its REST slug and keeps its message; anything else is logged and
// reported generically so infrastructure details (DSNs, SQL) never reach the
// model. A nil error stays nil.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	var te *toolErr
	if errors.As(err, &te) {
		return te
	}
	if slug, ok := slugFor(err); ok {
		return toolError(slug, err.Error())
	}
	slog.Error("mcp tool failed with an unexpected error", "error", err)
	return toolError(slugInternalError, internalErrorDetail)
}
