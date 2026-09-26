package http_test

import (
	"net/http"
	"testing"
)

func TestSetLocationGeometry(t *testing.T) {
	t.Run("sets geometry on an existing slot", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.seedSlot("WH1-STOR-AMB-A07-03-02-B", "PalletRack")

		body := map[string]any{
			"position":   map[string]any{"xM": 1.0, "yM": 2.0, "zM": 0.0},
			"dimensions": map[string]any{"widthM": 1.2, "depthM": 0.9, "heightM": 2.0},
		}
		resp := ts.do(http.MethodPut, "/locations/WH1-STOR-AMB-A07-03-02-B/geometry", body).assertStatus(t, http.StatusOK)

		var out struct {
			Position *struct {
				XM float64 `json:"xM"`
			} `json:"position"`
		}
		resp.decode(t, &out)
		if out.Position == nil || out.Position.XM != 1.0 {
			t.Fatalf("expected position to be set, got %+v", out.Position)
		}
	})

	t.Run("also accepts an explicit pick sequence", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.seedSlot("WH1-STOR-AMB-A07-03-02-B", "PalletRack")

		body := map[string]any{
			"position":     map[string]any{"xM": 1.0, "yM": 2.0, "zM": 0.0},
			"dimensions":   map[string]any{"widthM": 1.2, "depthM": 0.9, "heightM": 2.0},
			"pickSequence": 5,
		}
		resp := ts.do(http.MethodPut, "/locations/WH1-STOR-AMB-A07-03-02-B/geometry", body).assertStatus(t, http.StatusOK)

		var out struct {
			PickSequence *int `json:"pickSequence"`
		}
		resp.decode(t, &out)
		if out.PickSequence == nil || *out.PickSequence != 5 {
			t.Fatalf("expected pick sequence 5, got %v", out.PickSequence)
		}
	})

	t.Run("404 for an unknown slot", func(t *testing.T) {
		ts := newTestServer(t)
		body := map[string]any{
			"position":   map[string]any{"xM": 1.0, "yM": 2.0, "zM": 0.0},
			"dimensions": map[string]any{"widthM": 1.2, "depthM": 0.9, "heightM": 2.0},
		}
		ts.do(http.MethodPut, "/locations/WH1-STOR-AMB-A07-03-02-B/geometry", body).assertProblem(t, http.StatusNotFound, "location-slot-not-found")
	})

	t.Run("400 for a null geometry coordinate, not a silent zero", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.seedSlot("WH1-STOR-AMB-A07-03-02-B", "PalletRack")

		body := map[string]any{
			"position":   map[string]any{"xM": 1.0, "yM": 2.0, "zM": nil},
			"dimensions": map[string]any{"widthM": 1.2, "depthM": 0.9, "heightM": 2.0},
		}
		ts.do(http.MethodPut, "/locations/WH1-STOR-AMB-A07-03-02-B/geometry", body).
			assertProblem(t, http.StatusBadRequest, "missing-geometry-field")
	})

	t.Run("422 for zero dimensions", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		ts.seedSlot("WH1-STOR-AMB-A07-03-02-B", "PalletRack")

		body := map[string]any{
			"position":   map[string]any{"xM": 1.0, "yM": 2.0, "zM": 0.0},
			"dimensions": map[string]any{"widthM": 0, "depthM": 0, "heightM": 0},
		}
		ts.do(http.MethodPut, "/locations/WH1-STOR-AMB-A07-03-02-B/geometry", body).assertProblem(t, http.StatusUnprocessableEntity, "invalid-dimensions")
	})
}

func TestSetAisleGeometry(t *testing.T) {
	t.Run("sets a centreline on an existing aisle", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()

		body := map[string]any{
			"centreline": map[string]any{
				"start": map[string]any{"xM": 0.0, "yM": 0.0, "zM": 0.0},
				"end":   map[string]any{"xM": 10.0, "yM": 0.0, "zM": 0.0},
			},
		}
		resp := ts.do(http.MethodPut, "/zones/WH1-STOR-AMB/aisles/A07/geometry", body).assertStatus(t, http.StatusOK)

		var out struct {
			Centreline *struct {
				Start struct {
					XM float64 `json:"xM"`
				} `json:"start"`
			} `json:"centreline"`
		}
		resp.decode(t, &out)
		if out.Centreline == nil {
			t.Fatal("expected a centreline in the response")
		}
	})

	t.Run("404 for an unknown aisle", func(t *testing.T) {
		ts := newTestServer(t)
		body := map[string]any{
			"centreline": map[string]any{
				"start": map[string]any{"xM": 0.0, "yM": 0.0, "zM": 0.0},
				"end":   map[string]any{"xM": 10.0, "yM": 0.0, "zM": 0.0},
			},
		}
		ts.do(http.MethodPut, "/zones/WH1-STOR-AMB/aisles/A07/geometry", body).assertProblem(t, http.StatusNotFound, "aisle-not-found")
	})

	t.Run("422, never 500, for coincident centreline endpoints", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedStorageAisle()
		body := map[string]any{
			"centreline": map[string]any{
				"start": map[string]any{"xM": 5.0, "yM": 1.0, "zM": 0.0},
				"end":   map[string]any{"xM": 5.0, "yM": 1.0, "zM": 0.0},
			},
		}
		ts.do(http.MethodPut, "/zones/WH1-STOR-AMB/aisles/A07/geometry", body).
			assertProblem(t, http.StatusUnprocessableEntity, "segment-start-end-equal")
	})
}

