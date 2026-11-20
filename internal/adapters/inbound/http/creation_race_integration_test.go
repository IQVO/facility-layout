//go:build integration

// A concurrent creation of the same code loses data (ADR 0047, the follow-up
// ADR 0046 named): two requests registering the SAME new natural key both
// passed the use case's existence check and the second write overwrote the
// first row, so both answered 201. Through the REAL router over a real
// Postgres, one request per route is parked after its aggregate write inside
// its still-open transaction (the gated publisher) while a second request
// for the SAME code runs past the existence check and blocks on the row.
// Exactly one creation must win: the winner answers 201, the loser answers
// the documented duplicate refusal (FINAL, ADR 0046 — stored and replayed
// under the loser's own Idempotency-Key), exactly one row exists and it
// still carries the WINNER's data.
package http_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
)

// creationRaceCase is one creation route's staged lost race.
type creationRaceCase struct {
	name string
	// path and the two request bodies: the SAME natural key, different
	// payload so the surviving row's owner is provable.
	path       string
	winnerBody string
	loserBody  string
	// dupSlug is the documented duplicate-* problem type the loser gets.
	dupSlug string
	// table is counted to prove exactly one row was written.
	table string
	// seed registers the parents the raced route needs (its own keys).
	seed func(t *testing.T, router http.Handler)
	// winnerColumn asserts the surviving row still carries the winner's
	// payload — the pre-ADR-0047 defect silently replaced it with the
	// loser's. query must select exactly one scalar; want is its value.
	winnerColumn func() (query string, want any)
}

// stageCreationRace runs the two requests of c: the winner is parked after
// its aggregate write (inside its open transaction) until the loser is
// blocked on that row, then released. The winner is always the parked
// request: it commits first.
func stageCreationRace(t *testing.T, c creationRaceCase, keyPrefix string) (winner, loser *httptest.ResponseRecorder) {
	t.Helper()
	pool := idempotencyDB(t)
	gate := newGatedPublisher(events.NewLogPublisher(slog.New(slog.NewTextHandler(io.Discard, nil))))
	router := newIdempotencyRouterPublishing(t, pool, gate)
	if c.seed != nil {
		c.seed(t, router)
	}

	gate.arm()
	a := make(chan *httptest.ResponseRecorder, 1)
	go func() { a <- idempotentPost(t, router, c.path, keyPrefix+"-a", c.winnerBody) }()
	<-gate.arrived

	b := make(chan *httptest.ResponseRecorder, 1)
	go func() { b <- idempotentPost(t, router, c.path, keyPrefix+"-b", c.loserBody) }()
	waitForLockWaiter(t, pool)
	close(gate.freed)

	winner, loser = <-a, <-b
	assertCreationRaceOutcome(t, pool, router, c, keyPrefix, winner, loser)
	return winner, loser
}

// assertCreationRaceOutcome is the ADR 0047 contract for one route: one 201,
// one FINAL duplicate refusal, one row carrying the winner's data, and the
// loser's answer stored and replayed under its own key (ADR 0046).
func assertCreationRaceOutcome(t *testing.T, pool *pgxpool.Pool, router http.Handler, c creationRaceCase, keyPrefix string, winner, loser *httptest.ResponseRecorder) {
	t.Helper()
	if winner.Code != http.StatusCreated {
		t.Fatalf("winner = %d %s, want 201", winner.Code, winner.Body.String())
	}
	if loser.Code != http.StatusConflict || problemSlug(t, loser.Body.Bytes()) != c.dupSlug {
		t.Fatalf("loser = %d %s, want 409 %s (the concurrent creation of the same code must lose to the duplicate refusal, not overwrite the winner)", loser.Code, loser.Body.String(), c.dupSlug)
	}
	if got := countRows(t, pool, c.table); got != 1 {
		t.Fatalf("%s rows = %d, want exactly 1", c.table, got)
	}
	if c.winnerColumn != nil {
		query, want := c.winnerColumn()
		var got any
		if err := pool.QueryRow(context.Background(), query).Scan(&got); err != nil {
			t.Fatalf("read the surviving row: %v", err)
		}
		if got != want {
			t.Fatalf("the surviving row carries %v, want the winner's %v (the loser overwrote it)", got, want)
		}
	}
	if n := keyRows(t, pool, keyPrefix+"-a"); n != 1 {
		t.Fatalf("winner key rows = %d, want 1", n)
	}
	if n := keyRows(t, pool, keyPrefix+"-b"); n != 1 {
		t.Fatalf("loser key rows = %d, want 1 (a FINAL refusal is stored with its key, ADR 0046)", n)
	}

	// The loser's answer is FINAL (ADR 0046): stored, so its own retry under
	// the same key replays byte-identically instead of executing again.
	replay := idempotentPost(t, router, c.path, keyPrefix+"-b", c.loserBody)
	if !wasReplayed(replay) || replay.Code != http.StatusConflict || problemSlug(t, replay.Body.Bytes()) != c.dupSlug || replay.Body.String() != loser.Body.String() {
		t.Fatalf("loser's retry = %d replayed=%v %s, want the stored %s replayed byte-identically", replay.Code, wasReplayed(replay), replay.Body.String(), c.dupSlug)
	}
}

