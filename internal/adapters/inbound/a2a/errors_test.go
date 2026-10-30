package a2a

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	inboundhttp "github.com/claudioed/facility-layout/internal/adapters/inbound/http"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// restOnly are the REST-body-decoding sentinels (unexported there) that no
// skill raises; they are the only REST entries the A2A table does not carry.
var restOnlySlugs = map[string]bool{
	"missing-geometry-field":            true,
	"missing-sequence-hint":             true,
	"missing-transfer-capability-field": true,
}

// TestErrorTableMatchesRESTOneForOne is the drift sensor behind "the SAME
// status classification as the REST adapter": every REST entry (minus the
// body-decoding ones) must exist here with the same slug AND status, and the
// A2A table must hold nothing REST lacks.
func TestErrorTableMatchesRESTOneForOne(t *testing.T) {
	rest := inboundhttp.ErrorSlugs()
	if len(rest) == 0 {
		t.Fatal("REST adapter exposes no error slugs")
	}
	mappable := 0
	for _, entry := range rest {
		if entry.Err != nil && restOnlySlugs[entry.Slug] {
			continue
		}
		mappable++
		got, ok := categoryFor(entry.Err)
		if !ok {
			t.Errorf("REST maps %v to %d %q but the A2A table has no entry for it", entry.Err, entry.Status, entry.Slug)
			continue
		}
		if got.problem.slug != entry.Slug || got.status != entry.Status {
			t.Errorf("%v: REST %d %q, A2A %d %q", entry.Err, entry.Status, entry.Slug, got.status, got.problem.slug)
		}
	}
	if len(errorCategories) != mappable {
		t.Errorf("A2A table has %d entries, REST has %d mappable entries", len(errorCategories), mappable)
	}
}

// TestArgumentSlugsExistInTheRESTTable pins that the adapter's own argument
// checks reuse slugs REST reports, with the REST status.
func TestArgumentSlugsExistInTheRESTTable(t *testing.T) {
	for slug, wantStatus := range map[string]int{
		slugInvalidSiteCode:     http.StatusBadRequest,
		slugMissingLocationCode: http.StatusBadRequest,
	} {
		c, ok := categoryForSlug(slug)
		if !ok {
			t.Errorf("slug %q is not in the REST-derived table", slug)
			continue
		}
		if c.status != wantStatus {
			t.Errorf("slug %q status = %d, want %d", slug, c.status, wantStatus)
		}
	}
}

// TestProblemFromUseCaseClassification pins the status classes REST uses for
// the conditions the two read skills can actually raise.
func TestProblemFromUseCaseClassification(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		slug   string
	}{
		{"site missing", usecases.ErrSiteNotFound, 404, "site-not-found"},
		{"slot missing", usecases.ErrLocationSlotNotFound, 404, "location-slot-not-found"},
		{"different zones", usecases.ErrNoRouteBetweenZones, 422, "no-route-between-zones"},
		{"malformed code", shared.ErrMalformedLocationCode, 400, "malformed-location-code"},
		{"wrapped sentinel still maps", fmt.Errorf("lookup: %w", usecases.ErrSiteNotFound), 404, "site-not-found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := problemFromUseCase(tc.err)
			if p.Status != tc.status || p.Type != problemTypeBase+"/"+tc.slug {
				t.Fatalf("problem = %+v, want %d %s", p, tc.status, tc.slug)
			}
		})
	}
}

// TestUnmappedErrorIsAnInternalProblemWithoutItsDetail proves an infrastructure
// failure is answered as a 500 problem that withholds the cause (DSNs, SQL).
func TestUnmappedErrorIsAnInternalProblemWithoutItsDetail(t *testing.T) {
	p := problemFromUseCase(errors.New("dial tcp 10.0.0.1:5432: password=hunter2"))
	if p.Status != http.StatusInternalServerError || p.Type != problemTypeBase+"/internal-error" {
		t.Fatalf("problem = %+v, want 500 internal-error", p)
	}
	if p.Detail != internalErrorDetail {
		t.Fatalf("detail = %q, want the generic %q", p.Detail, internalErrorDetail)
	}
}
