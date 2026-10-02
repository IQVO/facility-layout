package kafka_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestEverySyncWriterSetsBatchTimeout is a source-level fitness check: every
// kafka-go Writer literal in this package must set BatchTimeout (see
// syncWriterBatchTimeout). kafka-go's 1s default silently caps a
// synchronous, one-message-per-call writer -- the outbox relay -- at ~1
// event/s, and nothing else in the test suite notices.
func TestEverySyncWriterSetsBatchTimeout(t *testing.T) {
	fset := token.NewFileSet()
	found := 0
	for _, name := range sourceFiles(t) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, lit := range writerLiterals(f) {
			found++
			if !setsField(lit, "BatchTimeout") {
				t.Errorf("%s: kafka Writer literal without BatchTimeout", fset.Position(lit.Pos()))
			}
		}
	}
	if found == 0 {
		t.Fatal("found no kafka Writer literals; the fitness check is not looking at the right package")
	}
}

// sourceFiles lists this package's non-test Go files.
func sourceFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			out = append(out, n)
		}
	}
	return out
}

// writerLiterals returns every `<pkg>.Writer{...}` composite literal in f.
func writerLiterals(f *ast.File) []*ast.CompositeLit {
	var out []*ast.CompositeLit
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.CompositeLit); ok {
			if sel, ok := lit.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Writer" {
				out = append(out, lit)
			}
		}
		return true
	})
	return out
}

// setsField reports whether lit has a keyed element named field.
func setsField(lit *ast.CompositeLit, field string) bool {
	for _, el := range lit.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == field {
				return true
			}
		}
	}
	return false
}
