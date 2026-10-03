package architecture

// Fleet event fitness tests (harness v3, phase 4.2). MANAGED by warehouse-harness-template
// (tools/migrate_v3.py): do not edit in a service repo. They parse the module with go/parser, so
// they are independent of the repo's module path and need no extra dependency.
//
//   - TestCloudEventsOnly: CloudEvents 1.0 is the ONLY envelope. No EVENT_ENVELOPE_MODE toggle
//     anywhere, and a module that talks to Kafka must build its envelopes with the CloudEvents SDK
//     (at least one non-test file imports github.com/cloudevents/sdk-go). Limits: this cannot prove
//     every message goes through that helper (outbox relays forward pre-encoded messages), it
//     catches the module-level regressions: the toggle returning or the SDK being dropped.
//   - TestReplayConsumersSetCommitInterval: a kafka-go ReaderConfig whose GroupID is a call like
//     uniqueConsumerGroup() (a process-unique group: the full-replay cache consumers) must set CommitInterval, else kafka-go commits synchronously after EVERY
//     message and boot replay becomes O(history) x RTT (order-management hit a CrashLoopBackOff).
//     Consumers on a fixed shared group commit per message on purpose and are not flagged.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const kafkaGoImport = "github.com/segmentio/kafka-go"

// goFiles returns non-test, non-generated .go files under dirs (relative to the module root).
func goFiles(t *testing.T, dirs ...string) []string {
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
			n := e.Name()
			if !e.IsDir() && strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") &&
				!strings.Contains(n, ".gen.") && !strings.HasSuffix(n, ".pb.go") {
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

func parseFile(t *testing.T, path string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return fset, f
}

// kafkaAlias returns the local name under which the file imports kafka-go ("" if it does not).
func kafkaAlias(f *ast.File) string {
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) == kafkaGoImport {
			if imp.Name != nil {
				return imp.Name.Name
			}
			return "kafka"
		}
	}
	return ""
}

func importsAny(f *ast.File, substrings ...string) bool {
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		for _, s := range substrings {
			if strings.Contains(p, s) {
				return true
			}
		}
	}
	return false
}

// literalOf reports whether n is a composite literal of type <alias>.<name>.
func literalOf(n ast.Node, alias, name string) (*ast.CompositeLit, bool) {
	cl, ok := n.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	sel, ok := cl.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return nil, false
	}
	id, ok := sel.X.(*ast.Ident)
	return cl, ok && id.Name == alias
}

func fieldSet(cl *ast.CompositeLit) map[string]ast.Expr {
	m := map[string]ast.Expr{}
	for _, e := range cl.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			if k, ok := kv.Key.(*ast.Ident); ok {
				m[k.Name] = kv.Value
			}
		}
	}
	return m
}

func TestCloudEventsOnly(t *testing.T) {
	usesKafka, usesSDK := "", false
	for _, p := range goFiles(t, "internal", "cmd") {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "EVENT_ENVELOPE_MODE") {
			t.Errorf("%s", archViolation("contents", "CloudEvents 1.0 is the only envelope",
				p+" mentions EVENT_ENVELOPE_MODE: the flat/dual envelope toggle was removed fleet-wide "+
					"(fleet CloudEvents standard, warehouse-docs agents/fleet/cloudevents.md)"))
		}
		_, f := parseFile(t, p)
		if kafkaAlias(f) != "" && usesKafka == "" {
			usesKafka = p
		}
		if importsAny(f, "cloudevents/sdk-go") {
			usesSDK = true
		}
	}
	if usesKafka != "" && !usesSDK {
		t.Errorf("%s", archViolation("contents", "Kafka envelopes are built with the CloudEvents SDK",
			usesKafka+" uses kafka-go but no non-test file imports github.com/cloudevents/sdk-go: "+
				"hand-built envelopes break the fleet contract"))
	}
}

// isUniqueGroupCall reports whether e is a call such as uniqueConsumerGroup() or pkg.NewUniqueConsumerGroup(x):
// the GroupID of a full-replay cache consumer, unique to this process instance.
func isUniqueGroupCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	name := ""
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		name = fn.Name
	case *ast.SelectorExpr:
		name = fn.Sel.Name
	}
	l := strings.ToLower(name)
	return strings.Contains(l, "unique") && strings.Contains(l, "group")
}

func TestReplayConsumersSetCommitInterval(t *testing.T) {
	for _, p := range goFiles(t, "internal", "cmd") {
		fset, f := parseFile(t, p)
		alias := kafkaAlias(f)
		if alias == "" {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			cl, ok := literalOf(n, alias, "ReaderConfig")
			if !ok {
				return true
			}
			fs := fieldSet(cl)
			if g, hasGroup := fs["GroupID"]; !hasGroup || !isUniqueGroupCall(g) {
				return true
			}
			if _, hasCI := fs["CommitInterval"]; !hasCI {
				t.Errorf("%s", archViolation("contents", "replay readers set CommitInterval",
					fset.Position(cl.Pos()).String()+": ReaderConfig with a process-unique GroupID (a full-replay "+
						"cache consumer) has no CommitInterval, so kafka-go commits synchronously after every message"))
			}
			return true
		})
	}
}
