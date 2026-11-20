package server

// Phase 60: ride modes & cabin pre-conditioning.
//
//	GET  /v1/ride-modes                       tenant-effective presets (house + overrides)
//	POST /v1/clients/{id}/ride-mode           apply a mode to a rider (tenant API)
//	POST /v1/track/{token}/ride-mode          rider self-serve mode picker (track token)
//
// Applying a mode expands the preset through the validated preference
// path — every write is consent-scoped and audit-logged with provenance
// "mode:{mode}".

import (
	"encoding/json"
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase60(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/ride-modes", h.listRideModes)
	mux.HandleFunc("POST /v1/clients/{id}/ride-mode", h.applyRideMode)
	mux.HandleFunc("POST /v1/track/{token}/ride-mode", h.trackApplyRideMode)
}

func (h *handlers) listRideModes(w http.ResponseWriter, r *http.Request) {
	modes, err := h.svc.Clients.EffectiveModes(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"modes": modes})
}

type rideModeBody struct {
	Mode string `json:"mode"`
}

func (h *handlers) applyRideMode(w http.ResponseWriter, r *http.Request) {
	var body rideModeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	applied, err := h.svc.Clients.ApplyMode(r.Context(), tenantOf(r), r.PathValue("id"), body.Mode, "console")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"applied": applied})
}

func (h *handlers) trackApplyRideMode(w http.ResponseWriter, r *http.Request) {
	tid, clientID, err := h.trackClient(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var body rideModeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	applied, err := h.svc.Clients.ApplyMode(r.Context(), tid, clientID, body.Mode, "self-serve")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"applied": applied})
}
