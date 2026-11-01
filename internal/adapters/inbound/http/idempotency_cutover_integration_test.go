//go:build integration

// REST guideline cutover cases for the idempotency middleware: the key is
// global (same key on another route is a reuse), a malformed key is
// idempotency-key-invalid, and the two formerly unkeyed commands
// (POST /location-imports, POST /locations/{code}/decommissionings) are
// keyed. Real router, real Postgres (testcontainers).
package http_test

import (
	"net/http"
	"strings"
	"testing"
)

const importRowsBody = `[{"siteCode":"WH1","siteName":"Fulfilment Centre One","areaCode":"STOR","zoneCode":"AMB","temperatureClass":"Ambient","aisleCode":"A07","sequenceHint":7,"bay":"03","level":"02","position":"B","locationType":"PalletRack"}]`

func TestIdempotency_KeyIsGlobalAcrossRoutes_Returns409(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)

	if rec := idempotentPost(t, router, "/sites", "global-key-1", validSiteBody); rec.Code != http.StatusCreated {
		t.Fatalf("first call status = %d, want 201", rec.Code)
	}
	rec := idempotentPost(t, router, "/location-types", "global-key-1", `{"name":"PalletRack","defaultCapacity":{"maxWeightKg":1200,"maxVolumeM3":2.4}}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := problemSlug(t, rec.Body.Bytes()); got != "idempotency-key-reused" {
		t.Fatalf("problem slug = %q, want idempotency-key-reused", got)
	}
	if got := countRows(t, pool, "location_types"); got != 0 {
		t.Fatalf("location_types rows = %d, want 0", got)
	}
}

func TestIdempotency_MalformedKey_Returns400Invalid(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)

	for name, key := range map[string]string{
		"longer than 128": strings.Repeat("k", 129),
		"non printable":   "bad\tkey",
	} {
		t.Run(name, func(t *testing.T) {
			rec := idempotentPost(t, router, "/sites", key, validSiteBody)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			if got := problemSlug(t, rec.Body.Bytes()); got != "idempotency-key-invalid" {
				t.Fatalf("problem slug = %q, want idempotency-key-invalid", got)
			}
		})
	}
	if got := countRows(t, pool, "sites"); got != 0 {
		t.Fatalf("sites rows = %d, want 0", got)
	}
}

func TestIdempotency_LocationImports_KeyedReplayAndMissingKey(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)
	if rec := idempotentPost(t, router, "/location-types", "imp-type", `{"name":"PalletRack","defaultCapacity":{"maxWeightKg":1200,"maxVolumeM3":2.4}}`); rec.Code != http.StatusCreated {
		t.Fatalf("seed type: %d %s", rec.Code, rec.Body.String())
	}

	if rec := idempotentPost(t, router, "/location-imports", "", importRowsBody); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing key status = %d, want 400", rec.Code)
	}
	first := idempotentPost(t, router, "/location-imports", "imp-1", importRowsBody)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200 (body: %s)", first.Code, first.Body.String())
	}
	second := idempotentPost(t, router, "/location-imports", "imp-1", importRowsBody)
	if second.Code != first.Code || second.Body.String() != first.Body.String() {
		t.Fatalf("replay differs from first:\n%s\n%s", first.Body.String(), second.Body.String())
	}
	if got := second.Header().Get("Idempotency-Replayed"); got != "true" {
		t.Fatalf("replay Idempotency-Replayed = %q, want true", got)
	}
	if got := countRows(t, pool, "location_slots"); got != 1 {
		t.Fatalf("location_slots rows = %d, want 1", got)
	}
}

func TestIdempotency_Decommissionings_KeyedReplayAndMissingKey(t *testing.T) {
	pool := idempotencyDB(t)
	router := newIdempotencyRouter(t, pool)
	seedLocationChain(t, router)
	if rec := idempotentPost(t, router, "/locations", "dec-slot", validSlotBody); rec.Code != http.StatusCreated {
		t.Fatalf("seed slot: %d %s", rec.Code, rec.Body.String())
	}
	path := "/locations/WH1-STOR-AMB-A07-03-02-B/decommissionings"

	if rec := idempotentPost(t, router, path, "", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing key status = %d, want 400", rec.Code)
	}
	first := idempotentPost(t, router, path, "dec-1", `{}`)
	if first.Code >= 300 {
		t.Fatalf("first status = %d (body: %s)", first.Code, first.Body.String())
	}
	second := idempotentPost(t, router, path, "dec-1", `{}`)
	if second.Code != first.Code || second.Body.String() != first.Body.String() {
		t.Fatalf("replay differs from first: %d %s", second.Code, second.Body.String())
	}
	if got := second.Header().Get("Idempotency-Replayed"); got != "true" {
		t.Fatalf("replay Idempotency-Replayed = %q, want true", got)
	}
}
