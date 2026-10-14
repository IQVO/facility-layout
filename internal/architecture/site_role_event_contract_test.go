package architecture

import (
	"os"
	"strings"
	"testing"
)

// TestSiteRoleContractIsPublished pins the ADR-0037 AsyncAPI contract: the
// SiteRoleChanged message on the integration topic and the additive siteRole
// on SiteRegistered.
func TestSiteRoleContractIsPublished(t *testing.T) {
	contract, err := os.ReadFile("../../apis/asyncapi.yaml")
	if err != nil {
		t.Fatalf("read AsyncAPI contract: %v", err)
	}
	for _, want := range []string{
		"$ref: '#/components/messages/siteRoleChanged'",
		"const: com.warehouse.wms.facility-layout.site.SiteRoleChanged",
		"const: urn:warehouse:facility-layout:events:SiteRoleChanged:v1",
		"required: [siteCode, siteRole, previousSiteRole]",
		"previousSiteRole:",
		"siteRole:",
	} {
		if !strings.Contains(string(contract), want) {
			t.Errorf("AsyncAPI contract missing %q", want)
		}
	}
}
