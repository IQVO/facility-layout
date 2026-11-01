package http_test

import (
	"net/http"
	"testing"
)

// TestRetiredRoutesAreGone pins the REST guideline hard cutover: the verb
// routes replaced by noun routes answer 404 or 405, never an alias.
func TestRetiredRoutesAreGone(t *testing.T) {
	retired := []struct {
		method, path string
	}{
		{http.MethodPost, "/locations/import"},
		{http.MethodPost, "/locations/WH1-STOR-AMB-A01-01-01-A/decommission"},
	}
	for _, rt := range retired {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			res := newTestServer(t).do(rt.method, rt.path, map[string]any{})
			if res.status != http.StatusNotFound && res.status != http.StatusMethodNotAllowed {
				t.Fatalf("retired route answered %d, want 404 or 405", res.status)
			}
		})
	}
}
