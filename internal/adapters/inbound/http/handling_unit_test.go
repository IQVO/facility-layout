package http_test

import (
	"net/http"
	"testing"
)

const huSlot = "WH1-STOR-AMB-A07-03-02-B"

func slotHandlingUnit(t *testing.T, r response) (string, bool) {
	t.Helper()
	var raw map[string]any
	r.decode(t, &raw)
	v, ok := raw["handlingUnit"]
	if !ok {
		return "", false
	}
	return v.(string), true
}

func TestDeclareHandlingUnit(t *testing.T) {
	t.Run("sets the unit, echoes it, and the GET shows it", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.seedSlot(huSlot, "PalletRack")

		resp := ts.do(http.MethodPut, "/locations/"+huSlot+"/handling-unit", map[string]any{"handlingUnit": "CASE"}).assertStatus(t, http.StatusOK)
		if got, ok := slotHandlingUnit(t, resp); !ok || got != "CASE" {
			t.Fatalf("PUT handlingUnit = %q (present=%v)", got, ok)
		}
		get := ts.do(http.MethodGet, "/locations/"+huSlot, nil).assertStatus(t, http.StatusOK)
		if got, ok := slotHandlingUnit(t, get); !ok || got != "CASE" {
			t.Fatalf("GET handlingUnit = %q (present=%v)", got, ok)
		}
	})

	t.Run("a slot with no unit omits the field", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.seedSlot(huSlot, "PalletRack")
		get := ts.do(http.MethodGet, "/locations/"+huSlot, nil).assertStatus(t, http.StatusOK)
		if got, ok := slotHandlingUnit(t, get); ok {
			t.Fatalf("handlingUnit must be omitted, got %q", got)
		}
	})

	t.Run("registration accepts the optional unit", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		resp := ts.do(http.MethodPost, "/locations", map[string]any{
			"locationCode": huSlot, "locationType": "PalletRack", "handlingUnit": "PALLET",
		}).assertStatus(t, http.StatusCreated)
		if got, _ := slotHandlingUnit(t, resp); got != "PALLET" {
			t.Fatalf("handlingUnit = %q", got)
		}
	})

	t.Run("registration with an unknown unit is 422", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.do(http.MethodPost, "/locations", map[string]any{
			"locationCode": huSlot, "locationType": "PalletRack", "handlingUnit": "TRUCK",
		}).assertProblem(t, http.StatusUnprocessableEntity, "unknown-handling-unit")
	})

	t.Run("422 for an unknown or missing unit", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.seedSlot(huSlot, "PalletRack")
		for _, body := range []map[string]any{{"handlingUnit": "TRUCK"}, {"handlingUnit": "case"}, {"handlingUnit": ""}, {}} {
			ts.do(http.MethodPut, "/locations/"+huSlot+"/handling-unit", body).assertProblem(t, http.StatusUnprocessableEntity, "unknown-handling-unit")
		}
	})

	t.Run("400 for an unreadable body or a malformed code", func(t *testing.T) {
		ts := newTestServer(t)
		ts.doRaw(http.MethodPut, "/locations/"+huSlot+"/handling-unit", "{not json").assertStatus(t, http.StatusBadRequest)
		ts.do(http.MethodPut, "/locations/nope/handling-unit", map[string]any{"handlingUnit": "CASE"}).assertStatus(t, http.StatusBadRequest)
	})

	t.Run("404 for an unknown slot", func(t *testing.T) {
		ts := newTestServer(t)
		ts.do(http.MethodPut, "/locations/"+huSlot+"/handling-unit", map[string]any{"handlingUnit": "CASE"}).assertProblem(t, http.StatusNotFound, "location-slot-not-found")
	})

	t.Run("409 for a decommissioned slot", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.seedSlot(huSlot, "PalletRack")
		ts.do(http.MethodPost, "/locations/"+huSlot+"/decommission", nil).assertStatus(t, http.StatusNoContent)
		ts.do(http.MethodPut, "/locations/"+huSlot+"/handling-unit", map[string]any{"handlingUnit": "CASE"}).assertProblem(t, http.StatusConflict, "already-decommissioned")
	})
}
