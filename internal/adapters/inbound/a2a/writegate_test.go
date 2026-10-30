package a2a

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// readOnlyUseCaseSymbols are the only identifiers this adapter and its
// composition root may take from the usecases package (plus Err* sentinels).
// The write gate: a skill may change state ONLY if the context's MCP surface
// already offers that write, and facility-layout's MCP surface is read-only
// (ADR-0007), so no write use case may be reachable from the A2A agent.
var readOnlyUseCaseSymbols = map[string]bool{
	"GetSiteLayout":          true,
	"SiteLayout":             true,
	"EstimateTravelDistance": true,
	"TravelDistance":         true,
}

// usecaseSymbols returns every usecases.<Name> selector in src.
func usecaseSymbols(t *testing.T, filename string, src any) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "usecases" {
			out = append(out, sel.Sel.Name)
		}
		return true
	})
	return out
}

// writeUseCases returns the symbols that are neither allowed read symbols nor
// Err sentinels.
func writeUseCases(symbols []string) []string {
	var bad []string
	for _, s := range symbols {
		if !readOnlyUseCaseSymbols[s] && !strings.HasPrefix(s, "Err") {
			bad = append(bad, s)
		}
	}
	sort.Strings(bad)
	return bad
}

func nonTestGoFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no Go files under %s: the write gate would pass vacuously", dir)
	}
	return out
}

func TestAdapterAndCompositionRootReachNoWriteUseCase(t *testing.T) {
	var files []string
	files = append(files, nonTestGoFiles(t, ".")...)
	files = append(files, nonTestGoFiles(t, filepath.Join("..", "..", "..", "..", "cmd", "a2a"))...)
	for _, f := range files {
		if bad := writeUseCases(usecaseSymbols(t, f, nil)); len(bad) > 0 {
			t.Errorf("%s reaches use cases outside the read-only allow-list: %v (facility-layout's MCP surface offers no write, so neither may A2A)", f, bad)
		}
	}
}

func TestDepsHoldOnlyTheReadUseCases(t *testing.T) {
	data, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), "server.go", data, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var fields []string
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "Deps" {
			return true
		}
		for _, field := range ts.Type.(*ast.StructType).Fields.List {
			for _, name := range field.Names {
				fields = append(fields, name.Name)
			}
		}
		return false
	})
	sort.Strings(fields)
	if got := strings.Join(fields, ","); got != "EstimateTravelDistance,GetSiteLayout" {
		t.Fatalf("Deps fields = %s, want exactly the two read use cases", got)
	}
}

// TestWriteGateDetectorBites proves the detector flags write use cases and
// accepts the read ones, so the gate cannot pass vacuously.
func TestWriteGateDetectorBites(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"a write use case is flagged", "package x\nvar _ = usecases.RegisterSite{}", []string{"RegisterSite"}},
		{"a decommission is flagged", "package x\nvar _ = usecases.DecommissionSite{}", []string{"DecommissionSite"}},
		{"the read use cases pass", "package x\nvar _ = usecases.GetSiteLayout{}\nvar _ usecases.EstimateTravelDistance\nvar _ = usecases.ErrSiteNotFound", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := writeUseCases(usecaseSymbols(t, "src.go", tc.src))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("flagged %v, want %v", got, tc.want)
			}
		})
	}
}
