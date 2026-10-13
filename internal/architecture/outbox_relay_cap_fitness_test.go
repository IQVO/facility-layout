package architecture

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// callName returns the bare function name of a call: NewOutboxRelay for both
// NewOutboxRelay(...) and postgres.NewOutboxRelay(...).
func callName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

// passesMaxAttempts reports whether one of the call's arguments is a
// WithMaxAttempts(...) option.
func passesMaxAttempts(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		if inner, ok := arg.(*ast.CallExpr); ok && callName(inner) == "WithMaxAttempts" {
			return true
		}
	}
	return false
}

// outboxRelayCapViolations parses one Go file and returns how many
// NewOutboxRelay calls it makes plus one violation per call that does not
// pass WithMaxAttempts inline.
func outboxRelayCapViolations(t *testing.T, path string) (calls int, violations []string) {
	t.Helper()
	fset, f := parseFile(t, path)
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || callName(call) != "NewOutboxRelay" {
			return true
		}
		calls++
		if !passesMaxAttempts(call) {
			violations = append(violations, relayCapMessage(fset, call))
		}
		return true
	})
	return calls, violations
}

func relayCapMessage(fset *token.FileSet, call *ast.CallExpr) string {
	return fmt.Sprintf("%s: NewOutboxRelay without WithMaxAttempts: one poison row would block the relay forever. "+
		"FIX: pass postgres.WithMaxAttempts(outboxMaxAttempts(logger)) (env OUTBOX_MAX_ATTEMPTS, default 20, 0 = unlimited; ADR 0036)",
		fset.Position(call.Pos()))
}

// TestOutboxRelaysPassAttemptCap enforces gap fix D12 (ADR 0036): every
// non-test file under cmd/ that builds an outbox relay passes an attempt cap,
// so the cap is a deliberate setting at each composition root and never the
// unlimited default by omission.
func TestOutboxRelaysPassAttemptCap(t *testing.T) {
	total := 0
	for _, path := range goFiles(t, "cmd") {
		calls, violations := outboxRelayCapViolations(t, path)
		total += calls
		for _, v := range violations {
			t.Error(v)
		}
	}
	if total == 0 {
		t.Fatal("no NewOutboxRelay call found under cmd/; cmd/facility builds the relay, so the sensor is looking in the wrong place")
	}
}

// TestOutboxRelayCapSensorFailsOnBadFixtures proves the sensor can fail and
// accepts both call shapes that pass the option.
func TestOutboxRelayCapSensorFailsOnBadFixtures(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantBad int
	}{
		{"no options", "package x\n\nfunc f() { _ = postgres.NewOutboxRelay(pool, sink, logger) }\n", 1},
		{"other options only", "package x\n\nfunc f() { _ = postgres.NewOutboxRelay(pool, sink, logger, postgres.WithInterval(d), postgres.WithBatchSize(n)) }\n", 1},
		{"capped", "package x\n\nfunc f() { _ = postgres.NewOutboxRelay(pool, sink, logger, postgres.WithMaxAttempts(n)) }\n", 0},
		{"capped, unqualified", "package x\n\nfunc f() { _ = NewOutboxRelay(pool, sink, logger, WithInterval(d), WithMaxAttempts(20)) }\n", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "fixture.go")
			if err := os.WriteFile(p, []byte(tc.src), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			calls, got := outboxRelayCapViolations(t, p)
			if calls != 1 || len(got) != tc.wantBad {
				t.Fatalf("calls=%d violations=%v, want 1 call and %d violation(s)", calls, got, tc.wantBad)
			}
		})
	}
}
