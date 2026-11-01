package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// -------------------------------------------------------- location types ---

func (s *Server) handleRegisterLocationType(w http.ResponseWriter, r *http.Request) {
	var req registerLocationTypeRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	roleName := req.Role
	if roleName == "" {
		roleName = string(placement.Storage)
	}
	role, err := placement.ParseLocationRole(roleName)
	if err != nil {
		writeError(w, r, err)
		return
	}

	capacity := shared.Capacity{}
	if req.DefaultCapacity.MaxWeightKg != 0 || req.DefaultCapacity.MaxVolumeM3 != 0 {
		capacity, err = shared.NewCapacity(req.DefaultCapacity.MaxWeightKg, req.DefaultCapacity.MaxVolumeM3)
		if err != nil {
			writeError(w, r, err)
			return
		}
	}

	registered, err := s.RegisterLocationType.Execute(r.Context(), req.Name, role, capacity)
	if err != nil {
		writeError(w, r, err)
		return
	}

	w.Header().Set("Location", "/location-types/"+registered.Name())
	writeJSON(w, http.StatusCreated, toLocationTypeResponse(registered))
}

func (s *Server) handleListLocationTypes(w http.ResponseWriter, r *http.Request) {
	limit, after, ok := pageParams(w, r)
	if !ok {
		return
	}
	types, err := s.ListLocationTypes.Execute(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]locationTypeResponse, 0, len(types))
	for _, t := range types {
		out = append(out, toLocationTypeResponse(t))
	}
	writePage(w, out, limit, after, locationTypeAt)
}

func (s *Server) handleGetLocationType(w http.ResponseWriter, r *http.Request) {
	found, err := s.GetLocationType.Execute(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLocationTypeResponse(found))
}

// -------------------------------------------------------- placement rules --

func (s *Server) handleDefinePlacementRule(w http.ResponseWriter, r *http.Request) {
	var req definePlacementRuleRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	effect, err := placement.ParseEffect(req.Effect)
	if err != nil {
		writeError(w, r, err)
		return
	}
	predicate, err := placement.NewZonePredicate(req.Zone.ZoneCode, shared.TemperatureClass(req.Zone.TemperatureClass), req.Zone.Hazmat)
	if err != nil {
		writeError(w, r, err)
		return
	}

	defined, err := s.DefinePlacementRule.Execute(r.Context(), req.RuleID, req.LocationType, effect, predicate)
	if err != nil {
		writeError(w, r, err)
		return
	}

	w.Header().Set("Location", "/placement-rules/"+defined.ID())
	writeJSON(w, http.StatusCreated, toPlacementRuleResponse(defined))
}

func (s *Server) handleListPlacementRules(w http.ResponseWriter, r *http.Request) {
	limit, after, ok := pageParams(w, r)
	if !ok {
		return
	}
	rules, err := s.ListPlacementRules.Execute(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]placementRuleResponse, 0, len(rules))
	for _, rule := range rules {
		out = append(out, toPlacementRuleResponse(rule))
	}
	writePage(w, out, limit, after, placementRuleAt)
}

func (s *Server) handleGetPlacementRule(w http.ResponseWriter, r *http.Request) {
	found, err := s.GetPlacementRule.Execute(r.Context(), chi.URLParam(r, "ruleId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toPlacementRuleResponse(found))
}
