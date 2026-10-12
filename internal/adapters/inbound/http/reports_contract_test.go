package http_test

import (
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	"github.com/claudioed/facility-layout/internal/adapters/inbound/http"
	"github.com/claudioed/facility-layout/internal/analytics/report"
)

// The facility-reports binary needs an analytical Postgres to boot, so the
// Schemathesis `contract` job (which boots cmd/facility in-memory) cannot
// reach it. This file is the reports API's contract test instead (gap fix
// D18): it holds apis/openapi-reports.yaml and NewReportsRouter to each
// other in both directions -- every route is documented and every
// documented operation is routed -- and checks that each documented
// response status the handlers can produce returns a body whose keys are
// exactly the schema's properties, with every required key present.

type oaSchema struct {
	Ref        string              `yaml:"$ref"`
	Type       string              `yaml:"type"`
	Required   []string            `yaml:"required"`
	Properties map[string]oaSchema `yaml:"properties"`
	Items      *oaSchema           `yaml:"items"`
}

type oaMedia struct {
	Schema oaSchema `yaml:"schema"`
}

type oaResponse struct {
	Content map[string]oaMedia `yaml:"content"`
}

type oaOperation struct {
	OperationID string                `yaml:"operationId"`
	Responses   map[string]oaResponse `yaml:"responses"`
}

type oaSpec struct {
	Paths      map[string]map[string]oaOperation `yaml:"paths"`
	Components struct {
		Schemas map[string]oaSchema `yaml:"schemas"`
	} `yaml:"components"`
}

func loadReportsSpec(t *testing.T) oaSpec {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(here), "..", "..", "..", "..", "apis", "openapi-reports.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var spec oaSpec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return spec
}

func (s oaSpec) resolve(t *testing.T, sc oaSchema) oaSchema {
	t.Helper()
	if sc.Ref == "" {
		return sc
	}
	name := strings.TrimPrefix(sc.Ref, "#/components/schemas/")
	resolved, ok := s.Components.Schemas[name]
	if !ok {
		t.Fatalf("unresolved $ref %q", sc.Ref)
	}
	return resolved
}

// assertConforms checks body against schema: an object's keys are exactly
// the declared properties (all required ones present), recursively through
// arrays of objects.
func (s oaSpec) assertConforms(t *testing.T, where string, sc oaSchema, body any) {
	t.Helper()
	sc = s.resolve(t, sc)
	switch sc.Type {
	case "object":
		obj, ok := body.(map[string]any)
		if !ok {
			t.Errorf("%s: want object, got %T", where, body)
			return
		}
		for _, req := range sc.Required {
			if _, ok := obj[req]; !ok {
				t.Errorf("%s: required property %q missing", where, req)
			}
		}
		for key, val := range obj {
			prop, ok := sc.Properties[key]
			if !ok {
				t.Errorf("%s: undocumented property %q", where, key)
				continue
			}
			s.assertConforms(t, where+"."+key, prop, val)
		}
	case "array":
		arr, ok := body.([]any)
		if !ok {
			t.Errorf("%s: want array, got %T", where, body)
			return
		}
		if sc.Items != nil {
			for _, item := range arr {
				s.assertConforms(t, where+"[]", *sc.Items, item)
			}
		}
	case "string":
		if _, ok := body.(string); !ok {
			t.Errorf("%s: want string, got %T", where, body)
		}
	case "integer", "number":
		if _, ok := body.(float64); !ok {
			t.Errorf("%s: want number, got %T", where, body)
		}
	}
}

func TestReportsSpecAndRouterAgree(t *testing.T) {
	spec := loadReportsSpec(t)

	var documented []string
	for path, ops := range spec.Paths {
		for method := range ops {
			documented = append(documented, strings.ToUpper(method)+" "+path)
		}
	}

	router := http.NewReportsRouter(&http.ReportsHandlers{Store: &fakeReportStore{}}, nil)
	var routed []string
	if err := chi.Walk(router, func(method, route string, _ stdhttp.Handler, _ ...func(stdhttp.Handler) stdhttp.Handler) error {
		routed = append(routed, method+" "+route)
		return nil
	}); err != nil {
		t.Fatalf("walk routes: %v", err)
	}

	sort.Strings(documented)
	sort.Strings(routed)
	if !slices.Equal(documented, routed) {
		t.Fatalf("apis/openapi-reports.yaml and NewReportsRouter disagree\n documented: %v\n routed:     %v", documented, routed)
	}
}

