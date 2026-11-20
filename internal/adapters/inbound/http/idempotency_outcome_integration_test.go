//go:build integration

package http_test

import (
	"net/http"
	"testing"

	inboundhttp "github.com/claudioed/facility-layout/internal/adapters/inbound/http"
)

// No production problem type declares OUTCOME (ADR 0046 §3: no keyed use case
// commits state and then refuses), so the OUTCOME mechanism of the middleware
// is proven with a synthetic refusal over the real middleware and Postgres:
// the command commits one write, answers a refusal declared OUTCOME, and that
// write commits WITH the key row; a replay answers from the row and records
// nothing new.
func TestReplayClass_AnOutcomeRefusalCommitsItsWritesWithTheKeyRowAndReplays(t *testing.T) {
	ops := newReplayOps(t)
	calls := 0
	mw := ops.Middleware(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := ops.Write(r.Context(), "outcome"); err != nil {
			t.Errorf("write: %v", err)
		}
		inboundhttp.RefuseAsOutcomeForTest(w, r)
	}))

	first := post(t, mw, "k-synthetic-outcome", `{"n":1}`)
	if first.Code != http.StatusConflict {
		t.Fatalf("first = %d %s, want the 409 OUTCOME refusal", first.Code, first.Body.String())
	}
	if got := ops.Committed(t, "outcome"); got != 1 {
		t.Fatalf("committed writes after the refusal = %d, want 1: an OUTCOME refusal commits what the command did", got)
	}
	if got := ops.KeyRows(t, "k-synthetic-outcome"); got != 1 {
		t.Fatalf("key rows = %d, want 1: the answer is stored in the same transaction as the writes", got)
	}
	for i := 0; i < 3; i++ {
		rep := post(t, mw, "k-synthetic-outcome", `{"n":1}`)
		if rep.Code != http.StatusConflict || !replayed(rep) || rep.Body.String() != first.Body.String() {
			t.Errorf("replay %d = %d replayed=%v, want the stored 409", i, rep.Code, replayed(rep))
		}
	}
	if calls != 1 {
		t.Errorf("the command ran %d times, want 1", calls)
	}
	if got := ops.Committed(t, "outcome"); got != 1 {
		t.Errorf("committed writes after 3 replays = %d, want 1: a replay records nothing new", got)
	}
}
