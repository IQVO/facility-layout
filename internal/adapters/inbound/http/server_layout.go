package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/structure"
)

// --------------------------------------------- "draw the warehouse" reads --

func (s *Server) handleGetSiteLayout(w http.ResponseWriter, r *http.Request) {
	// An ABSENT `format` defaults to json; a PRESENT one must be one of the
	// enum values. `?format=` (present but empty) is not in the published
	// enum [json, svg] and is rejected -- the Schemathesis contract job
	// found it answered 200.
	format := "json"
	vals, present := r.URL.Query()["format"]
	if present {
		format = vals[0]
	}
	if format != "json" && format != "svg" {
		writeProblem(w, http.StatusBadRequest, problemInfo{"invalid-layout-format", "Layout format must be json or svg"}, "layout format must be \"json\" or \"svg\", got "+strconv.Quote(format), r.URL.Path)
		return
	}

	layout, err := s.GetSiteLayout.Execute(r.Context(), chi.URLParam(r, "siteCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}

	if format == "svg" {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(renderLayoutSVG(layout)))
		return
	}

	writeJSON(w, http.StatusOK, toSiteLayoutResponse(layout))
}

func (s *Server) handleGetZoneGrid(w http.ResponseWriter, r *http.Request) {
	grid, err := s.GetZoneGrid.Execute(r.Context(), chi.URLParam(r, "zoneId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toZoneGridResponse(grid))
}

// --------------------------------------------------------- geometry (ADR-0017) --

func (s *Server) handleSetLocationGeometry(w http.ResponseWriter, r *http.Request) {
	code, err := shared.ParseLocationCode(chi.URLParam(r, "locationCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req setLocationGeometryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	position, err := fromPoint3DRequest(req.Position)
	if err != nil {
		writeError(w, r, err)
		return
	}
	dimensions, err := fromDimensionsRequest(req.Dimensions)
	if err != nil {
		writeError(w, r, err)
		return
	}
	updated, err := s.SetLocationGeometry.Execute(r.Context(), code, position, dimensions, req.PickSequence)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLocationSlotResponse(updated))
}

// handleDeclareLocationHandlingUnit sets or changes a slot's handling unit
// (ADR-0040) and returns the updated slot.
func (s *Server) handleDeclareLocationHandlingUnit(w http.ResponseWriter, r *http.Request) {
	code, err := shared.ParseLocationCode(chi.URLParam(r, "locationCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req declareHandlingUnitRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	updated, err := s.DeclareLocationHandlingUnit.Execute(r.Context(), code, req.HandlingUnit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLocationSlotResponse(updated))
}

func (s *Server) handleSetAisleGeometry(w http.ResponseWriter, r *http.Request) {
	aisleID := chi.URLParam(r, "zoneId") + "-" + chi.URLParam(r, "aisleCode")
	var req setAisleGeometryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	centreline, err := fromSegmentRequest(req.Centreline)
	if err != nil {
		writeError(w, r, err)
		return
	}
	updated, err := s.SetAisleGeometry.Execute(r.Context(), aisleID, centreline)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAisleResponse(updated))
}

// handleRegisterFixedStructure registers a site-scoped physical obstacle
// (wall, column, office, conveyor, or other). id is caller-supplied, the
// same pattern PlacementRule ids follow: when the caller omits it, a UUID
// is minted here.
func (s *Server) handleRegisterFixedStructure(w http.ResponseWriter, r *http.Request) {
	siteCode := chi.URLParam(r, "siteCode")
	var req registerFixedStructureRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	kind, err := structure.ParseKind(req.Kind)
	if err != nil {
		writeError(w, r, err)
		return
	}
	footprint, err := fromRectRequest(req.Footprint)
	if err != nil {
		writeError(w, r, err)
		return
	}
	id := req.ID
	if id == "" {
		id = uuid.NewString()
	}

	registered, err := s.RegisterFixedStructure.Execute(r.Context(), id, siteCode, kind, footprint, req.Label)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/sites/"+siteCode+"/structures/"+registered.ID())
	writeJSON(w, http.StatusCreated, toFixedStructureResponse(registered))
}

func (s *Server) handleListFixedStructures(w http.ResponseWriter, r *http.Request) {
	limit, after, ok := pageParams(w, r)
	if !ok {
		return
	}
	structures, err := s.ListFixedStructures.Execute(r.Context(), chi.URLParam(r, "siteCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]fixedStructureResponse, 0, len(structures))
	for _, f := range structures {
		out = append(out, toFixedStructureResponse(f))
	}
	writePage(w, out, limit, after, fixedStructureAt)
}
