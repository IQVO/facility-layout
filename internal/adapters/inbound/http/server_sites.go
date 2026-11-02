package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/facility-layout/internal/domain/placement"
)

// ---------------------------------------------------------------- sites ----

func (s *Server) handleRegisterSite(w http.ResponseWriter, r *http.Request) {
	var req registerSiteRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	role, err := fromRegisterSiteRole(req.SiteRole)
	if err != nil {
		writeError(w, r, err)
		return
	}
	registered, err := s.RegisterSite.Execute(r.Context(), req.SiteCode, req.Name, role)
	if err != nil {
		writeError(w, r, err)
		return
	}

	w.Header().Set("Location", "/sites/"+registered.Code())
	writeJSON(w, http.StatusCreated, toSiteResponse(registered))
}

func (s *Server) handleListSites(w http.ResponseWriter, r *http.Request) {
	limit, after, ok := pageParams(w, r)
	if !ok {
		return
	}
	sites, err := s.ListSites.Execute(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]siteResponse, 0, len(sites))
	for _, site := range sites {
		out = append(out, toSiteResponse(site))
	}
	writePage(w, out, limit, after, siteAt)
}

func (s *Server) handleGetSite(w http.ResponseWriter, r *http.Request) {
	found, err := s.GetSite.Execute(r.Context(), chi.URLParam(r, "siteCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toSiteResponse(found))
}

// handleGetSiteTransferCapability returns a site's current transfer
// capability and the revision of its last SiteCapabilityChanged (ADR-0035).
func (s *Server) handleGetSiteTransferCapability(w http.ResponseWriter, r *http.Request) {
	found, err := s.GetSite.Execute(r.Context(), chi.URLParam(r, "siteCode"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransferCapabilityResponse(found))
}

// handleChangeSiteTransferCapability replaces a site's transfer capability
// with the full snapshot in the body (ADR-0035). 200 with the resulting
// state; a repeat of the current snapshot is a 200 no-op. A concurrent
// change of the same site loses with 409 concurrent-modification.
func (s *Server) handleChangeSiteTransferCapability(w http.ResponseWriter, r *http.Request) {
	var req setTransferCapabilityRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	capability, err := fromTransferCapabilityRequest(req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	updated, err := s.ChangeSiteTransferCapability.Execute(r.Context(), chi.URLParam(r, "siteCode"), capability)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransferCapabilityResponse(updated))
}

// handleChangeSiteRole sets a site's role in the fulfilment network
// (ADR-0037). 200 with the site; a repeat of the current role is a 200
// no-op. UNSPECIFIED, an unknown or a missing role is 422
// invalid-site-role; a concurrent change of the same site loses with 409
// concurrent-modification. The transfer capability is never touched.
func (s *Server) handleChangeSiteRole(w http.ResponseWriter, r *http.Request) {
	var req setSiteRoleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	role, err := fromSetSiteRoleRequest(req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	updated, err := s.ChangeSiteRole.Execute(r.Context(), chi.URLParam(r, "siteCode"), role)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toSiteResponse(updated))
}

// handleListSiteRoleTransferDefaults returns the role -> default transfer
// capability table (ADR-0037): the one source of truth the warehouse-infra
// seed and operators read instead of hard-coding it.
func (s *Server) handleListSiteRoleTransferDefaults(w http.ResponseWriter, r *http.Request) {
	limit, after, ok := pageParams(w, r)
	if !ok {
		return
	}
	rows := toSiteRoleTransferDefaultsResponse(s.ListSiteRoleTransferDefaults.Execute(r.Context()))
	writePage(w, rows, limit, after, siteRoleDefaultAt(rows))
}

// handleListLocationsByRole answers "where are this site's dock doors" (or
// any other LocationRole) without walking the full nested site layout
// (ADR-0016). The role query parameter is required.
func (s *Server) handleListLocationsByRole(w http.ResponseWriter, r *http.Request) {
	limit, after, ok := pageParams(w, r, "role")
	if !ok {
		return
	}
	role, err := placement.ParseLocationRole(r.URL.Query().Get("role"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	slots, err := s.ListLocationsByRole.Execute(r.Context(), chi.URLParam(r, "siteCode"), role)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]locationSlotResponse, 0, len(slots))
	for _, sl := range slots {
		out = append(out, toLocationSlotResponse(sl))
	}
	writePage(w, out, limit, after, locationSlotAt)
}
