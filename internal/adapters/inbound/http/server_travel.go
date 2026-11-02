package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// --------------------------------------------------- travel graph (ADR-0017) --

// handleRegisterCrossAisle declares a zone-scoped connection between two of
// its aisles at a bay ordinal, adding an edge to the zone's travel graph.
func (s *Server) handleRegisterCrossAisle(w http.ResponseWriter, r *http.Request) {
	zoneID := chi.URLParam(r, "zoneId")
	var req registerCrossAisleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	registered, err := s.RegisterCrossAisle.Execute(r.Context(), zoneID, req.FromAisle, req.ToAisle, req.AtBay)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toCrossAisleResponse(registered))
}

// handleListCrossAisles answers GET /zones/{zoneId}/cross-aisles
// (ADR-0017): every declared connection in the zone, ordered from-aisle,
// to-aisle, bay — the read half the POST already deserved.
func (s *Server) handleListCrossAisles(w http.ResponseWriter, r *http.Request) {
	limit, after, ok := pageParams(w, r)
	if !ok {
		return
	}
	connections, err := s.ListCrossAisles.Execute(r.Context(), chi.URLParam(r, "zoneId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]crossAisleResponse, 0, len(connections))
	for _, c := range connections {
		out = append(out, toCrossAisleResponse(c))
	}
	writePage(w, out, limit, after, crossAisleAt)
}

// handleGetZoneTravelGraph returns one zone's travel graph as nodes +
// edges, built fresh from its aisles, slots, and cross-aisles.
func (s *Server) handleGetZoneTravelGraph(w http.ResponseWriter, r *http.Request) {
	view, err := s.GetZoneTravelGraph.Execute(r.Context(), chi.URLParam(r, "zoneId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTravelGraphResponse(view))
}

// handleEstimateTravelDistance answers the shortest travel distance
// between two coded locations over the pure-domain travel graph. A
// malformed code is a 400 (it could never identify a location); two
// locations in different zones is a 422 (ErrNoRouteBetweenZones) — this
// phase's graph does not connect zones, so the endpoint refuses rather
// than guessing.
func (s *Server) handleEstimateTravelDistance(w http.ResponseWriter, r *http.Request) {
	from, err := shared.ParseLocationCode(r.URL.Query().Get("from"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	to, err := shared.ParseLocationCode(r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	distance, err := s.EstimateTravelDistance.Execute(r.Context(), from, to)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTravelDistanceResponse(distance))
}
