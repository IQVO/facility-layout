//go:build integration

// One AS/RS per aisle under a real race (ADR 0048): two KEYED requests each
// register a DIFFERENT AutomatedStorage code in the same aisle. Through the
// REAL router over a real Postgres, the winner is parked after its slot
// write inside its still-open transaction (the gated publisher) while the
// loser runs past the use case's in-memory check and blocks on migration
// 0014's partial unique index; the winner is then released and commits.
// Exactly one AS/RS ends up in the aisle either way. What the loser answers
// depends on the route:
//   - POST /locations: 409 automated-storage-already-in-aisle, RETRYABLE
//     (ADR 0046), so its key is released and a retry executes again;
//   - POST /location-imports: the index violation aborts the import's shared
//     transaction (the per-row rejection cannot be recorded inside it), so
//     the import answers 500; nothing of it commits and the key is released.
//     A retry under the same key executes again and now reports the row
//     rejected with the rule's reason — the same 200 answer a sequential
//     import gets.
package http_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
	"github.com/claudioed/facility-layout/internal/application/usecases"
)

const (
	asrsWinnerCode = "WH1-AUTO-ASRS-ASRS1-00-00-S"
	asrsLoserCode  = "WH1-AUTO-ASRS-ASRS1-00-00-T"
)

// seedASRSAisle registers site WH1, zone WH1-AUTO-ASRS, aisle ASRS1 and the
// AutomatedStorage location type (role Storage), each under its own key.
func seedASRSAisle(t *testing.T, router http.Handler) {
	t.Helper()
	seeds := []struct{ path, key, body string }{
		{"/sites", "asrs-seed-site", validSiteBody},
		{"/sites/WH1/zones", "asrs-seed-zone", `{"areaCode":"AUTO","zoneCode":"ASRS","temperatureClass":"Ambient","hazmat":false}`},
		{"/zones/WH1-AUTO-ASRS/aisles", "asrs-seed-aisle", `{"aisleCode":"ASRS1","sequenceHint":1,"direction":"TwoWay"}`},
		{"/location-types", "asrs-seed-type", `{"name":"AutomatedStorage","role":"Storage","defaultCapacity":{"maxWeightKg":50000,"maxVolumeM3":400}}`},
	}
	for _, s := range seeds {
		if rec := idempotentPost(t, router, s.path, s.key, s.body); rec.Code != http.StatusCreated {
			t.Fatalf("seed %s: %d %s", s.path, rec.Code, rec.Body.String())
		}
	}
}

// asrsImportBody is a one-row import of the AutomatedStorage slot at
// position in aisle ASRS1 (bay 00, level 00), naming the seeded parents.
func asrsImportBody(position string) string {
	return `[{"siteCode":"WH1","areaCode":"AUTO","zoneCode":"ASRS","temperatureClass":"Ambient","hazmat":false,` +
		`"aisleCode":"ASRS1","sequenceHint":1,"direction":"TwoWay","bay":"00","level":"00","position":"` + position + `",` +
		`"locationType":"AutomatedStorage"}]`
}

// stageASRSRace seeds the aisle, parks the winner's request after its slot
// write, waits until the loser blocks on the partial unique index, then
// releases the winner. It returns both answers and the pool.
func stageASRSRace(t *testing.T, path, winnerKey, winnerBody, loserKey, loserBody string) (pool *pgxpool.Pool, router http.Handler, winner, loser *httptest.ResponseRecorder) {
	t.Helper()
	pool = idempotencyDB(t)
	gate := newGatedPublisher(events.NewLogPublisher(slog.New(slog.NewTextHandler(io.Discard, nil))))
	router = newIdempotencyRouterPublishing(t, pool, gate)
	seedASRSAisle(t, router)

	gate.arm()
	a := make(chan *httptest.ResponseRecorder, 1)
	go func() { a <- idempotentPost(t, router, path, winnerKey, winnerBody) }()
	<-gate.arrived

	b := make(chan *httptest.ResponseRecorder, 1)
	go func() { b <- idempotentPost(t, router, path, loserKey, loserBody) }()
	waitForLockWaiter(t, pool)
	close(gate.freed)
	return pool, router, <-a, <-b
}

