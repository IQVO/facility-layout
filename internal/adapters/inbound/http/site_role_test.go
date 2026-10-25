package http_test

import (
	"net/http"
	"testing"
)

type siteBody struct {
	SiteCode string `json:"siteCode"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	SiteRole string `json:"siteRole"`
}

type transferDefaultBody struct {
	SiteRole                   string `json:"siteRole"`
	TransferOriginEnabled      bool   `json:"transferOriginEnabled"`
	TransferDestinationEnabled bool   `json:"transferDestinationEnabled"`
}

func TestSiteRoleAtRegistration(t *testing.T) {
	t.Run("an omitted role registers the site as UNSPECIFIED", func(t *testing.T) {
		ts := newTestServer(t)
		var out siteBody
		ts.do(http.MethodPost, "/sites", map[string]any{"siteCode": "WH1", "name": "One"}).assertStatus(t, http.StatusCreated).decode(t, &out)
		if out.SiteRole != "UNSPECIFIED" {
			t.Fatalf("siteRole = %q", out.SiteRole)
		}
	})

	t.Run("a stated role is stored and read back on GET and list", func(t *testing.T) {
		ts := newTestServer(t)
		ts.do(http.MethodPost, "/sites", map[string]any{"siteCode": "ABE8", "name": "ABE8 - Non-Sortable Fulfillment Center (Breinigsville, PA)", "siteRole": "NON_SORTABLE_FC"}).
			assertStatus(t, http.StatusCreated)
		var one siteBody
		ts.do(http.MethodGet, "/sites/ABE8", nil).assertStatus(t, http.StatusOK).decode(t, &one)
		if one.SiteRole != "NON_SORTABLE_FC" {
			t.Fatalf("GET siteRole = %q", one.SiteRole)
		}
		var all []siteBody
		ts.do(http.MethodGet, "/sites", nil).assertStatus(t, http.StatusOK).decode(t, &all)
		if len(all) != 1 || all[0].SiteRole != "NON_SORTABLE_FC" {
			t.Fatalf("list = %+v", all)
		}
	})

	t.Run("an unknown role is 422 invalid-site-role", func(t *testing.T) {
		ts := newTestServer(t)
		ts.do(http.MethodPost, "/sites", map[string]any{"siteCode": "WH1", "name": "One", "siteRole": "Fulfillment Center"}).
			assertProblem(t, http.StatusUnprocessableEntity, "invalid-site-role")
		ts.do(http.MethodGet, "/sites/WH1", nil).assertProblem(t, http.StatusNotFound, "site-not-found")
	})
}

func TestChangeSiteRole(t *testing.T) {
	seed := func(ts *testServer) {
		ts.do(http.MethodPost, "/sites", map[string]any{"siteCode": "WH1", "name": "One"}).assertStatus(t, http.StatusCreated)
	}

	t.Run("PUT sets the role and answers with the site", func(t *testing.T) {
		ts := newTestServer(t)
		seed(ts)
		var out siteBody
		ts.do(http.MethodPut, "/sites/WH1/role", map[string]any{"siteRole": "MICRO_FC"}).assertStatus(t, http.StatusOK).decode(t, &out)
		if out != (siteBody{SiteCode: "WH1", Name: "One", Status: "Active", SiteRole: "MICRO_FC"}) {
			t.Fatalf("got %+v", out)
		}
		ts.do(http.MethodPut, "/sites/WH1/role", map[string]any{"siteRole": "MICRO_FC"}).assertStatus(t, http.StatusOK)
	})

	t.Run("a role change never flips the transfer capability", func(t *testing.T) {
		ts := newTestServer(t)
		seed(ts)
		ts.do(http.MethodPut, "/sites/WH1/role", map[string]any{"siteRole": "ROBOTICS_SORTABLE_FC"}).assertStatus(t, http.StatusOK)
		var capability capabilityBody
		ts.do(http.MethodGet, "/sites/WH1/transfer-capability", nil).assertStatus(t, http.StatusOK).decode(t, &capability)
		if capability != (capabilityBody{SiteCode: "WH1"}) {
			t.Fatalf("capability = %+v", capability)
		}
	})

	t.Run("UNSPECIFIED, unknown and missing roles are 422 invalid-site-role", func(t *testing.T) {
		ts := newTestServer(t)
		seed(ts)
		for _, body := range []map[string]any{{"siteRole": "UNSPECIFIED"}, {"siteRole": "WAREHOUSE"}, {}} {
			ts.do(http.MethodPut, "/sites/WH1/role", body).assertProblem(t, http.StatusUnprocessableEntity, "invalid-site-role")
		}
	})

	t.Run("404 for an unknown site, 400 for a malformed body", func(t *testing.T) {
		ts := newTestServer(t)
		ts.do(http.MethodPut, "/sites/WH9/role", map[string]any{"siteRole": "XL_FC"}).assertProblem(t, http.StatusNotFound, "site-not-found")
		seed(ts)
		ts.doRaw(http.MethodPut, "/sites/WH1/role", `{"siteRole":`).assertProblem(t, http.StatusBadRequest, "malformed-request-body")
	})
}

func TestSiteRoleTransferDefaults(t *testing.T) {
	ts := newTestServer(t)
	var out []transferDefaultBody
	ts.do(http.MethodGet, "/site-roles/transfer-defaults", nil).assertStatus(t, http.StatusOK).decode(t, &out)
	if len(out) != 15 {
		t.Fatalf("got %d rows, want 15: %+v", len(out), out)
	}
	want := map[string][2]bool{
		"ROBOTICS_SORTABLE_FC": {true, true}, "MICRO_FC": {false, true}, "INBOUND_CROSS_DOCK": {true, false},
		"RETURNS_CENTER": {true, false}, "FRESH_GROCERY_FC": {false, false}, "SIMULATED_FC": {true, true},
		"AIR_HUB_GATEWAY": {false, false},
	}
	for _, row := range out {
		if row.SiteRole == "UNSPECIFIED" {
			t.Fatal("UNSPECIFIED has no default and must not be listed")
		}
		if w, ok := want[row.SiteRole]; ok && (row.TransferOriginEnabled != w[0] || row.TransferDestinationEnabled != w[1]) {
			t.Errorf("%s: got origin=%v destination=%v", row.SiteRole, row.TransferOriginEnabled, row.TransferDestinationEnabled)
		}
	}
	if out[0].SiteRole != "ROBOTICS_SORTABLE_FC" || out[14].SiteRole != "AIR_HUB_GATEWAY" {
		t.Fatalf("order: first=%s last=%s", out[0].SiteRole, out[14].SiteRole)
	}
}
