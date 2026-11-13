// Phase 4 API surface: next-generation case management layered on the CRM.
// Cases are the accountable unit of concierge work — owner, priority, SLA
// clock, tasks, timeline notes, and AI assist (triage/summary/next-action)
// with engine provenance on every AI-produced field.
package server

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase4(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/cases", h.openCase)
	mux.HandleFunc("GET /v1/cases", h.listCases)
	mux.HandleFunc("GET /v1/cases/metrics", h.caseMetrics)
	mux.HandleFunc("GET /v1/cases/{id}", h.getCase)
	mux.HandleFunc("POST /v1/cases/{id}/assign", h.assignCase)
	mux.HandleFunc("POST /v1/cases/{id}/transition", h.transitionCase)
	mux.HandleFunc("POST /v1/cases/{id}/link", h.linkCase)
	mux.HandleFunc("POST /v1/cases/{id}/tasks", h.addCaseTask)
	mux.HandleFunc("GET /v1/cases/{id}/tasks", h.listCaseTasks)
	mux.HandleFunc("POST /v1/cases/{id}/tasks/{taskID}/complete", h.completeCaseTask)
	mux.HandleFunc("POST /v1/cases/{id}/notes", h.addCaseNote)
	mux.HandleFunc("GET /v1/cases/{id}/notes", h.listCaseNotes)
	mux.HandleFunc("GET /v1/cases/{id}/summary", h.caseSummary)
	mux.HandleFunc("GET /v1/cases/{id}/next-action", h.caseNextAction)
	mux.HandleFunc("POST /v1/cases/sla-scan", h.caseSLAScan)
}

func (h *handlers) openCase(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientID       string `json:"client_id"`
		Title          string `json:"title"`
		Text           string `json:"text"` // inbound description; triaged by AI
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	c, err := h.svc.Cases.Open(r.Context(), tenantOf(r), body.ClientID, body.Title, body.Text, body.IdempotencyKey)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, c)
}

func (h *handlers) listCases(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := h.svc.Cases.List(r.Context(), tenantOf(r), q.Get("state"), q.Get("kind"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) caseMetrics(w http.ResponseWriter, r *http.Request) {
	m, err := h.svc.Cases.Metrics(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, m)
}

func (h *handlers) getCase(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.Cases.Get(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *handlers) assignCase(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OwnerID string `json:"owner_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	c, err := h.svc.Cases.Assign(r.Context(), tenantOf(r), r.PathValue("id"), body.OwnerID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *handlers) transitionCase(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To string `json:"to"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	c, err := h.svc.Cases.Transition(r.Context(), tenantOf(r), r.PathValue("id"), body.To)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *handlers) linkCase(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RequestID      string `json:"request_id"`
		BookingID      string `json:"booking_id"`
		ConversationID string `json:"conversation_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	c, err := h.svc.Cases.Link(r.Context(), tenantOf(r), r.PathValue("id"),
		body.RequestID, body.BookingID, body.ConversationID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *handlers) addCaseTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	t, err := h.svc.Cases.AddTask(r.Context(), tenantOf(r), r.PathValue("id"), body.Title)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, t)
}

func (h *handlers) listCaseTasks(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Cases.Tasks(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) completeCaseTask(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.Cases.CompleteTask(r.Context(), tenantOf(r), r.PathValue("id"), r.PathValue("taskID"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

func (h *handlers) addCaseNote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Author string `json:"author"`
		Body   string `json:"body"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	n, err := h.svc.Cases.AddNote(r.Context(), tenantOf(r), r.PathValue("id"), body.Author, body.Body)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, n)
}

func (h *handlers) listCaseNotes(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Cases.Notes(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) caseSummary(w http.ResponseWriter, r *http.Request) {
	summary, engine, err := h.svc.Cases.Summarize(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"summary": summary, "engine": engine})
}

func (h *handlers) caseNextAction(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Cases.NextAction(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *handlers) caseSLAScan(w http.ResponseWriter, r *http.Request) {
	n, err := h.svc.Cases.ScanSLA(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]int{"escalated": n})
}
