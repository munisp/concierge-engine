package server

// Phase 58: rider preference profiles.
//
//	GET /v1/clients/{id}/ride-profile            typed comfort view (tenant)
//	GET /v1/track/{token}/preferences            client's own curated prefs (track token)
//	PUT /v1/track/{token}/preferences/{key}      self-serve set (track token)
//
// The track token IS the credential — the client edits only their own
// profile, and only curated/custom vocabulary passes validation.

import (
	"net/http"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase58(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/clients/{id}/ride-profile", h.rideProfile)
	mux.HandleFunc("GET /v1/track/{token}/preferences", h.trackPreferences)
	mux.HandleFunc("PUT /v1/track/{token}/preferences/{key}", h.trackSavePreference)
}

func (h *handlers) rideProfile(w http.ResponseWriter, r *http.Request) {
	rp, err := h.svc.Clients.RideProfile(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rp)
}

// trackClient resolves the track token to (tenantID, clientID).
func (h *handlers) trackClient(r *http.Request) (string, string, error) {
	tid, assignmentID, err := h.svc.Trips.ParseClientToken(r.PathValue("token"))
	if err != nil {
		return "", "", err
	}
	assignments, err := h.svc.Store.Dispatch().ListAssignments(r.Context(), tid)
	if err != nil {
		return "", "", err
	}
	for _, a := range assignments {
		if a.ID != assignmentID {
			continue
		}
		b, err := h.svc.Store.Bookings().Get(r.Context(), tid, a.BookingID)
		if err != nil {
			return "", "", err
		}
		sr, err := h.svc.Store.Requests().Get(r.Context(), tid, b.RequestID)
		if err != nil {
			return "", "", err
		}
		return tid, sr.ClientID, nil
	}
	return "", "", httpx.ErrNotFound
}

func (h *handlers) trackPreferences(w http.ResponseWriter, r *http.Request) {
	tid, cid, err := h.trackClient(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	rp, err := h.svc.Clients.RideProfile(r.Context(), tid, cid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rp)
}

func (h *handlers) trackSavePreference(w http.ResponseWriter, r *http.Request) {
	tid, cid, err := h.trackClient(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	p, err := h.svc.Clients.SavePreference(r.Context(), tid, cid, r.PathValue("key"),
		body.Value, "service", "client self-serve (track portal)", "client")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

var _ = domain.ValidatePreference // validation documented for readers