// TestCreationRace_ExactlyOneCreationOfTheSameCodeWins races every keyed
// creation route. It fails on any route whose second writer can still
// overwrite the first (the ADR 0046 follow-up defect).
func TestCreationRace_ExactlyOneCreationOfTheSameCodeWins(t *testing.T) {
	cases := []creationRaceCase{
		{
			name:       "site",
			path:       "/sites",
			winnerBody: `{"siteCode":"WH9","name":"Race Winner One"}`,
			loserBody:  `{"siteCode":"WH9","name":"Race Loser Nine"}`,
			dupSlug:    "duplicate-site-code",
			table:      "sites",
			winnerColumn: func() (string, any) {
				return `SELECT name FROM sites WHERE code = 'WH9'`, "Race Winner One"
			},
		},
		{
			name:       "zone",
			path:       "/sites/WH1/zones",
			winnerBody: `{"areaCode":"RCV","zoneCode":"CLD","temperatureClass":"Chilled","hazmat":false}`,
			loserBody:  `{"areaCode":"RCV","zoneCode":"CLD","temperatureClass":"Ambient","hazmat":true}`,
			dupSlug:    "duplicate-zone",
			table:      "zones",
			seed: func(t *testing.T, router http.Handler) {
				t.Helper()
				if rec := idempotentPost(t, router, "/sites", "race-zone-site", validSiteBody); rec.Code != http.StatusCreated {
					t.Fatalf("seed site: %d %s", rec.Code, rec.Body.String())
				}
			},
			winnerColumn: func() (string, any) {
				return `SELECT temperature_class FROM zones WHERE id = 'WH1-RCV-CLD'`, "Chilled"
			},
		},
		{
			name:       "aisle",
			path:       "/zones/WH1-STOR-AMB/aisles",
			winnerBody: `{"aisleCode":"A11","sequenceHint":4,"direction":"TwoWay"}`,
			loserBody:  `{"aisleCode":"A11","sequenceHint":9,"direction":"OneWay"}`,
			dupSlug:    "duplicate-aisle",
			table:      "aisles",
			seed: func(t *testing.T, router http.Handler) {
				t.Helper()
				if rec := idempotentPost(t, router, "/sites", "race-aisle-site", validSiteBody); rec.Code != http.StatusCreated {
					t.Fatalf("seed site: %d %s", rec.Code, rec.Body.String())
				}
				if rec := idempotentPost(t, router, "/sites/WH1/zones", "race-aisle-zone",
					`{"areaCode":"STOR","zoneCode":"AMB","temperatureClass":"Ambient","hazmat":false}`); rec.Code != http.StatusCreated {
					t.Fatalf("seed zone: %d %s", rec.Code, rec.Body.String())
				}
			},
			winnerColumn: func() (string, any) {
				return `SELECT sequence_hint FROM aisles WHERE aisle_code = 'A11'`, int32(4)
			},
		},
		{
			name:       "cross-aisle",
			path:       "/zones/WH1-STOR-AMB/cross-aisles",
			winnerBody: `{"fromAisle":"A01","toAisle":"A02","atBay":"03"}`,
			loserBody:  `{"fromAisle":"A01","toAisle":"A02","atBay":"03"}`,
			dupSlug:    "duplicate-cross-aisle",
			table:      "cross_aisles",
			seed: func(t *testing.T, router http.Handler) {
				t.Helper()
				if rec := idempotentPost(t, router, "/sites", "race-cross-site", validSiteBody); rec.Code != http.StatusCreated {
					t.Fatalf("seed site: %d %s", rec.Code, rec.Body.String())
				}
				if rec := idempotentPost(t, router, "/sites/WH1/zones", "race-cross-zone",
					`{"areaCode":"STOR","zoneCode":"AMB","temperatureClass":"Ambient","hazmat":false}`); rec.Code != http.StatusCreated {
					t.Fatalf("seed zone: %d %s", rec.Code, rec.Body.String())
				}
				if rec := idempotentPost(t, router, "/zones/WH1-STOR-AMB/aisles", "race-cross-a1",
					`{"aisleCode":"A01","sequenceHint":1,"direction":"TwoWay"}`); rec.Code != http.StatusCreated {
					t.Fatalf("seed aisle 1: %d %s", rec.Code, rec.Body.String())
				}
				if rec := idempotentPost(t, router, "/zones/WH1-STOR-AMB/aisles", "race-cross-a2",
					`{"aisleCode":"A02","sequenceHint":2,"direction":"TwoWay"}`); rec.Code != http.StatusCreated {
					t.Fatalf("seed aisle 2: %d %s", rec.Code, rec.Body.String())
				}
			},
		},
		{
			name:       "location type",
			path:       "/location-types",
			winnerBody: `{"name":"Shelf","defaultCapacity":{"maxWeightKg":1500,"maxVolumeM3":3.0}}`,
			loserBody:  `{"name":"Shelf","defaultCapacity":{"maxWeightKg":900,"maxVolumeM3":1.8}}`,
			dupSlug:    "duplicate-location-type",
			table:      "location_types",
			winnerColumn: func() (string, any) {
				return `SELECT default_max_weight_kg FROM location_types WHERE name = 'Shelf'`, 1500.0
			},
		},
		{
			name:       "placement rule",
			path:       "/placement-rules",
			winnerBody: `{"ruleId":"race-rule","locationType":"PalletRack","effect":"Deny","zone":{"temperatureClass":"Ambient"}}`,
			loserBody:  `{"ruleId":"race-rule","locationType":"PalletRack","effect":"Allow","zone":{"temperatureClass":"Ambient"}}`,
			dupSlug:    "duplicate-placement-rule",
			table:      "placement_rules",
			seed: func(t *testing.T, router http.Handler) {
				t.Helper()
				if rec := idempotentPost(t, router, "/location-types", "race-rule-type",
					`{"name":"PalletRack","defaultCapacity":{"maxWeightKg":1200,"maxVolumeM3":2.4}}`); rec.Code != http.StatusCreated {
					t.Fatalf("seed type: %d %s", rec.Code, rec.Body.String())
				}
			},
			winnerColumn: func() (string, any) {
				return `SELECT effect FROM placement_rules WHERE id = 'race-rule'`, "Deny"
			},
		},
		{
			name:       "fixed structure",
			path:       "/sites/WH1/structures",
			winnerBody: `{"id":"race-wall","kind":"Wall","footprint":{"origin":{"xM":0,"yM":0,"zM":0},"size":{"widthM":1,"depthM":1,"heightM":1}},"label":"North wall"}`,
			loserBody:  `{"id":"race-wall","kind":"Wall","footprint":{"origin":{"xM":0,"yM":0,"zM":0},"size":{"widthM":1,"depthM":1,"heightM":1}},"label":"South wall"}`,
			dupSlug:    "duplicate-fixed-structure",
			table:      "fixed_structures",
			seed: func(t *testing.T, router http.Handler) {
				t.Helper()
				if rec := idempotentPost(t, router, "/sites", "race-structure-site", validSiteBody); rec.Code != http.StatusCreated {
					t.Fatalf("seed site: %d %s", rec.Code, rec.Body.String())
				}
			},
			winnerColumn: func() (string, any) {
				return `SELECT label FROM fixed_structures WHERE id = 'race-wall'`, "North wall"
			},
		},
		{
			name:       "location slot",
			path:       "/locations",
			winnerBody: `{"locationCode":"WH1-STOR-AMB-A07-03-02-B","locationType":"PalletRack","capacityOverride":{"maxWeightKg":800,"maxVolumeM3":1.6}}`,
			loserBody:  `{"locationCode":"WH1-STOR-AMB-A07-03-02-B","locationType":"PalletRack","capacityOverride":{"maxWeightKg":500,"maxVolumeM3":1.0}}`,
			dupSlug:    "duplicate-location-code",
			table:      "location_slots",
			seed:       seedLocationChain,
			winnerColumn: func() (string, any) {
				return `SELECT max_weight_kg FROM location_slots WHERE code = 'WH1-STOR-AMB-A07-03-02-B'`, 800.0
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stageCreationRace(t, tc, "race-"+tc.name)
		})
	}
}
