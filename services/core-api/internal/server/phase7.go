package server

import (
	"net/http"
	"strconv"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
)

// Phase 11 channel receipts: delivery/read status callbacks from the
// channel adapters (SLA evidence), queryable per channel message id.
func (h *handlers) registerPhase7(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/channels/receipts", h.recordReceipt)
	mux.HandleFunc("GET /v1/channels/receipts", h.listReceipts)
	mux.HandleFunc("GET /v1/channels/receipts-counts", h.receiptCounts)
	// Phase 12: SLA board surfaces.
	mux.HandleFunc("GET /v1/channels/receipts-failures", h.receiptFailures)
	mux.HandleFunc("GET /v1/channels/receipts-case", h.receiptsForCase)
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

// receiptFailures: recent failed deliveries correlated to cases (SLA board).
func (h *handlers) receiptFailures(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	out, err := h.svc.Receipts.Failures(r.Context(), tenantOf(r), limit)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// receiptsForCase: full delivery evidence for one case — every linked
// channel message id with its ordered receipt lifecycle.
func (h *handlers) receiptsForCase(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Receipts.ForCase(r.Context(), tenantOf(r), r.URL.Query().Get("case_id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
