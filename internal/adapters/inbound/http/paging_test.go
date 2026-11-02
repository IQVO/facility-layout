package http_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

// walkPages follows nextCursor from the first page until it is absent and
// returns every item's id in the order served.
func walkPages(t *testing.T, ts *testServer, path, idField string, limit int) []string {
	t.Helper()
	var ids []string
	cursor := ""
	for range 1000 {
		q := url.Values{"limit": {fmt.Sprint(limit)}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var body struct {
			Items      []map[string]any `json:"items"`
			NextCursor string           `json:"nextCursor"`
		}
		ts.do(http.MethodGet, path+"?"+q.Encode(), nil).assertStatus(t, http.StatusOK).decode(t, &body)
		if len(body.Items) > limit {
			t.Fatalf("page of %d items exceeds limit %d", len(body.Items), limit)
		}
		for _, it := range body.Items {
			ids = append(ids, fmt.Sprint(it[idField]))
		}
		if body.NextCursor == "" {
			return ids
		}
		cursor = body.NextCursor
	}
	t.Fatal("pagination did not terminate")
	return nil
}

func assertCompleteOnce(t *testing.T, got []string, want map[string]bool) {
	t.Helper()
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("row %s served twice: %v", id, got)
		}
		seen[id] = true
	}
	for id := range want {
		if !seen[id] {
			t.Fatalf("row %s skipped: %v", id, got)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("served %d rows, want %d: %v", len(got), len(want), got)
	}
}

func TestKeysetPaginationWalksEveryRowOnce(t *testing.T) {
	t.Run("aisles sharing a sequence hint", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		ts.seedZone("STOR", "AMB", "Ambient", false)
		want := map[string]bool{}
		for i := range 7 {
			code := fmt.Sprintf("A%02d", i+1)
			ts.seedAisle("WH1-STOR-AMB", code, i/3, "TwoWay") // three rows per hint
			want["WH1-STOR-AMB-"+code] = true
		}
		got := walkPages(t, ts, "/zones/WH1-STOR-AMB/aisles", "aisleId", 2)
		assertCompleteOnce(t, got, want)
		if got[0] != "WH1-STOR-AMB-A01" || got[len(got)-1] != "WH1-STOR-AMB-A07" {
			t.Fatalf("aisles not in walk order: %v", got)
		}
	})

	t.Run("site role transfer defaults", func(t *testing.T) {
		ts := newTestServer(t)
		var all struct {
			Items []map[string]any `json:"items"`
		}
		ts.do(http.MethodGet, "/site-roles/transfer-defaults?limit=500", nil).assertStatus(t, http.StatusOK).decode(t, &all)
		want := map[string]bool{}
		for _, it := range all.Items {
			want[fmt.Sprint(it["siteRole"])] = true
		}
		if len(want) < 4 {
			t.Fatalf("need more rows than the limit, got %d", len(want))
		}
		assertCompleteOnce(t, walkPages(t, ts, "/site-roles/transfer-defaults", "siteRole", 3), want)
	})
}

func TestPagedCollectionsRejectBadPagingParameters(t *testing.T) {
	paths := []string{
		"/sites", "/site-roles/transfer-defaults", "/sites/WH1/zones", "/sites/WH1/structures",
		"/sites/WH1/locations?role=Storage", "/zones/WH1-STOR-AMB/aisles", "/zones/WH1-STOR-AMB/cross-aisles",
		"/location-types", "/placement-rules",
	}
	cases := []struct{ query, slug string }{
		{"limit=0", "invalid-limit"},
		{"limit=501", "invalid-limit"},
		{"limit=abc", "invalid-limit"},
		{"cursor=%21%21not-a-cursor", "invalid-cursor"},
		{"after=WH1", "unknown-query-parameter"},
		{"offset=10", "unknown-query-parameter"},
		{"nextToken=abc", "unknown-query-parameter"},
	}
	for _, p := range paths {
		for _, c := range cases {
			t.Run(p+" "+c.query, func(t *testing.T) {
				ts := newTestServer(t)
				ts.seedSite()
				ts.seedZone("STOR", "AMB", "Ambient", false)
				sep := "?"
				if u, _ := url.Parse(p); u.RawQuery != "" {
					sep = "&"
				}
				ts.do(http.MethodGet, p+sep+c.query, nil).assertProblem(t, http.StatusBadRequest, c.slug)
			})
		}
	}
}
