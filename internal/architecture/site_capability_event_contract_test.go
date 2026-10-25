package architecture

import (
	"os"
	"strings"
	"testing"
)

func TestSiteCapabilityChangedContractPublishesFullLWWState(t *testing.T) {
	contract, err := os.ReadFile("../../apis/asyncapi.yaml")
	if err != nil {
		t.Fatalf("read AsyncAPI contract: %v", err)
	}

	for _, want := range []string{
		"$ref: '#/components/messages/siteCapabilityChanged'",
		"const: com.warehouse.wms.facility-layout.site.SiteCapabilityChanged",
		"const: urn:warehouse:facility-layout:events:SiteCapabilityChanged:v1",
		"required: [site_code, transfer_origin_enabled, transfer_destination_enabled, capability_revision]",
		"site_code:",
		"transfer_origin_enabled:",
		"transfer_destination_enabled:",
		"capability_revision:",
	} {
		if !strings.Contains(string(contract), want) {
			t.Errorf("AsyncAPI contract missing %q", want)
		}
	}
}
