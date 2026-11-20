package server

// Phase 62: chauffeur matching endpoints.
//
//	GET    /v1/bookings/{id}/crew-suggestions          ranked, explained candidates
//	POST   /v1/bookings/{id}/assign-matched            assign the top-ranked chauffeur
//	GET    /v1/clients/{id}/chauffeur-links            rider's favorite/blocked marks
//	PUT    /v1/clients/{id}/chauffeur-links/{crewID}   console sets a mark
//	DELETE /v1/clients/{id}/chauffeur-links/{crewID}   console clears a mark
//	PUT    /v1/track/{token}/chauffeur                 rider self-serve mark (trip's chauffeur)

import (
	"encoding/json"
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase62(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/bookings/{id}/crew-suggestions", h.crewSuggestions)
	mux.HandleFunc("POST /v1/bookings/{id}/assign-matched", h.assignMatched)
	mux.HandleFunc("GET /v1/clients/{id}/chauffeur-links", h.listChauffeurLinks)
	mux.HandleFunc("PUT /v1/clients/{id}/chauffeur-links/{crewID}", h.setChauffeurLink)
	mux.HandleFunc("DELETE /v1/clients/{id}/chauffeur-links/{crewID}", h.clearChauffeurLink)
	mux.HandleFunc("PUT /v1/track/{token}/chauffeur", h.trackChauffeurLink)
}

func (h *handlers) crewSuggestions(w http.ResponseWriter, r *http.Request) {
	ranked, err := h.svc.Dispatch.SuggestCrew(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"candidates": ranked})
}

func (h *handlers) assignMatched(w http.ResponseWriter, r *http.Request) {
	a, err := h.svc.Dispatch.AssignMatched(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"assignment": a})
}

func (h *handlers) listChauffeurLinks(w http.ResponseWriter, r *http.Request) {
	links, err := h.svc.Dispatch.ChauffeurLinks(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"links": links})
}

func (h *handlers) setChauffeurLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	l, err := h.svc.Dispatch.SetChauffeurLink(r.Context(), tenantOf(r), r.PathValue("id"), r.PathValue("crewID"), body.Kind)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"link": l})
}

func (h *handlers) clearChauffeurLink(w http.ResponseWriter, r *http.Request) {
	if _, err := h.svc.Dispatch.SetChauffeurLink(r.Context(), tenantOf(r), r.PathValue("id"), r.PathValue("crewID"), ""); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusNoContent, nil)
}

// trackChauffeurLink lets the rider mark the chauffeur of THIS trip —
// the token proves the trip, the assignment names the chauffeur.
func (h *handlers) trackChauffeurLink(w http.ResponseWriter, r *http.Request) {
	tid, clientID, err := h.trackClient(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	_, assignmentID, err := h.svc.Trips.ParseClientToken(r.PathValue("token"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var crewID string
	assignments, err := h.svc.Store.Dispatch().ListAssignments(r.Context(), tid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	for _, a := range assignments {
		if a.ID == assignmentID {
			crewID = a.CrewMemberID
		}
	}
	if crewID == "" {
		httpx.WriteError(w, httpx.ErrNotFound) // no chauffeur on this trip
		return
	}
	var body struct {
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	l, err := h.svc.Dispatch.SetChauffeurLink(r.Context(), tid, clientID, crewID, body.Kind)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"link": l})
}
