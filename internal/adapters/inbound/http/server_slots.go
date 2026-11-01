package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// ------------------------------------------------------- location slots ----

func (s *Server) handleRegisterLocationSlot(w http.ResponseWriter, r *http.Request) {
	var req registerLocationSlotRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	code, err := shared.ParseLocationCode(req.LocationCode)
	if err != nil {
		writeError(w, r, err)
		return
	}

	override := shared.Capacity{}
	if req.CapacityOverride != nil {
		override, err = shared.NewCapacity(req.CapacityOverride.MaxWeightKg, req.CapacityOverride.MaxVolumeM3)
		if err != nil {
			writeError(w, r, err)
			return
		}
	}

	registered, err := s.RegisterLocationSlot.ExecuteWithHandlingUnit(r.Context(), code, req.LocationType, override, req.DockFlow, req.Activities, req.HandlingUnit)
	if err != nil {
		writeError(w, r, err)
		return
	}

	w.Header().Set("Location", "/locations/"+registered.Code().String())
	writeJSON(w, http.StatusCreated, toLocationSlotResponse(registered))
}

func (s *Server) handleGetLocationSlot(w http.ResponseWriter, r *http.Request) {
	code, err := shared.ParseLocationCode(chi.URLParam(r, "locationCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	found, err := s.GetLocationSlot.Execute(r.Context(), code)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLocationSlotResponse(found))
}

// handleGetLocationClassification resolves a LocationSlot's LocationCode to
// its parent Zone and returns that Zone's Hazmat/TemperatureClass
// attributes — the Open Host Service / Published Language realization that
// lets inventory-storage (and future consumers) validate placement of a
// classified product without duplicating Zone data of its own. Zone
// remains the sole domain aggregate that owns these attributes; this
// handler only reads and joins.
func (s *Server) handleGetLocationClassification(w http.ResponseWriter, r *http.Request) {
	code, err := shared.ParseLocationCode(chi.URLParam(r, "locationCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	found, err := s.GetLocationClassification.Execute(r.Context(), code)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLocationClassificationResponse(found))
}

func (s *Server) handleDecommissionLocationSlot(w http.ResponseWriter, r *http.Request) {
	code, err := shared.ParseLocationCode(chi.URLParam(r, "locationCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.DecommissionLocationSlot.Execute(r.Context(), code); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleImportFacilityLayout answers 200 OK, not 201: a bulk import is a
// partial-success report over many rows, not the creation of one
// addressable resource, so there is no single Location to hand back. The
// per-row outcome is in the body.
func (s *Server) handleImportFacilityLayout(w http.ResponseWriter, r *http.Request) {
	var req []importRowRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	rows := make([]usecases.ImportRow, 0, len(req))
	for _, row := range req {
		rows = append(rows, toImportRow(row))
	}

	report, err := s.ImportFacilityLayout.Execute(r.Context(), rows)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toImportReportResponse(report))
}
