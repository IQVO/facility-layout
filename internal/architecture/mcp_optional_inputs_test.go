package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// optionalWordRE matches "optional" as a word in a jsonschema description.
var optionalWordRE = regexp.MustCompile(`(?i)\boptional\b`)

// mcpOptionalInputViolations returns every struct field in one Go source
// file whose `jsonschema` description calls the field optional while its
// `json` tag lacks `omitempty`. The MCP Go SDK infers a tool's input schema
// from the struct: a field without `omitempty` is emitted as REQUIRED, so a
// model that (correctly) leaves an "optional" filter out gets the call
// rejected by schema validation before the handler ever runs (gap fix D16).
func mcpOptionalInputViolations(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		if st, ok := n.(*ast.StructType); ok {
			for _, field := range st.Fields.List {
				if v := optionalFieldViolation(fset, field); v != "" {
					out = append(out, v)
				}
			}
		}
		return true
	})
	return out
}

// optionalFieldViolation reports one field, or "" when it complies.
func optionalFieldViolation(fset *token.FileSet, field *ast.Field) string {
	if field.Tag == nil {
		return ""
	}
	raw, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return ""
	}
	tag := reflect.StructTag(raw)
	schema, hasSchema := tag.Lookup("jsonschema")
	if !hasSchema || !optionalWordRE.MatchString(schema) {
		return ""
	}
	jsonTag, _ := tag.Lookup("json")
	if slicesContains(strings.Split(jsonTag, ",")[1:], "omitempty") {
		return ""
	}
	name := "<embedded>"
	if len(field.Names) > 0 {
		name = field.Names[0].Name
	}
	return fmt.Sprintf(
		"%s: MCP input field %s is described as optional (jsonschema %q) but its json tag %q has no omitempty, "+
			"so the SDK marks it REQUIRED and calls without it fail. FIX: add `,omitempty` to the json tag "+
			"(or stop calling it optional if the handler really requires it).",
		fset.Position(field.Pos()), name, schema, jsonTag)
}

func slicesContains(items []string, want string) bool {
	for _, it := range items {
		if it == want {
			return true
		}
	}
	return false
}

// TestMCPOptionalToolInputsCarryOmitempty enforces gap fix D16 on every
// non-test file of the MCP inbound adapter.
func TestMCPOptionalToolInputsCarryOmitempty(t *testing.T) {
	files := goFilesUnder(t, "../adapters/inbound/mcp", false)
	if len(files) == 0 {
		t.Fatal("no MCP adapter sources found; the sensor is looking in the wrong place")
	}
	for _, path := range files {
		for _, v := range mcpOptionalInputViolations(t, path) {
			t.Error(v)
		}
	}
}

// TestMCPOptionalToolInputsSensorFailsOnBadFixtures proves the sensor can
// fail, and does not flag required fields or already-correct ones.
func TestMCPOptionalToolInputsSensorFailsOnBadFixtures(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"optional without omitempty", "package x\n\ntype In struct {\n\tScope string `json:\"scope\" jsonschema:\"optional scope filter\"`\n}\n", true},
		{"Optional capitalised", "package x\n\ntype In struct {\n\tScope string `json:\"scope\" jsonschema:\"Optional scope filter\"`\n}\n", true},
		{"optional with omitempty", "package x\n\ntype In struct {\n\tScope string `json:\"scope,omitempty\" jsonschema:\"optional scope filter\"`\n}\n", false},
		{"required field", "package x\n\ntype In struct {\n\tFrom string `json:\"from\" jsonschema:\"start of the window (required)\"`\n}\n", false},
		{"optionally is not optional", "package x\n\ntype In struct {\n\tFrom string `json:\"from\" jsonschema:\"a code, optionally prefixed\"`\n}\n", false},
		{"no jsonschema tag", "package x\n\ntype Out struct {\n\tScope string `json:\"scope\"`\n}\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "fixture.go")
			if err := os.WriteFile(p, []byte(tc.src), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			got := mcpOptionalInputViolations(t, p)
			if tc.wantErr && len(got) == 0 {
				t.Fatal("sensor did not flag the bad fixture")
			}
			if !tc.wantErr && len(got) != 0 {
				t.Fatalf("compliant fixture reported violations: %v", got)
			}
		})
	}
}
