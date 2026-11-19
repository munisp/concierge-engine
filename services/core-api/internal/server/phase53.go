package server

// Phase 53: visual campaign builder API (Mautic feature-borrow A1).
// The console canvas edits the Phase 35 drip engine through these
// endpoints — same ledger-derived progress the sweep acts on.
//
//	GET    /v1/outreach/sequences           definitions + live per-node counters
//	PUT    /v1/outreach/sequences/{name}    upsert (validated BEFORE persist)
//	DELETE /v1/outreach/sequences/{name}    remove (ledger history survives)
//	POST   /v1/outreach/sequences/run       on-demand sweep (builder "run now")

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (h *handlers) registerPhase53(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/outreach/sequences", h.sequencesList)
	mux.HandleFunc("PUT /v1/outreach/sequences/{name}", h.sequenceUpsert)
	mux.HandleFunc("DELETE /v1/outreach/sequences/{name}", h.sequenceDelete)
	mux.HandleFunc("POST /v1/outreach/sequences/run", h.sequencesRun)
}

func (h *handlers) sequencesList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.Sequences.Progress(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sequences": rows})
}

func (h *handlers) sequenceUpsert(w http.ResponseWriter, r *http.Request) {
	var seq model.Sequence
	if err := httpx.Decode(r, &seq); err != nil {
		httpx.WriteError(w, err)
		return
	}
	name := r.PathValue("name")
	if seq.Name != "" && seq.Name != name {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	seq.Name = name
	if err := h.svc.Sequences.UpsertSequence(r.Context(), tenantOf(r), seq); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, seq)
}

func (h *handlers) sequenceDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Sequences.DeleteSequence(r.Context(), tenantOf(r), r.PathValue("name")); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) sequencesRun(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Sequences.Run(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
