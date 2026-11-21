package http

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// (a) Every problem type declares exactly one replay class on its catalogue entry. There is no
// runtime default: a new problem type is classified by the PR that adds it, or this test is red.
func TestEveryProblemTypeDeclaresAReplayClass(t *testing.T) {
	types := allProblemTypes()
	if len(types) == 0 {
		t.Fatal("the catalogue accessor returned no problem types: the test would pass on nothing")
	}
	classOf := map[string]replayClass{}
	for _, p := range types {
		t.Run(p.Slug, func(t *testing.T) {
			if !validReplayClass(p.Class) {
				t.Errorf("problem type %q (%d) declares no replay class (%q): set FINAL, OUTCOME or RETRYABLE on its catalogue entry (.claude/rules/fleet/idempotency-and-outbox.md)", p.Slug, p.Status, p.Class)
			}
			if p.Status >= http.StatusInternalServerError && p.Class != classRetryable {
				t.Errorf("problem type %q answers %d: a 5xx is never stored, so its class is RETRYABLE, got %q", p.Slug, p.Status, p.Class)
			}
			if prev, ok := classOf[p.Slug]; ok && prev != p.Class {
				t.Errorf("slug %q is declared %s and %s: one problem type has exactly one class", p.Slug, prev, p.Class)
			}
			classOf[p.Slug] = p.Class
		})
	}
}

// The check above must bite: an unclassified entry and a misspelt class are refused.
func TestTheUnclassifiedEntryCheckBites(t *testing.T) {
	for _, c := range []replayClass{"", "final", "STORED", "OUTCOME "} {
		if validReplayClass(c) {
			t.Errorf("class %q is accepted; only FINAL, OUTCOME and RETRYABLE are classes", c)
		}
	}
	for _, c := range []replayClass{classFinal, classOutcome, classRetryable} {
		if !validReplayClass(c) {
			t.Errorf("class %q is refused", c)
		}
	}
}

// problemInfo literals are the catalogue: they may only be written where the class is declared
// (errors.go's rows and problems.go's register calls), and each one carries its class. A literal
// built at a call site would escape the unclassified-entry check above.
func TestEveryProblemInfoLiteralIsInTheCatalogueAndClassified(t *testing.T) {
	literals := problemInfoLiterals(t)
	if len(literals) == 0 {
		t.Fatal("no problemInfo literal found: the scan would pass on nothing")
	}
	for _, l := range literals {
		if l.file != "errors.go" && l.file != "problems.go" {
			t.Errorf("%s: a problemInfo literal outside errors.go and problems.go: declare it with register(...) in problems.go so it carries a class", l.pos)
		}
		if !literalDeclaresClass(l.lit) {
			t.Errorf("%s: this problemInfo literal declares no class", l.pos)
		}
	}
}

type problemInfoLiteral struct {
	file string
	pos  token.Position
	lit  *ast.CompositeLit
}

// problemInfoLiterals finds every problemInfo composite literal of the package's non-test files.
func problemInfoLiterals(t *testing.T) []problemInfoLiteral {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var found []problemInfoLiteral
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.CompositeLit); ok && isProblemInfo(lit) {
				found = append(found, problemInfoLiteral{file: name, pos: fset.Position(lit.Pos()), lit: lit})
			}
			return true
		})
	}
	return found
}

func isProblemInfo(lit *ast.CompositeLit) bool {
	id, ok := lit.Type.(*ast.Ident)
	return ok && id.Name == "problemInfo"
}

// literalDeclaresClass: the third positional element, or a keyed `class`.
func literalDeclaresClass(lit *ast.CompositeLit) bool {
	for i, e := range lit.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "class" {
				return true
			}
		} else if i == 2 {
			return true
		}
	}
	return false
}

// The ADR's class table is the human copy of the catalogue: one row per slug, same status and class.
func TestTheADRClassTableMatchesTheCatalogue(t *testing.T) {
	raw, err := os.ReadFile("../../../../docs/docs/adr/0046-idempotency-replay-classes.md")
	if err != nil {
		t.Fatalf("the ADR with the class table: %v", err)
	}
	row := regexp.MustCompile("(?m)^\\| `([a-z0-9-]+)` \\| (\\d{3}) \\| (FINAL|OUTCOME|RETRYABLE) \\|")
	type entry struct{ status, class string }
	inADR := map[string]entry{}
	for _, m := range row.FindAllStringSubmatch(string(raw), -1) {
		if _, dup := inADR[m[1]]; dup {
			t.Errorf("slug %q is listed twice in the ADR table", m[1])
		}
		inADR[m[1]] = entry{m[2], m[3]}
	}
	inCode := map[string]entry{}
	for _, p := range allProblemTypes() {
		inCode[p.Slug] = entry{strconv.Itoa(p.Status), string(p.Class)}
		got, ok := inADR[p.Slug]
		switch {
		case !ok:
			t.Errorf("problem type %q (%s) is missing from the ADR table", p.Slug, p.Class)
		case got.class != string(p.Class):
			t.Errorf("problem type %q is %s in the catalogue and %s in the ADR", p.Slug, p.Class, got.class)
		case got.status != strconv.Itoa(p.Status):
			t.Errorf("problem type %q answers %d in the catalogue and %s in the ADR", p.Slug, p.Status, got.status)
		}
	}
	for slug := range inADR {
		if _, ok := inCode[slug]; !ok {
			t.Errorf("the ADR lists %q, which is not a problem type of the catalogue", slug)
		}
	}
}

