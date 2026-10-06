package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	inboundhttp "github.com/claudioed/facility-layout/internal/adapters/inbound/http"
	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/slot"
)

// restOnlySlugs are REST problem slugs raised by the HTTP adapter's own
// request decoding (unexported sentinels of that package). No tool decodes
// such a body, so the MCP table deliberately has no entry for them.
var restOnlySlugs = map[string]bool{
	"missing-geometry-field": true,
	"missing-sequence-hint":  true,
}

// TestSlugTableMatchesRESTOneForOne pins the contract of ADR-0033: the MCP
// slug table and the REST problem table are the same vocabulary. Every typed
// error REST maps (bar the REST-decoding-only ones) must map here to the SAME
// slug, and every entry here must be an error REST maps to that slug.
func TestSlugTableMatchesRESTOneForOne(t *testing.T) {
	rest := inboundhttp.ErrorSlugs()
	if len(rest) == 0 {
		t.Fatal("REST adapter exposes no error slugs")
	}

	for _, entry := range rest {
		got, ok := slugFor(entry.Err)
		if restOnlySlugs[entry.Slug] {
			if ok {
				t.Errorf("REST-decoding-only slug %q unexpectedly mapped by MCP as %q", entry.Slug, got)
			}
			continue
		}
		if !ok {
			t.Errorf("REST maps %v to %q but the MCP table has no entry for it", entry.Err, entry.Slug)
			continue
		}
		if got != entry.Slug {
			t.Errorf("%v: REST slug %q, MCP slug %q", entry.Err, entry.Slug, got)
		}
	}

	restSlugByErr := make(map[error]string, len(rest))
	for _, entry := range rest {
		restSlugByErr[entry.Err] = entry.Slug
	}
	for _, entry := range errorCatalog {
		want, ok := restSlugByErr[entry.target]
		if !ok {
			t.Errorf("MCP maps %v to %q but REST has no entry for that error", entry.target, entry.slug)
			continue
		}
		if want != entry.slug {
			t.Errorf("%v: MCP slug %q, REST slug %q", entry.target, entry.slug, want)
		}
	}
	if want := len(rest) - countRESTOnly(rest); len(errorCatalog) != want {
		t.Errorf("MCP table has %d entries, REST has %d mappable entries", len(errorCatalog), want)
	}
}

func countRESTOnly(rest []inboundhttp.ErrorSlug) int {
	n := 0
	for _, entry := range rest {
		if restOnlySlugs[entry.Slug] {
			n++
		}
	}
	return n
}

// TestInlineValidationSlugsAreRESTSlugs: the slugs the tools raise for their
// own argument checks (no sentinel involved) must be slugs REST really uses.
func TestInlineValidationSlugsAreRESTSlugs(t *testing.T) {
	known := map[string]bool{}
	for _, entry := range inboundhttp.ErrorSlugs() {
		known[entry.Slug] = true
	}
	// invalid-report-query and internal-error are raised by REST handlers
	// directly (reports_handler.go / the 500 default), not through the table.
	known[slugInvalidReportQuery] = true
	known[slugInternalError] = true
	for _, slug := range []string{
		slugInvalidSiteCode, slugInvalidZoneCode, slugMissingLocationCode,
		slugInvalidReportQuery, slugInternalError,
	} {
		if !known[slug] {
			t.Errorf("inline slug %q is not a REST problem slug", slug)
		}
	}
}

func TestMapError(t *testing.T) {
	const generic = "internal-error: an unexpected internal error occurred"
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"not found", usecases.ErrSiteNotFound, "site-not-found: " + usecases.ErrSiteNotFound.Error()},
		{"zone not found", usecases.ErrZoneNotFound, "zone-not-found: " + usecases.ErrZoneNotFound.Error()},
		{"wrapped sentinel keeps its slug and detail", fmt.Errorf("loading: %w", usecases.ErrSiteNotFound), "site-not-found: loading: " + usecases.ErrSiteNotFound.Error()},
		{"unknown role", placement.ErrUnknownLocationRole, "unknown-location-role: " + placement.ErrUnknownLocationRole.Error()},
		{"malformed location code", shared.ErrMalformedLocationCode, "malformed-location-code: " + shared.ErrMalformedLocationCode.Error()},
		{"missing location code", slot.ErrMissingLocationCode, "missing-location-code: " + slot.ErrMissingLocationCode.Error()},
		{"no route between zones", usecases.ErrNoRouteBetweenZones, "no-route-between-zones: " + usecases.ErrNoRouteBetweenZones.Error()},
		{"concurrent modification", ports.ErrConcurrentModification, "concurrent-modification: " + ports.ErrConcurrentModification.Error()},
		{"already slugged tool error passes through", toolError("invalid-site-code", "siteCode is required"), "invalid-site-code: siteCode is required"},
		{"unmapped error becomes internal-error", errors.New("dial tcp 10.0.0.1:5432: connection refused"), generic},
		{"unmapped wrapped error does not leak", fmt.Errorf("pgx: %w", errors.New("password=hunter2")), generic},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mapError(tc.err)
			if got == nil || got.Error() != tc.want {
				t.Fatalf("mapError(%v) = %v, want %q", tc.err, got, tc.want)
			}
			if strings.Contains(got.Error(), "hunter2") || strings.Contains(got.Error(), "10.0.0.1") {
				t.Fatalf("internal detail leaked: %v", got)
			}
		})
	}
}

