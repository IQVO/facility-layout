package architecture

// Runtime-binary sensor (fleet decision 2026-10-17: contexts run only on real
// Postgres and real Kafka; memory adapters are test doubles). Adopted in the
// facility-layout real-wiring migration (ADR 0039) from the harness template.
//
//   - TestRuntimeBinaryHasNoInMemoryAdapters: no non-test file under cmd/**
//     may import internal/adapters/outbound/memory (or anything below it). The
//     in-memory adapters are TEST DOUBLES for unit and BDD tests; the shipped
//     binaries (cmd/facility, cmd/mcp, cmd/facility-projector,
//     cmd/facility-reports) run on real Postgres and real Kafka only. Without
//     this rule a cmd can fall back to them when DATABASE_URL is unset, start
//     no relay without KAFKA_BROKERS, so a misconfigured deployment comes up
//     "healthy" and silently loses every event.
//   - TestRuntimeBinarySensorBites proves the detector flags a plain, an
//     aliased, a blank and a dot import of the memory package and a nested
//     package of it, and passes the production adapters, a look-alike path and
//     a comment that merely mentions the package.
//
// cmd/ must hold at least one non-test Go file (a cmd/ directory with no
// binary is an error, so the sensor can never pass vacuously). The detector
// self-test always runs.

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// memoryAdapterSuffix is the import-path tail of the in-memory test doubles.
const memoryAdapterSuffix = "/internal/adapters/outbound/memory"

// isMemoryAdapterImport reports whether path is the memory adapter package or
// a package below it.
func isMemoryAdapterImport(path string) bool {
	return strings.HasSuffix(path, memoryAdapterSuffix) || strings.Contains(path, memoryAdapterSuffix+"/")
}

// memoryImports parses src as a Go file and returns one finding per import of
// the in-memory adapters ("file:line imports path").
func memoryImports(filename string, src any) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		if isMemoryAdapterImport(path) {
			out = append(out, fmt.Sprintf("%s imports %s", fset.Position(imp.Pos()), path))
		}
	}
	return out, nil
}

func runtimeBinaryViolation(finding string) string {
	return fmt.Sprintf("ARCHITECTURE VIOLATION (contents rule)\n"+
		"  RULE:    the shipped binary (cmd/**) never imports the in-memory adapters\n"+
		"  WHAT:    %s\n"+
		"  WHY:     fleet contexts run on real Postgres and real Kafka only. An in-memory runtime path "+
		"lets a misconfigured deployment boot \"healthy\" with no outbox relay and no idempotency, "+
		"and silently lose events.\n"+
		"  FIX:     wire the Postgres/Kafka adapters instead; if the code you need is a clock or an id generator it "+
		"lives in internal/adapters/outbound/system. The memory adapters stay in tests (unit and BDD). "+
		"Do NOT loosen this test.\n", finding)
}

func TestRuntimeBinaryHasNoInMemoryAdapters(t *testing.T) {
	cmdDir := filepath.Join("..", "..", "cmd")
	if _, err := os.Stat(cmdDir); err != nil {
		t.Fatalf("cmd/ must exist: %v", err)
	}
	files := goFilesUnder(t, cmdDir, false)
	if len(files) == 0 {
		t.Fatal("cmd/ exists but holds no non-test Go file: the sensor would pass vacuously")
	}
	sort.Strings(files)
	for _, path := range files {
		findings, err := memoryImports(path, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, finding := range findings {
			t.Errorf("%s", runtimeBinaryViolation(finding))
		}
	}
}

func TestRuntimeBinarySensorBites(t *testing.T) {
	const mem = `"example.com/svc/internal/adapters/outbound/memory"`
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"plain import is flagged", "package main\nimport " + mem + "\nvar _ = memory.NewSiteRepo", 1},
		{"aliased import is flagged", "package main\nimport mem " + mem, 1},
		{"blank import is flagged", "package main\nimport _ " + mem, 1},
		{"dot import is flagged", "package main\nimport . " + mem, 1},
		{"a package below memory is flagged", "package main\nimport \"example.com/svc/internal/adapters/outbound/memory/nested\"", 1},
		{"another module's memory adapter is flagged", "package main\nimport \"github.com/other/svc/internal/adapters/outbound/memory\"", 1},
		{"two imports are two findings", "package main\nimport (\n" + mem + "\nm2 \"github.com/other/svc/internal/adapters/outbound/memory\"\n)", 2},
		{"the production adapters pass", `package main
import (
	"example.com/svc/internal/adapters/outbound/postgres"
	"example.com/svc/internal/adapters/outbound/system"
)`, 0},
		{"a look-alike path passes", "package main\nimport \"example.com/svc/internal/adapters/outbound/memorystore\"", 0},
		{"a comment that names the package passes", "package main\n// the memory adapters (internal/adapters/outbound/memory) are test doubles\nimport \"fmt\"\nvar _ = fmt.Sprint", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := memoryImports("src.go", tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(got) != tc.want {
				t.Fatalf("findings = %v, want %d", got, tc.want)
			}
		})
	}
}