// Every catalogue row is reachable: statusFor and problemFor answer the row's own status and
// problem for its sentinel, so no earlier row shadows it and the accessor cannot drift.
func TestEveryCatalogueRowIsWhatTheMappersAnswer(t *testing.T) {
	for _, c := range errorCategories {
		if got := statusFor(c.err); got != c.status {
			t.Errorf("%s: statusFor = %d, the row says %d (shadowed by an earlier row?)", c.problem.slug, got, c.status)
		}
		if got := problemFor(c.err); got != c.problem {
			t.Errorf("%s: problemFor = %+v, the row says %+v", c.problem.slug, got, c.problem)
		}
	}
}

// The decision the middleware takes, for every status and class: 2xx stored, 5xx never, the rest
// by the declared class, and a refusal without a class (or with a misspelt one) is released.
func TestStorableDecisionByStatusAndClass(t *testing.T) {
	cases := []struct {
		code  int
		class replayClass
		want  bool
	}{
		{http.StatusCreated, "", true},
		{http.StatusNoContent, "", true},
		{http.StatusOK, classRetryable, true},
		{http.StatusInternalServerError, classFinal, false},
		{http.StatusInternalServerError, classOutcome, false},
		{http.StatusServiceUnavailable, "", false},
		{http.StatusBadRequest, classFinal, true},
		{http.StatusConflict, classOutcome, true},
		{http.StatusUnprocessableEntity, classFinal, true},
		{http.StatusConflict, classRetryable, false},
		{http.StatusNotFound, classRetryable, false},
		{http.StatusConflict, "", false},
		{http.StatusBadRequest, "final", false},
		{http.StatusMovedPermanently, "", false},
	}
	for _, c := range cases {
		rec := newCapture()
		rec.markReplayClass(c.class)
		rec.WriteHeader(c.code)
		if got := rec.storable(); got != c.want {
			t.Errorf("status %d class %q: storable = %v, want %v", c.code, c.class, got, c.want)
		}
	}
}

// Lost writes: a 2xx and an OUTCOME refusal need their writes; a FINAL refusal does not.
func TestWritesAreLostOnlyForAnswersThatNeedThem(t *testing.T) {
	for _, c := range []struct {
		code  int
		class replayClass
		want  bool
	}{
		{http.StatusCreated, "", true},
		{http.StatusConflict, classOutcome, true},
		{http.StatusConflict, classFinal, false},
		{http.StatusConflict, classRetryable, false},
	} {
		rec := newCapture()
		rec.markReplayClass(c.class)
		rec.WriteHeader(c.code)
		if got := rec.writesAreLost(); got != c.want {
			t.Errorf("status %d class %q: writesAreLost = %v, want %v", c.code, c.class, got, c.want)
		}
	}
}

// writeProblem marks the capturing writer with the class of the problem it answers.
func TestWriteProblemMarksTheCaptureWithTheCatalogueClass(t *testing.T) {
	for _, p := range allProblemTypes() {
		rec := newCapture()
		writeProblem(rec, p.Status, p.info, "d", "/x")
		if rec.class != p.Class {
			t.Errorf("%s: capture class = %q, want %q", p.Slug, rec.class, p.Class)
		}
	}
	// A plain writer (no idempotency middleware wired) is not a capture: nothing to mark.
	writeProblem(httptest.NewRecorder(), http.StatusConflict, problemInternalError, "d", "/x")
}

func TestWriteErrorMarksTheClassOfTheMappedProblem(t *testing.T) {
	for _, c := range errorCategories {
		rec := newCapture()
		writeError(rec, httptest.NewRequest(http.MethodPost, "/x", nil), c.err)
		if rec.class != c.problem.class || !validReplayClass(rec.class) {
			t.Errorf("%s: writeError marked %q, the entry declares %q", c.problem.slug, rec.class, c.problem.class)
		}
	}
}

// An error the catalogue does not map answers 500 internal-error, a 5xx: RETRYABLE, never stored.
func TestAnUnmappedErrorIsAnInternalErrorThatIsNeverStored(t *testing.T) {
	rec := newCapture()
	writeError(rec, httptest.NewRequest(http.MethodPost, "/x", nil), errors.New("a domain error nobody mapped"))
	if rec.Code != http.StatusInternalServerError || rec.class != classRetryable || rec.storable() {
		t.Fatalf("unmapped error = %d class %q storable=%v, want 500 RETRYABLE not storable", rec.Code, rec.class, rec.storable())
	}
}
