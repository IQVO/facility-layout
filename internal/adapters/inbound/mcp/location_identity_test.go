package mcp

import (
	"encoding/json"
	"testing"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

// TestToolsReturnTheOptionalLocationIdentity seeds a site, a zone and a yard
// spot carrying a GLN / spotKind (ADR-0045) and checks the read tools return
// them, while a location without them keeps the old compact shape.
func TestToolsReturnTheOptionalLocationIdentity(t *testing.T) {
	h := seedIdentitySite(t)

	sites, err := h.deps.listSites(h.ctx(), listSitesInput{})
	if err != nil || len(sites.Sites) != 1 || sites.Sites[0].GLN != "4006381333931" {
		t.Fatalf("list_sites = %+v, err %v", sites.Sites, err)
	}

	layout, err := h.deps.getSiteLayout(h.ctx(), siteLayoutInput{SiteCode: "WH1"})
	if err != nil {
		t.Fatal(err)
	}
	if layout.Site.GLN != "4006381333931" || len(layout.Zones) != 1 || layout.Zones[0].GLN != "5412345000013" {
		t.Fatalf("get_site_layout identity = site %q zones %+v", layout.Site.GLN, layout.Zones)
	}

	yard, err := h.deps.listFunctionalLocations(h.ctx(), listFunctionalLocationsInput{SiteCode: "WH1", Role: "Yard"})
	if err != nil || len(yard.Locations) != 2 {
		t.Fatalf("list_functional_locations = %+v, err %v", yard.Locations, err)
	}
	with, without := yard.Locations[0], yard.Locations[1]
	if with.GLN != "4012345000009" || with.SpotKind != "REEFER_PLUG" {
		t.Fatalf("yard spot identity = %q / %q", with.GLN, with.SpotKind)
	}
	assertOmitsIdentity(t, without)
}

// assertOmitsIdentity checks a location without identity keeps the old
// compact shape: neither gln nor spotKind appears in its JSON.
func assertOmitsIdentity(t *testing.T, loc functionalLocationDTO) {
	t.Helper()
	raw, err := json.Marshal(loc)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]any
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"gln", "spotKind"} {
		if _, present := shape[field]; present {
			t.Fatalf("%s must be omitted when none was recorded: %s", field, raw)
		}
	}
}

// seedIdentitySite registers site WH1 (with a GLN), its YARD/NORTH zone (with
// a GLN) and two yard spots: one with a GLN and a spot kind, one without.
func seedIdentitySite(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	glns := memory.NewGLNRegistry()
	h.registerSite.GLNs, h.registerZone.GLNs, h.registerSlot.GLNs = glns, glns, glns

	if _, err := h.registerSite.ExecuteWithGLN(h.ctx(), "WH1", "One", site.RoleUnspecified, "4006381333931"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.registerZone.ExecuteWithGLN(h.ctx(), "WH1", "YARD", "NORTH", shared.Ambient, false, nil, nil, "5412345000013"); err != nil {
		t.Fatal(err)
	}
	h.mustRegisterAisle("WH1-YARD-NORTH", "Y01", 1, shared.TwoWay)
	h.mustRegisterDockLocationType("YardSpot", placement.Yard)

	register := func(code string, opts usecases.SlotOptions) {
		t.Helper()
		parsed, err := shared.ParseLocationCode(code)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.registerSlot.ExecuteWithOptions(h.ctx(), parsed, "YardSpot", shared.Capacity{}, "", nil, opts); err != nil {
			t.Fatalf("register %s: %v", code, err)
		}
	}
	register("WH1-YARD-NORTH-Y01-01-01-A", usecases.SlotOptions{GLN: "4012345000009", SpotKind: "REEFER_PLUG"})
	register("WH1-YARD-NORTH-Y01-01-02-A", usecases.SlotOptions{})

	return h
}
