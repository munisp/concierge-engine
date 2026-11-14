package server

import (
	"net/http"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
)

// Phase 11 channel receipts: delivery/read status callbacks from the
// channel adapters (SLA evidence), queryable per channel message id.
func (h *handlers) registerPhase7(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/channels/receipts", h.recordReceipt)
	mux.HandleFunc("GET /v1/channels/receipts", h.listReceipts)
	mux.HandleFunc("GET /v1/channels/receipts-counts", h.receiptCounts)
}

func (h *handlers) recordReceipt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Channel     string `json:"channel"`
		ExternalRef string `json:"external_ref"`
		Status      string `json:"status"`
		At          string `json:"at"` // RFC3339 or epoch seconds; zero = server time
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	var at time.Time
	if body.At != "" {
		if t, err := time.Parse(time.RFC3339, body.At); err == nil {
			at = t
		} else if epoch, err := time.ParseDuration(body.At + "s"); err == nil {
			at = time.Unix(int64(epoch.Seconds()), 0)
		}
	}
	rc, err := h.svc.Receipts.Record(r.Context(), tenantOf(r), body.Channel,
		body.ExternalRef, body.Status, at)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, rc)
}

func (h *handlers) listReceipts(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Receipts.For(r.Context(), tenantOf(r), r.URL.Query().Get("external_ref"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *handlers) receiptCounts(w http.ResponseWriter, r *http.Request) {
	counts, err := h.svc.Receipts.Counts(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, counts)
}
