package http_test

import (
	"net/http"
	"testing"
)

type capabilityBody struct {
	SiteCode                   string `json:"siteCode"`
	TransferOriginEnabled      bool   `json:"transferOriginEnabled"`
	TransferDestinationEnabled bool   `json:"transferDestinationEnabled"`
	CapabilityRevision         int64  `json:"capabilityRevision"`
}

func TestSiteTransferCapability(t *testing.T) {
	seedSite := func(ts *testServer) {
		ts.do(http.MethodPost, "/sites", map[string]any{"siteCode": "WH1", "name": "Fulfilment Centre One"}).assertStatus(t, http.StatusCreated)
	}

	t.Run("a registered site starts capable of nothing at revision 0", func(t *testing.T) {
		ts := newTestServer(t)
		seedSite(ts)
		var out capabilityBody
		ts.do(http.MethodGet, "/sites/WH1/transfer-capability", nil).assertStatus(t, http.StatusOK).decode(t, &out)
		if out != (capabilityBody{SiteCode: "WH1"}) {
			t.Fatalf("got %+v", out)
		}
	})

	t.Run("PUT replaces the snapshot and advances the revision once per real change", func(t *testing.T) {
		ts := newTestServer(t)
		seedSite(ts)
		body := map[string]any{"transferOriginEnabled": true, "transferDestinationEnabled": false}

		var out capabilityBody
		ts.do(http.MethodPut, "/sites/WH1/transfer-capability", body).assertStatus(t, http.StatusOK).decode(t, &out)
		if out != (capabilityBody{SiteCode: "WH1", TransferOriginEnabled: true, CapabilityRevision: 1}) {
			t.Fatalf("first PUT: %+v", out)
		}

		ts.do(http.MethodPut, "/sites/WH1/transfer-capability", body).assertStatus(t, http.StatusOK).decode(t, &out)
		if out.CapabilityRevision != 1 {
			t.Fatalf("a repeated PUT is idempotent: revision %d, want 1", out.CapabilityRevision)
		}

		body["transferDestinationEnabled"] = true
		ts.do(http.MethodPut, "/sites/WH1/transfer-capability", body).assertStatus(t, http.StatusOK).decode(t, &out)
		if out != (capabilityBody{SiteCode: "WH1", TransferOriginEnabled: true, TransferDestinationEnabled: true, CapabilityRevision: 2}) {
			t.Fatalf("second change: %+v", out)
		}

		ts.do(http.MethodGet, "/sites/WH1/transfer-capability", nil).assertStatus(t, http.StatusOK).decode(t, &out)
		if out.CapabilityRevision != 2 || !out.TransferDestinationEnabled {
			t.Fatalf("GET after PUT: %+v", out)
		}
	})

	t.Run("404 for an unknown site on both verbs", func(t *testing.T) {
		ts := newTestServer(t)
		ts.do(http.MethodGet, "/sites/WH9/transfer-capability", nil).assertProblem(t, http.StatusNotFound, "site-not-found")
		ts.do(http.MethodPut, "/sites/WH9/transfer-capability",
			map[string]any{"transferOriginEnabled": true, "transferDestinationEnabled": true}).
			assertProblem(t, http.StatusNotFound, "site-not-found")
	})

	t.Run("400 when a field is omitted or null, never a silent false", func(t *testing.T) {
		ts := newTestServer(t)
		seedSite(ts)
		for _, body := range []map[string]any{
			{"transferOriginEnabled": true},
			{"transferDestinationEnabled": true},
			{"transferOriginEnabled": nil, "transferDestinationEnabled": true},
			{},
		} {
			ts.do(http.MethodPut, "/sites/WH1/transfer-capability", body).
				assertProblem(t, http.StatusBadRequest, "missing-transfer-capability-field")
		}
	})

	t.Run("400 for a non-boolean field", func(t *testing.T) {
		ts := newTestServer(t)
		seedSite(ts)
		ts.do(http.MethodPut, "/sites/WH1/transfer-capability",
			map[string]any{"transferOriginEnabled": "yes", "transferDestinationEnabled": true}).
			assertProblem(t, http.StatusBadRequest, "malformed-request-body")
	})
}