func TestMapErrorNilStaysNil(t *testing.T) {
	if got := mapError(nil); got != nil {
		t.Fatalf("mapError(nil) = %v, want nil", got)
	}
}

// TestHandlerInputErrorsCarrySlugs: the argument checks the handlers do
// themselves already speak the fleet convention, including the blank
// location code estimate_travel_distance rejects.
func TestHandlerInputErrorsCarrySlugs(t *testing.T) {
	h := newHarness(t)
	_, siteErr := h.deps.getSiteLayout(h.ctx(), siteLayoutInput{})
	_, gridErr := h.deps.getZoneGrid(h.ctx(), zoneGridInput{})
	_, funcErr := h.deps.listFunctionalLocations(h.ctx(), listFunctionalLocationsInput{Role: "Dock"})
	_, graphErr := h.deps.getZoneTravelGraph(h.ctx(), zoneTravelGraphInput{})
	_, fromErr := h.deps.estimateTravelDistance(h.ctx(), estimateTravelDistanceInput{To: "WH1-STOR-AMB-A07-02-01-A"})
	_, toErr := h.deps.estimateTravelDistance(h.ctx(), estimateTravelDistanceInput{From: "WH1-STOR-AMB-A07-01-01-A"})
	_, blankErr := h.deps.estimateTravelDistance(h.ctx(), estimateTravelDistanceInput{From: "   ", To: "WH1-STOR-AMB-A07-02-01-A"})

	tests := []struct {
		name string
		err  error
		slug string
	}{
		{"get_site_layout without siteCode", siteErr, "invalid-site-code"},
		{"get_zone_grid without zoneId", gridErr, "invalid-zone-code"},
		{"list_functional_locations without siteCode", funcErr, "invalid-site-code"},
		{"get_zone_travel_graph without zoneId", graphErr, "invalid-zone-code"},
		{"estimate_travel_distance without from", fromErr, "missing-location-code"},
		{"estimate_travel_distance without to", toErr, "missing-location-code"},
		{"estimate_travel_distance with blank from", blankErr, "missing-location-code"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.HasPrefix(tc.err.Error(), tc.slug+": ") {
				t.Fatalf("error %q does not start with %q", tc.err, tc.slug+": ")
			}
		})
	}
}

// noopReports is a ReportsClient that is never reached: the tool rejects the
// input before delegating.
type noopReports struct{}

func (noopReports) GetCatalogGrowth(context.Context, CatalogGrowthQuery) (CatalogGrowthReportView, error) {
	return CatalogGrowthReportView{}, nil
}

func (noopReports) GetFreshness(context.Context) (FreshnessView, error) {
	return FreshnessView{}, nil
}

func TestReportToolErrorSlugs(t *testing.T) {
	t.Run("missing window is invalid-report-query", func(t *testing.T) {
		_, err := GetCatalogGrowthReportForTest(t.Context(), noopReports{}, CatalogGrowthToolInput{})
		if err == nil || !strings.HasPrefix(err.Error(), "invalid-report-query: ") {
			t.Fatalf("got %v, want an invalid-report-query error", err)
		}
	})
	t.Run("unconfigured client is internal-error", func(t *testing.T) {
		_, err := GetCatalogGrowthReportForTest(t.Context(), nil, CatalogGrowthToolInput{From: "a", To: "b"})
		if err == nil || !strings.HasPrefix(err.Error(), "internal-error: ") {
			t.Fatalf("got %v, want an internal-error", err)
		}
	})
}
