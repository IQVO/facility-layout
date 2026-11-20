package http

import (
	"net/http"
	"net/http/httptest"
)

// replayClass says what the Idempotency-Key middleware does with a problem answer when the same
// request is retried under the same key (docs/docs/adr/0046-idempotency-replay-classes.md, the
// fleet rule .claude/rules/fleet/idempotency-and-outbox.md). It is declared ON the catalogue entry
// of each problem type, next to its slug and title; there is no default and no list of slugs in
// the middleware.
type replayClass string

const (
	// classFinal: the answer is a function of the request alone, or a stored answer can never
	// become false. Stored and replayed.
	classFinal replayClass = "FINAL"
	// classOutcome: the command COMMITTED state and then answered an error that must be
	// remembered. Stored, and its writes commit with the stored answer (one transaction). No
	// problem type of this service declares it today (ADR 0046 §3).
	classOutcome replayClass = "OUTCOME"
	// classRetryable: nothing committed and the answer depends on state that can change. Not
	// stored: the transaction rolls back, key row included, and a retry executes again.
	classRetryable replayClass = "RETRYABLE"
)

// validReplayClass reports whether c is one of the three classes; "" is "unclassified".
func validReplayClass(c replayClass) bool {
	return c == classFinal || c == classOutcome || c == classRetryable
}

// problemType is one problem type of the catalogue as the tests and the ADR table see it.
type problemType struct {
	Slug   string
	Status int
	Class  replayClass
	info   problemInfo // the answer; err is the typed domain error behind it, nil for a registered one
	err    error
}

// allProblemTypes is the whole catalogue: every typed domain error of errorCategories with the
// status it maps to, then every problem type declared with register (problems.go).
func allProblemTypes() []problemType {
	out := make([]problemType, 0, len(errorCategories)+len(registered))
	for _, c := range errorCategories {
		out = append(out, problemType{Slug: c.problem.slug, Status: c.status, Class: c.problem.class, info: c.problem, err: c.err})
	}
	return append(out, registered...)
}

// replayMarker is implemented by the capturing writer the idempotency middleware hands the
// handler. The production error writer marks it with the class of the problem it answers.
type replayMarker interface {
	markReplayClass(replayClass)
}

// markReplayClass marks w when it is the middleware's capture; any other writer (a route without
// the middleware, a test recorder) has nothing to mark.
func markReplayClass(w http.ResponseWriter, class replayClass) {
	if m, ok := w.(replayMarker); ok {
		m.markReplayClass(class)
	}
}

// capture is the writer the middleware gives the wrapped handler: a recorder that also remembers
// the class of the problem the handler answered with.
type capture struct {
	*httptest.ResponseRecorder
	class replayClass
}

func newCapture() *capture { return &capture{ResponseRecorder: httptest.NewRecorder()} }

func (c *capture) markReplayClass(class replayClass) { c.class = class }

// success reports a 2xx answer.
func (c *capture) success() bool {
	return c.Code >= http.StatusOK && c.Code < http.StatusMultipleChoices
}

// storable reports whether the captured answer is final for the request: a 2xx is stored, a 5xx
// never is, and every other answer is stored only when its problem type declares FINAL or
// OUTCOME. A refusal that declares no class (or a misspelt one) is released like RETRYABLE: a
// released refusal costs one more execution, a wrongly stored one pins a stale refusal for the
// whole retention window.
func (c *capture) storable() bool {
	switch {
	case c.success():
		return true
	case c.Code >= http.StatusInternalServerError:
		return false
	default:
		return c.class == classFinal || c.class == classOutcome
	}
}

// writesAreLost reports whether an answer that needs its writes (a 2xx, or an OUTCOME refusal)
// cannot be stored once the handler's savepoint could not be released.
func (c *capture) writesAreLost() bool {
	return c.success() || c.class == classOutcome
}