// assertOneASRSInTheAisle proves the index held: the aisle holds exactly one
// AutomatedStorage slot, and it is the winner's.
func assertOneASRSInTheAisle(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var n int
	var code string
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*), min(code) FROM location_slots WHERE location_type = 'AutomatedStorage' AND aisle_id = 'WH1-AUTO-ASRS-ASRS1'`,
	).Scan(&n, &code); err != nil {
		t.Fatalf("read the aisle's AS/RS slots: %v", err)
	}
	if n != 1 || code != asrsWinnerCode {
		t.Fatalf("aisle ASRS1 holds %d AutomatedStorage slot(s) (first %q), want exactly the winner's %s", n, code, asrsWinnerCode)
	}
}

func TestASRSRace_ALostRegistrationIsTheRetryableAisleRefusal(t *testing.T) {
	winnerBody := `{"locationCode":"` + asrsWinnerCode + `","locationType":"AutomatedStorage"}`
	loserBody := `{"locationCode":"` + asrsLoserCode + `","locationType":"AutomatedStorage"}`
	pool, router, winner, loser := stageASRSRace(t, "/locations", "asrs-race-a", winnerBody, "asrs-race-b", loserBody)

	if winner.Code != http.StatusCreated {
		t.Fatalf("winner = %d %s, want 201", winner.Code, winner.Body.String())
	}
	if loser.Code != http.StatusConflict || problemSlug(t, loser.Body.Bytes()) != "automated-storage-already-in-aisle" {
		t.Fatalf("loser = %d %s, want 409 automated-storage-already-in-aisle (the index must refuse the second AS/RS)", loser.Code, loser.Body.String())
	}
	assertOneASRSInTheAisle(t, pool)
	if n := keyRows(t, pool, "asrs-race-b"); n != 0 {
		t.Fatalf("loser key rows = %d, want 0 (a RETRYABLE refusal releases its key, ADR 0046)", n)
	}
	retry := idempotentPost(t, router, "/locations", "asrs-race-b", loserBody)
	if wasReplayed(retry) || retry.Code != http.StatusConflict || problemSlug(t, retry.Body.Bytes()) != "automated-storage-already-in-aisle" {
		t.Fatalf("loser's retry = %d replayed=%v %s, want an executed 409 automated-storage-already-in-aisle", retry.Code, wasReplayed(retry), retry.Body.String())
	}
	assertOneASRSInTheAisle(t, pool)
}

func TestASRSRace_ALostKeyedImportRollsBackReleasesItsKeyAndItsRetryRejectsTheRow(t *testing.T) {
	pool, router, winner, loser := stageASRSRace(t, "/location-imports",
		"asrs-import-a", asrsImportBody("S"), "asrs-import-b", asrsImportBody("T"))

	if winner.Code != http.StatusOK {
		t.Fatalf("winner import = %d %s, want 200", winner.Code, winner.Body.String())
	}
	if got := importReport(t, winner); got.SlotsImported != 1 || got.RowsRejected != 0 {
		t.Fatalf("winner report = %+v, want 1 imported, 0 rejected", got)
	}
	if loser.Code != http.StatusInternalServerError {
		t.Fatalf("loser import = %d %s, want 500 (the index violation aborts the import's shared transaction)", loser.Code, loser.Body.String())
	}
	if ct := loser.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("loser import Content-Type = %q, want application/problem+json", ct)
	}
	if slug := problemSlug(t, loser.Body.Bytes()); slug != "internal-error" {
		t.Fatalf("loser import problem = %q, want internal-error (the unmapped fallback, errors.go statusFor)", slug)
	}
	assertOneASRSInTheAisle(t, pool)
	if n := keyRows(t, pool, "asrs-import-b"); n != 0 {
		t.Fatalf("loser key rows = %d, want 0 (a 5xx rolls the key back, ADR 0019)", n)
	}

	retry := idempotentPost(t, router, "/location-imports", "asrs-import-b", asrsImportBody("T"))
	if wasReplayed(retry) || retry.Code != http.StatusOK {
		t.Fatalf("loser's retry = %d replayed=%v %s, want an executed 200", retry.Code, wasReplayed(retry), retry.Body.String())
	}
	got := importReport(t, retry)
	want := usecases.ErrAutomatedStorageAlreadyInAisle.Error()
	if got.SlotsImported != 0 || got.RowsRejected != 1 || len(got.Results) != 1 || got.Results[0].Error != want {
		t.Fatalf("retry report = %+v, want the one row rejected with %q", got, want)
	}
	if n := keyRows(t, pool, "asrs-import-b"); n != 1 {
		t.Fatalf("loser key rows after the retry = %d, want 1 (a 200 report is stored)", n)
	}
	assertOneASRSInTheAisle(t, pool)
}

// importReportBody is the wire shape of a POST /location-imports answer.
type importReportBody struct {
	SlotsImported int `json:"slotsImported"`
	RowsRejected  int `json:"rowsRejected"`
	Results       []struct {
		LocationCode string `json:"locationCode"`
		Succeeded    bool   `json:"succeeded"`
		Error        string `json:"error"`
	} `json:"results"`
}

func importReport(t *testing.T, rec *httptest.ResponseRecorder) importReportBody {
	t.Helper()
	var report importReportBody
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode import report: %v (body: %s)", err, rec.Body.String())
	}
	return report
}
