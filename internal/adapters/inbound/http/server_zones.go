package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// ---------------------------------------------------------------- zones ----

func (s *Server) handleRegisterZone(w http.ResponseWriter, r *http.Request) {
	var req registerZoneRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	temperatureClass, err := shared.ParseTemperatureClass(req.TemperatureClass)
	if err != nil {
		writeError(w, r, err)
		return
	}

	registered, err := s.RegisterZone.ExecuteWithGLN(r.Context(), chi.URLParam(r, "siteCode"), req.AreaCode, req.ZoneCode, temperatureClass, req.Hazmat, req.BayPitchM, req.LevelPitchM, req.GLN)
	if err != nil {
		writeError(w, r, err)
		return
	}

	w.Header().Set("Location", "/zones/"+registered.ID())
	writeJSON(w, http.StatusCreated, toZoneResponse(registered))
}

func (s *Server) handleListZones(w http.ResponseWriter, r *http.Request) {
	limit, after, ok := pageParams(w, r)
	if !ok {
		return
	}
	zones, err := s.ListZones.Execute(r.Context(), chi.URLParam(r, "siteCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]zoneResponse, 0, len(zones))
	for _, z := range zones {
		out = append(out, toZoneResponse(z))
	}
	writePage(w, out, limit, after, zoneAt)
}

func (s *Server) handleGetZone(w http.ResponseWriter, r *http.Request) {
	found, err := s.GetZone.Execute(r.Context(), chi.URLParam(r, "zoneId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toZoneResponse(found))
}

// --------------------------------------------------------------- aisles ----

func (s *Server) handleRegisterAisle(w http.ResponseWriter, r *http.Request) {
	var req registerAisleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.SequenceHint == nil {
		writeError(w, r, errMissingSequenceHint)
		return
	}

	direction, err := shared.ParseDirection(req.Direction)
	if err != nil {
		writeError(w, r, err)
		return
	}

	zoneID := chi.URLParam(r, "zoneId")
	registered, err := s.RegisterAisle.Execute(r.Context(), zoneID, req.AisleCode, *req.SequenceHint, direction)
	if err != nil {
		writeError(w, r, err)
		return
	}

	w.Header().Set("Location", "/zones/"+zoneID+"/aisles/"+registered.AisleCode())
	writeJSON(w, http.StatusCreated, toAisleResponse(registered))
}

func (s *Server) handleListAisles(w http.ResponseWriter, r *http.Request) {
	limit, after, ok := pageParams(w, r)
	if !ok {
		return
	}
	aisles, err := s.ListAisles.Execute(r.Context(), chi.URLParam(r, "zoneId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]aisleResponse, 0, len(aisles))
	for _, a := range aisles {
		out = append(out, toAisleResponse(a))
	}
	writePage(w, out, limit, after, aisleAt)
}

func (s *Server) handleGetAisle(w http.ResponseWriter, r *http.Request) {
	aisleID := chi.URLParam(r, "zoneId") + "-" + chi.URLParam(r, "aisleCode")
	found, err := s.GetAisle.Execute(r.Context(), aisleID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAisleResponse(found))
}
