package server

// Phase 61: journey orchestration.
//
//	GET  /v1/clients/{id}/journey              rider's leg timeline (tenant API)
//	POST /v1/clients/{id}/journey/legs         add a leg (flight resolves via GDS)
//	POST /v1/bookings/{id}/journey/link        chain the booking's ground leg to a flight leg
//	POST /v1/journeys/legs/{id}/flight-update  apply a status report (operator; the watch's path)
//	POST /v1/journeys/watch                    one flight-watch pass, all tenants
//	GET  /v1/track/{token}/journey             rider's journey on the tracking page

import (
	"encoding/json"
	"net/http"
	"time"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (h *handlers) registerPhase61(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/clients/{id}/journey", h.clientJourney)
	mux.HandleFunc("POST /v1/clients/{id}/journey/legs", h.addJourneyLeg)
	mux.HandleFunc("POST /v1/bookings/{id}/journey/link", h.linkGroundLeg)
	mux.HandleFunc("POST /v1/journeys/legs/{id}/flight-update", h.flightUpdate)
	mux.HandleFunc("POST /v1/journeys/watch", h.journeyWatch)
	mux.HandleFunc("GET /v1/track/{token}/journey", h.trackJourney)
}

func (h *handlers) clientJourney(w http.ResponseWriter, r *http.Request) {
	legs, err := h.svc.Journeys.Journey(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"legs": legs})
}

func (h *handlers) addJourneyLeg(w http.ResponseWriter, r *http.Request) {
	var in domain.LegInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	leg, err := h.svc.Journeys.AddLeg(r.Context(), tenantOf(r), r.PathValue("id"), in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"leg": leg})
}

func (h *handlers) linkGroundLeg(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FlightLegID string `json:"flight_leg_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	leg, err := h.svc.Journeys.LinkGroundLeg(r.Context(), tenantOf(r), r.PathValue("id"), body.FlightLegID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"leg": leg})
}

// flightUpdate is the operator/simulated path AND the documented contract
// the watch uses internally — a status report in, cascade out.
func (h *handlers) flightUpdate(w http.ResponseWriter, r *http.Request) {
	var rep model.FlightStatusReport
	if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	if rep.Source == "" {
		rep.Source = "manual"
	}
	if rep.Status == "" {
		rep.Status = model.LegScheduled
	}
	leg, err := h.svc.Journeys.ApplyFlightUpdate(r.Context(), tenantOf(r), r.PathValue("id"), &rep)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"leg": leg})
}

func (h *handlers) journeyWatch(w http.ResponseWriter, r *http.Request) {
	watched, updated, err := h.svc.Journeys.WatchAllTenants(r.Context(), 24*time.Hour)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"watched": watched, "updated": updated})
}

func (h *handlers) trackJourney(w http.ResponseWriter, r *http.Request) {
	tid, clientID, err := h.trackClient(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	legs, err := h.svc.Journeys.Journey(r.Context(), tid, clientID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"legs": legs})
}