func TestRegisterFixedStructure(t *testing.T) {
	t.Run("registers a structure and lists it back", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()

		body := map[string]any{
			"id":   "STR-1",
			"kind": "Wall",
			"footprint": map[string]any{
				"origin": map[string]any{"xM": 10.0, "yM": 20.0, "zM": 0.0},
				"size":   map[string]any{"widthM": 1.0, "depthM": 1.0, "heightM": 3.0},
			},
			"label": "North wall",
		}
		resp := ts.do(http.MethodPost, "/sites/WH1/structures", body).assertStatus(t, http.StatusCreated)
		if loc := resp.headers.Get("Location"); loc != "/sites/WH1/structures/STR-1" {
			t.Fatalf("unexpected Location header %q", loc)
		}

		var created struct {
			ID string `json:"id"`
		}
		resp.decode(t, &created)
		if created.ID != "STR-1" {
			t.Fatalf("unexpected id %q", created.ID)
		}

		listResp := ts.do(http.MethodGet, "/sites/WH1/structures", nil).assertStatus(t, http.StatusOK)
		var list []struct {
			ID string `json:"id"`
		}
		listResp.decode(t, &list)
		if len(list) != 1 || list[0].ID != "STR-1" {
			t.Fatalf("expected one structure STR-1, got %v", list)
		}
	})

	t.Run("mints a UUID when id is omitted", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()

		body := map[string]any{
			"kind": "Column",
			"footprint": map[string]any{
				"origin": map[string]any{"xM": 10.0, "yM": 20.0, "zM": 0.0},
				"size":   map[string]any{"widthM": 1.0, "depthM": 1.0, "heightM": 3.0},
			},
			"label": "Column A",
		}
		resp := ts.do(http.MethodPost, "/sites/WH1/structures", body).assertStatus(t, http.StatusCreated)
		var created struct {
			ID string `json:"id"`
		}
		resp.decode(t, &created)
		if created.ID == "" {
			t.Fatal("expected a minted id")
		}
	})

	t.Run("404 for an unknown site", func(t *testing.T) {
		ts := newTestServer(t)
		body := map[string]any{
			"kind": "Wall",
			"footprint": map[string]any{
				"origin": map[string]any{"xM": 10.0, "yM": 20.0, "zM": 0.0},
				"size":   map[string]any{"widthM": 1.0, "depthM": 1.0, "heightM": 3.0},
			},
			"label": "North wall",
		}
		ts.do(http.MethodPost, "/sites/WH1/structures", body).assertProblem(t, http.StatusNotFound, "site-not-found")
	})

	t.Run("422 for an unknown kind", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		body := map[string]any{
			"kind": "Elevator",
			"footprint": map[string]any{
				"origin": map[string]any{"xM": 10.0, "yM": 20.0, "zM": 0.0},
				"size":   map[string]any{"widthM": 1.0, "depthM": 1.0, "heightM": 3.0},
			},
			"label": "?",
		}
		ts.do(http.MethodPost, "/sites/WH1/structures", body).assertProblem(t, http.StatusUnprocessableEntity, "unknown-fixed-structure-kind")
	})

	t.Run("400 for a null footprint dimension, not a silent zero", func(t *testing.T) {
		ts := newTestServer(t)
		ts.seedSite()
		body := map[string]any{
			"kind": "Wall",
			"footprint": map[string]any{
				"origin": map[string]any{"xM": 10.0, "yM": 20.0, "zM": 0.0},
				"size":   map[string]any{"widthM": 1.0, "depthM": nil, "heightM": 3.0},
			},
			"label": "North wall",
		}
		ts.do(http.MethodPost, "/sites/WH1/structures", body).
			assertProblem(t, http.StatusBadRequest, "missing-geometry-field")
	})
}

func TestGetSiteLayoutIncludesFixedStructures(t *testing.T) {
	ts := newTestServer(t)
	ts.seedStorageAisle()
	ts.seedSlot("WH1-STOR-AMB-A07-03-02-B", "PalletRack")

	body := map[string]any{
		"id":   "STR-1",
		"kind": "Wall",
		"footprint": map[string]any{
			"origin": map[string]any{"xM": 10.0, "yM": 20.0, "zM": 0.0},
			"size":   map[string]any{"widthM": 1.0, "depthM": 1.0, "heightM": 3.0},
		},
		"label": "North wall",
	}
	ts.do(http.MethodPost, "/sites/WH1/structures", body).assertStatus(t, http.StatusCreated)

	resp := ts.do(http.MethodGet, "/sites/WH1/layout", nil).assertStatus(t, http.StatusOK)
	var out struct {
		FixedStructures []struct {
			ID string `json:"id"`
		} `json:"fixedStructures"`
	}
	resp.decode(t, &out)
	if len(out.FixedStructures) != 1 || out.FixedStructures[0].ID != "STR-1" {
		t.Fatalf("expected the registered structure in the layout, got %v", out.FixedStructures)
	}
}
