package architecture

// Kafka writer acknowledgement sensor (fleet rollout 2026-10-13).
//
//   - TestKafkaWritersSetRequiredAcks: every kafka-go Writer composite literal
//     (kafkago.Writer{...} / kafka.Writer{...}, whatever the import alias) in a
//     non-test file under internal/ or cmd/ must set RequiredAcks. kafka-go's
//     Writer.RequiredAcks "Defaults to RequireNone" (acks=0): the broker never
//     confirms the write, so a broker blip or a leader move silently drops the
//     message (a domain event, or a poison message bound for the DLQ) while
//     WriteMessages still returns nil.
//   - TestKafkaWriterAcksSensorBites proves the sensor flags a literal without
//     the key under any import alias and accepts one that sets it.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const acksKafkaGoImport = "github.com/segmentio/kafka-go"

// acksKafkaAlias returns the local name under which f imports kafka-go
// ("" when it does not; "." for a dot import).
func acksKafkaAlias(f *ast.File) string {
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) != acksKafkaGoImport {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return "kafka"
	}
	return ""
}

// isKafkaWriterType reports whether typ names kafka-go's Writer under alias.
func isKafkaWriterType(typ ast.Expr, alias string) bool {
	if id, ok := typ.(*ast.Ident); ok {
		return alias == "." && id.Name == "Writer"
	}
	sel, ok := typ.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Writer" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == alias
}

// hasKey reports whether the composite literal sets the keyed field name.
func hasKey(cl *ast.CompositeLit, name string) bool {
	for _, e := range cl.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if k, ok := kv.Key.(*ast.Ident); ok && k.Name == name {
			return true
		}
	}
	return false
}

// writersWithoutRequiredAcks returns how many kafka-go Writer literals f
// builds and the file:line of each one that does not set RequiredAcks.
func writersWithoutRequiredAcks(fset *token.FileSet, f *ast.File) (writers int, missing []string) {
	alias := acksKafkaAlias(f)
	if alias == "" || alias == "_" {
		return 0, nil
	}
	ast.Inspect(f, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok || cl.Type == nil || !isKafkaWriterType(cl.Type, alias) {
			return true
		}
		writers++
		if !hasKey(cl, "RequiredAcks") {
			missing = append(missing, fset.Position(cl.Pos()).String())
		}
		return true
	})
	return writers, missing
}

// acksSourceFiles lists every non-test .go file under the given module-root dirs.
func acksSourceFiles(t *testing.T, dirs ...string) []string {
	t.Helper()
	var out []string
	for _, d := range dirs {
		root := filepath.Join("..", "..", d)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !e.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return out
}

func requiredAcksViolation(pos string) string {
	return fmt.Sprintf("ARCHITECTURE VIOLATION (contents rule)\n"+
		"  RULE:    every kafka-go Writer sets RequiredAcks explicitly\n"+
		"  WHAT:    %s: kafka-go Writer literal without a RequiredAcks key\n"+
		"  WHY:     kafka-go defaults Writer.RequiredAcks to RequireNone (acks=0): the broker never confirms the "+
		"write, so a broker blip or a leader move silently drops the message while WriteMessages returns nil.\n"+
		"  FIX:     set RequiredAcks: kafkago.RequireAll (or the package's own acks constant that equals it) and "+
		"keep the writer synchronous (no Async: true) so a failed write reaches the caller. Do NOT loosen this test.\n",
		pos)
}

func TestKafkaWritersSetRequiredAcks(t *testing.T) {
	total := 0
	for _, p := range acksSourceFiles(t, "internal", "cmd") {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		writers, missing := writersWithoutRequiredAcks(fset, f)
		total += writers
		for _, pos := range missing {
			t.Errorf("%s", requiredAcksViolation(pos))
		}
	}
	if total == 0 {
		t.Fatal("no kafka-go Writer literal found under internal/ or cmd/; this service writes to Kafka, " +
			"so the sensor is looking in the wrong place")
	}
}

func TestKafkaWriterAcksSensorBites(t *testing.T) {
	cases := []struct {
		name        string
		src         string
		wantWriters int
		wantMissing int
	}{
		{"aliased writer without acks is flagged", `package x
import kafkago "github.com/segmentio/kafka-go"
func f() any { return &kafkago.Writer{Addr: kafkago.TCP("b"), Topic: "t.dlq"} }`, 1, 1},
		{"unaliased writer without acks is flagged", `package x
import "github.com/segmentio/kafka-go"
func f() any { return kafka.Writer{Topic: "t"} }`, 1, 1},
		{"custom alias is resolved", `package x
import kg "github.com/segmentio/kafka-go"
func f() any { return &kg.Writer{Topic: "t", RequiredAcks: kg.RequireAll} }
func g() any { return &kg.Writer{Topic: "u"} }`, 2, 1},
		{"writer with acks passes", `package x
import kafkago "github.com/segmentio/kafka-go"
func f() any { return &kafkago.Writer{Topic: "t", RequiredAcks: syncWriterRequiredAcks} }`, 1, 0},
		{"a Writer from another package is ignored", `package x
import "bufio"
func f() any { return &bufio.Writer{} }`, 0, 0},
		{"a local kafka package without the kafka-go import is ignored", `package x
import "example.com/internal/kafka"
func f() any { return &kafka.Writer{} }`, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "src.go", tc.src, 0)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			writers, missing := writersWithoutRequiredAcks(fset, f)
			if writers != tc.wantWriters || len(missing) != tc.wantMissing {
				t.Fatalf("writers=%d missing=%v, want %d writer(s) and %d missing", writers, missing, tc.wantWriters, tc.wantMissing)
			}
		})
	}
}