func TestReportsResponsesConformToSpec(t *testing.T) {
	spec := loadReportsSpec(t)
	day := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	populated := &fakeReportStore{
		report: report.CatalogReport{Rows: []report.Row{
			{Key: report.RowKey{Scope: "WH1", DayBucket: day}, SitesRegistered: 1, ZonesRegistered: 2},
			{Key: report.RowKey{Scope: "", DayBucket: day}, BulkImports: 1, ImportRowsSubmitted: 3, ImportRowsImported: 2, ImportRowsRejected: 1},
		}},
		lag: 4200 * time.Millisecond,
	}
	failing := &fakeReportStore{queryErr: errors.New("boom"), freshErr: errors.New("boom")}
	notReady := &http.Readiness{}
	notReady.SetNotReady()

	cases := []struct {
		name, path, specPath, status string
		handlers                     *http.ReportsHandlers
	}{
		{"healthz", "/healthz", "/healthz", "200", &http.ReportsHandlers{Store: populated}},
		{"readyz ready", "/readyz", "/readyz", "200", &http.ReportsHandlers{Store: populated}},
		{"readyz draining", "/readyz", "/readyz", "503", &http.ReportsHandlers{Store: populated, Readiness: notReady}},
		{"report rows", "/reports/catalog-growth?from=2026-03-01T00:00:00Z&to=2026-03-08T00:00:00Z&scope=WH1&granularity=day", "/reports/catalog-growth", "200", &http.ReportsHandlers{Store: populated}},
		{"report empty", "/reports/catalog-growth?from=2026-03-01T00:00:00Z&to=2026-03-08T00:00:00Z", "/reports/catalog-growth", "200", &http.ReportsHandlers{Store: &fakeReportStore{}}},
		{"report missing from", "/reports/catalog-growth?to=2026-03-08T00:00:00Z", "/reports/catalog-growth", "400", &http.ReportsHandlers{Store: populated}},
		{"report bad granularity", "/reports/catalog-growth?from=2026-03-01T00:00:00Z&to=2026-03-08T00:00:00Z&granularity=hour", "/reports/catalog-growth", "400", &http.ReportsHandlers{Store: populated}},
		{"report store down", "/reports/catalog-growth?from=2026-03-01T00:00:00Z&to=2026-03-08T00:00:00Z", "/reports/catalog-growth", "500", &http.ReportsHandlers{Store: failing}},
		{"freshness", "/reports/catalog-growth/freshness", "/reports/catalog-growth/freshness", "200", &http.ReportsHandlers{Store: populated}},
		{"freshness store down", "/reports/catalog-growth/freshness", "/reports/catalog-growth/freshness", "500", &http.ReportsHandlers{Store: failing}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			op, ok := spec.Paths[tc.specPath]["get"]
			if !ok {
				t.Fatalf("GET %s not documented", tc.specPath)
			}
			resp, ok := op.Responses[tc.status]
			if !ok {
				t.Fatalf("GET %s does not document status %s", tc.specPath, tc.status)
			}

			rec := httptest.NewRecorder()
			http.NewReportsRouter(tc.handlers, nil).ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, tc.path, nil))

			if got := rec.Code; got != mustAtoi(t, tc.status) {
				t.Fatalf("status = %d, want %s; body %s", got, tc.status, rec.Body.String())
			}
			contentType := rec.Header().Get("Content-Type")
			media, ok := resp.Content[contentType]
			if !ok {
				t.Fatalf("Content-Type %q not documented for GET %s %s (documented: %v)", contentType, tc.specPath, tc.status, keys(resp.Content))
			}
			var body any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v; body %s", err, rec.Body.String())
			}
			spec.assertConforms(t, "body", media.Schema, body)
		})
	}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Fatalf("bad status %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func keys(m map[string]oaMedia) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
