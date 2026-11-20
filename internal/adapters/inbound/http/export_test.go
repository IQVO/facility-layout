package http

import "net/http"

// ProblemTypeForTest is one problem type of the catalogue, for the external test package.
type ProblemTypeForTest struct {
	Slug   string
	Status int
	Class  string
}

// ProblemTypesForTest is the whole catalogue (allProblemTypes), one entry per slug and status.
func ProblemTypesForTest() []ProblemTypeForTest {
	var out []ProblemTypeForTest
	seen := map[string]bool{}
	for _, p := range allProblemTypes() {
		key := p.Slug + http.StatusText(p.Status)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ProblemTypeForTest{Slug: p.Slug, Status: p.Status, Class: string(p.Class)})
	}
	return out
}

// RefuseForTest answers the problem type (slug, status) through the production error writer: the
// typed domain error through writeError, any other through writeProblem, so the class marking the
// middleware reads is the production one.
func RefuseForTest(w http.ResponseWriter, r *http.Request, slug string, status int) {
	for _, p := range allProblemTypes() {
		if p.Slug != slug || p.Status != status {
			continue
		}
		if p.err != nil {
			writeError(w, r, p.err)
			return
		}
		writeProblem(w, p.Status, p.info, "refused for the test", r.URL.Path)
		return
	}
	panic("no problem type " + slug)
}

// RefuseAsOutcomeForTest answers a 409 whose class is OUTCOME. No production problem type
// declares OUTCOME (ADR 0046 §3), so the mechanism is exercised with this synthetic one.
func RefuseAsOutcomeForTest(w http.ResponseWriter, r *http.Request) {
	writeProblem(w, http.StatusConflict, problemInfo{"synthetic-outcome", "A synthetic OUTCOME refusal", classOutcome}, "refused for the test", r.URL.Path)
}
