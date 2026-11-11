package a2a

import (
	"testing"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

// TestSiteLayoutSkillCarriesTheOptionalGLNs checks the site_layout answer
// mirrors the MCP read model: the site's and the zone's GS1 Global Location
// Number (ADR-0045) when recorded.
func TestSiteLayoutSkillCarriesTheOptionalGLNs(t *testing.T) {
	h := newHarness(t)
	glns := memory.NewGLNRegistry()
	h.registerSite.GLNs, h.registerZone.GLNs = glns, glns

	if _, err := h.registerSite.ExecuteWithGLN(h.ctx(), "WH1", "One", site.RoleUnspecified, "4006381333931"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.registerZone.ExecuteWithGLN(h.ctx(), "WH1", "YARD", "NORTH", shared.Ambient, false, nil, nil, "5412345000013"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.registerZone.Execute(h.ctx(), "WH1", "STOR", "AMB", shared.Ambient, false, nil, nil); err != nil {
		t.Fatal(err)
	}

	layout, err := h.deps.GetSiteLayout.Execute(h.ctx(), "WH1")
	if err != nil {
		t.Fatal(err)
	}
	dto := toSiteLayoutDTO(layout)
	if dto.Site.GLN != "4006381333931" {
		t.Fatalf("site GLN = %q", dto.Site.GLN)
	}
	byID := map[string]string{}
	for _, z := range dto.Zones {
		byID[z.ZoneID] = z.GLN
	}
	if byID["WH1-YARD-NORTH"] != "5412345000013" || byID["WH1-STOR-AMB"] != "" {
		t.Fatalf("zone GLNs = %v", byID)
	}
}
