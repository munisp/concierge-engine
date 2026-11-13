// Next-generation case management (Phase 4) — layered on top of the CRM.
// Clients + preferences remain the identity/preference spine; a Case is the
// accountable unit of operational work binding client, conversations,
// requests and bookings with owner, priority, SLA clock and timeline.
//
// AI assist flows through the concierge-agent (local Qwen via Ollama when
// reachable; rules otherwise). Core-api keeps its own deterministic floor
// ("core-rules") so case intake never depends on any model being alive.
// Every AI-produced field records its engine for the audit trail.
package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// CaseAssistClient calls the concierge-agent's AI endpoints. Timeout-failures
// degrade to the caller's deterministic floor — assist is advisory, the case
// workflow is the system of record.
type CaseAssistClient struct {
	base string
	hc   *http.Client
}

func NewCaseAssistClient(base string) *CaseAssistClient {
	return &CaseAssistClient{base: strings.TrimSuffix(base, "/"), hc: &http.Client{Timeout: 8 * time.Second}}
}

type triageResult struct {
	Kind     string `json:"kind"`
	Priority string `json:"priority"`
	Summary  string `json:"summary"`
	Reason   string `json:"reason"`
	Engine   string `json:"engine"`
}

func (c *CaseAssistClient) post(ctx context.Context, path string, in, out any) error {
	if c.base == "" {
		return fmt.Errorf("assist disabled")
	}
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("assist %s: %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func (c *CaseAssistClient) Triage(ctx context.Context, text string) (*triageResult, error) {
	var out triageResult
	if err := c.post(ctx, "/v1/ai/triage", map[string]string{"text": text}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *CaseAssistClient) Summarize(ctx context.Context, timeline []string) (string, error) {
	var out struct {
		Summary string `json:"summary"`
	}
	if err := c.post(ctx, "/v1/ai/summarize", map[string]any{"timeline": timeline}, &out); err != nil {
		return "", err
	}
	return out.Summary, nil
}

type nextActionResult struct {
	Action    string `json:"action"`
	Rationale string `json:"rationale"`
	Engine    string `json:"engine"`
}

func (c *CaseAssistClient) NextAction(ctx context.Context, state, summary string) (*nextActionResult, error) {
	var out nextActionResult
	if err := c.post(ctx, "/v1/ai/next-action",
		map[string]string{"case_state": state, "summary": summary}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ------------------------------------------------------------- case service

type CaseService struct {
	s      *Services
	assist *CaseAssistClient
}

// SLA clocks by priority — P1 mirrors Taylor's 1-hour promise; the rest
// scale down by urgency. Case SLA = time-to-resolution, not quote SLA.
var caseSLAHours = map[string]time.Duration{
	"P1": 1 * time.Hour,
	"P2": 4 * time.Hour,
	"P3": 24 * time.Hour,
	"P4": 72 * time.Hour,
}

var caseTransitions = map[string][]string{
	model.CaseOpen:           {model.CaseTriaged, model.CaseInProgress, model.CaseClosed},
	model.CaseTriaged:        {model.CaseInProgress, model.CaseWaitingClient, model.CaseWaitingPartner, model.CaseClosed},
	model.CaseInProgress:     {model.CaseWaitingClient, model.CaseWaitingPartner, model.CaseResolved},
	model.CaseWaitingClient:  {model.CaseInProgress, model.CaseResolved, model.CaseClosed},
	model.CaseWaitingPartner: {model.CaseInProgress, model.CaseResolved},
	model.CaseResolved:       {model.CaseClosed},
	model.CaseClosed:         {}, // terminal
}

var caseKinds = map[string]bool{
	model.CaseKindBooking: true, model.CaseKindIncident: true,
	model.CaseKindInquiry: true, model.CaseKindVIP: true,
}

// Open creates a case and auto-triages it (kind/priority/summary from the
// inbound text). Idempotent per (tenant, idempotency key). The triage
// engine's provenance is recorded on the case.
func (svc *CaseService) Open(ctx context.Context, tenantID, clientID, title, text, idem string) (*model.Case, error) {
	if title == "" || text == "" {
		return nil, httpx.ErrValidation
	}
	now := svc.s.Now()
	kind, priority, summary, engine := svc.triage(ctx, text)
	due := now.Add(caseSLAHours[priority])
	c := &model.Case{
		ID: NewID(), TenantID: tenantID, ClientID: clientID, Title: title,
		Kind: kind, Priority: priority, State: model.CaseOpen,
		Summary: summary, TriageEngine: engine,
		SLADueAt: due, IdempotencyKey: idem, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.s.Store.Cases().Create(ctx, c); err != nil {
		if err == httpx.ErrConflict {
			return nil, err
		}
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "case", c.ID, "case.opened",
		map[string]any{"kind": kind, "priority": priority, "engine": engine})
	return c, nil
}

// triage resolves kind/priority/summary: concierge-agent (local Qwen) first,
// deterministic core rules when assist is unavailable.
func (svc *CaseService) triage(ctx context.Context, text string) (kind, priority, summary, engine string) {
	if svc.assist != nil {
		if t, err := svc.assist.Triage(ctx, text); err == nil && caseKinds[t.Kind] && caseSLAHours[t.Priority] > 0 {
			engine := t.Engine // already provenance-complete ("ollama/qwen3:8b" | "rules")
			if engine == "" {
				engine = "assist"
			}
			return t.Kind, t.Priority, t.Summary, engine
		}
	}
	kind, priority = coreRuleTriage(text)
	if len(text) > 140 {
		text = text[:140]
	}
	return kind, priority, text, "core-rules"
}

// coreRuleTriage is the always-available floor — intentionally minimal; the
// Python rules are the smarter fallback (this one only fires when the whole
// agent service is unreachable).
func coreRuleTriage(text string) (kind, priority string) {
	t := strings.ToLower(text)
	switch {
	case strings.Contains(t, "safety") || strings.Contains(t, "legal") || strings.Contains(t, "threat"):
		return model.CaseKindIncident, "P1"
	case strings.Contains(t, "jet") || strings.Contains(t, "helicopter") || strings.Contains(t, "yacht"):
		return model.CaseKindVIP, "P2"
	case strings.Contains(t, "urgent") || strings.Contains(t, "asap") || strings.Contains(t, "tonight"):
		return model.CaseKindBooking, "P2"
	case strings.Contains(t, "book") || strings.Contains(t, "quote") || strings.Contains(t, "transfer"):
		return model.CaseKindBooking, "P3"
	case strings.Contains(t, "complaint") || strings.Contains(t, "late") || strings.Contains(t, "refund"):
		return model.CaseKindIncident, "P2"
	default:
		return model.CaseKindInquiry, "P3"
	}
}

func (svc *CaseService) Get(ctx context.Context, tenantID, id string) (*model.Case, error) {
	return svc.s.Store.Cases().Get(ctx, tenantID, id)
}

func (svc *CaseService) List(ctx context.Context, tenantID, state, kind string) ([]model.Case, error) {
	if state != "" {
		if _, ok := caseTransitions[state]; !ok {
			return nil, httpx.ErrValidation
		}
	}
	if kind != "" && !caseKinds[kind] {
		return nil, httpx.ErrValidation
	}
	return svc.s.Store.Cases().List(ctx, tenantID, state, kind)
}

func (svc *CaseService) Assign(ctx context.Context, tenantID, id, ownerID string) (*model.Case, error) {
	if ownerID == "" {
		return nil, httpx.ErrValidation
	}
	c, err := svc.s.Store.Cases().Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	c.OwnerID = ownerID
	c.UpdatedAt = svc.s.Now()
	if c.State == model.CaseOpen {
		c.State = model.CaseTriaged // assignment implies triage acknowledged
	}
	if err := svc.s.Store.Cases().Update(ctx, c); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "case", c.ID, "case.assigned", map[string]any{"owner": ownerID})
	return c, nil
}

// Transition walks the case state machine; RESOLVED/CLOSED stamp their
// timestamps; CLOSED is terminal.
func (svc *CaseService) Transition(ctx context.Context, tenantID, id, to string) (*model.Case, error) {
	c, err := svc.s.Store.Cases().Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	allowed := false
	for _, s := range caseTransitions[c.State] {
		if s == to {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, httpx.ErrConflict
	}
	now := svc.s.Now()
	c.State = to
	c.UpdatedAt = now
	switch to {
	case model.CaseResolved:
		c.ResolvedAt = now
	case model.CaseClosed:
		if c.ResolvedAt.IsZero() {
			c.ResolvedAt = now
		}
		c.ClosedAt = now
	}
	if err := svc.s.Store.Cases().Update(ctx, c); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "case", c.ID, "case."+strings.ToLower(to), nil)
	return c, nil
}

// Link attaches the CRM/booking spine to the case (request, booking,
// conversation) — the case references; the CRM stays canonical.
func (svc *CaseService) Link(ctx context.Context, tenantID, id, requestID, bookingID, conversationID string) (*model.Case, error) {
	c, err := svc.s.Store.Cases().Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if requestID != "" {
		c.RequestID = requestID
	}
	if bookingID != "" {
		c.BookingID = bookingID
	}
	if conversationID != "" {
		c.ConversationID = conversationID
	}
	c.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.Cases().Update(ctx, c); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "case", c.ID, "case.linked", map[string]any{
		"request_id": c.RequestID, "booking_id": c.BookingID, "conversation_id": c.ConversationID})
	return c, nil
}

func (svc *CaseService) AddTask(ctx context.Context, tenantID, caseID, title string) (*model.CaseTask, error) {
	if title == "" {
		return nil, httpx.ErrValidation
	}
	if _, err := svc.s.Store.Cases().Get(ctx, tenantID, caseID); err != nil {
		return nil, err
	}
	t := &model.CaseTask{ID: NewID(), TenantID: tenantID, CaseID: caseID, Title: title, CreatedAt: svc.s.Now()}
	if err := svc.s.Store.Cases().AddTask(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (svc *CaseService) CompleteTask(ctx context.Context, tenantID, caseID, taskID string) (*model.CaseTask, error) {
	tasks, err := svc.s.Store.Cases().Tasks(ctx, tenantID, caseID)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		if tasks[i].ID == taskID {
			if tasks[i].Done {
				return &tasks[i], nil // idempotent
			}
			tasks[i].Done = true
			tasks[i].DoneAt = svc.s.Now()
			if err := svc.s.Store.Cases().UpdateTask(ctx, &tasks[i]); err != nil {
				return nil, err
			}
			return &tasks[i], nil
		}
	}
	return nil, httpx.ErrNotFound
}

func (svc *CaseService) Tasks(ctx context.Context, tenantID, caseID string) ([]model.CaseTask, error) {
	return svc.s.Store.Cases().Tasks(ctx, tenantID, caseID)
}

func (svc *CaseService) AddNote(ctx context.Context, tenantID, caseID, author, body string) (*model.CaseNote, error) {
	if body == "" {
		return nil, httpx.ErrValidation
	}
	if author == "" {
		author = "system"
	}
	if _, err := svc.s.Store.Cases().Get(ctx, tenantID, caseID); err != nil {
		return nil, err
	}
	n := &model.CaseNote{ID: NewID(), TenantID: tenantID, CaseID: caseID, Author: author, Body: body, CreatedAt: svc.s.Now()}
	if err := svc.s.Store.Cases().AddNote(ctx, n); err != nil {
		return nil, err
	}
	return n, nil
}

func (svc *CaseService) Notes(ctx context.Context, tenantID, caseID string) ([]model.CaseNote, error) {
	return svc.s.Store.Cases().Notes(ctx, tenantID, caseID)
}

// Summarize produces an AI digest of the case timeline (notes + state), and
// NextAction proposes the single best operator move. Both record the engine
// in the response so the UI can show provenance.
func (svc *CaseService) Summarize(ctx context.Context, tenantID, id string) (string, string, error) {
	c, err := svc.s.Store.Cases().Get(ctx, tenantID, id)
	if err != nil {
		return "", "", err
	}
	notes, err := svc.s.Store.Cases().Notes(ctx, tenantID, id)
	if err != nil {
		return "", "", err
	}
	timeline := []string{fmt.Sprintf("case opened: %s [%s/%s]", c.Title, c.Kind, c.Priority)}
	for _, n := range notes {
		timeline = append(timeline, n.Author+": "+n.Body)
	}
	if svc.assist != nil {
		if s, err := svc.assist.Summarize(ctx, timeline); err == nil && s != "" {
			return s, "assist", nil // the agent's engine label lives in its traces
		}
	}
	// core floor: last timeline entries joined
	start := 0
	if len(timeline) > 3 {
		start = len(timeline) - 3
	}
	return strings.Join(timeline[start:], " | "), "core-rules", nil
}

func (svc *CaseService) NextAction(ctx context.Context, tenantID, id string) (*nextActionResult, error) {
	c, err := svc.s.Store.Cases().Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if svc.assist != nil {
		if r, err := svc.assist.NextAction(ctx, c.State, c.Summary); err == nil && r.Action != "" {
			return r, nil
		}
	}
	return &nextActionResult{Action: coreNextAction(c.State), Rationale: "deterministic fallback", Engine: "core-rules"}, nil
}

func coreNextAction(state string) string {
	switch state {
	case model.CaseOpen:
		return "Triage and assign an owner"
	case model.CaseTriaged:
		return "Acknowledge the client and start work"
	case model.CaseInProgress:
		return "Check the SLA clock and push the oldest open task"
	case model.CaseWaitingClient:
		return "Nudge the client; prepare options for their reply"
	case model.CaseWaitingPartner:
		return "Chase the partner; line up a fallback supplier"
	case model.CaseResolved:
		return "Confirm satisfaction and close"
	default:
		return "Review the case"
	}
}

// ScanSLA escalates open cases past their SLA clock. Called by the platform
// sweeper (same shape as the webhook dispatcher).
func (svc *CaseService) ScanSLA(ctx context.Context, tenantID string) (int, error) {
	overdue, err := svc.s.Store.Cases().Overdue(ctx, tenantID, svc.s.Now())
	if err != nil {
		return 0, err
	}
	escalated := 0
	for i := range overdue {
		c := overdue[i]
		if c.Escalated {
			continue
		}
		c.Escalated = true
		c.UpdatedAt = svc.s.Now()
		if err := svc.s.Store.Cases().Update(ctx, &c); err != nil {
			continue
		}
		svc.s.Events.Emit(ctx, tenantID, "case", c.ID, "case.sla_breach",
			map[string]any{"priority": c.Priority, "due_at": c.SLADueAt})
		escalated++
	}
	return escalated, nil
}

// CaseMetrics is the operations KPI surface.
type CaseMetrics struct {
	Open           int     `json:"open"`
	Triaged        int     `json:"triaged"`
	InProgress     int     `json:"in_progress"`
	Waiting        int     `json:"waiting"`
	Resolved       int     `json:"resolved"`
	Closed         int     `json:"closed"`
	SLABreached    int     `json:"sla_breached"`
	AvgResolutionH float64 `json:"avg_resolution_hours"`
	AITriageShare  float64 `json:"ai_triage_share"` // fraction triaged by a model, not rules
}

func (svc *CaseService) Metrics(ctx context.Context, tenantID string) (*CaseMetrics, error) {
	list, err := svc.s.Store.Cases().List(ctx, tenantID, "", "")
	if err != nil {
		return nil, err
	}
	m := &CaseMetrics{}
	var totalResolution time.Duration
	resolved := 0
	ai := 0
	for _, c := range list {
		switch c.State {
		case model.CaseOpen:
			m.Open++
		case model.CaseTriaged:
			m.Triaged++
		case model.CaseInProgress:
			m.InProgress++
		case model.CaseWaitingClient, model.CaseWaitingPartner:
			m.Waiting++
		case model.CaseResolved:
			m.Resolved++
		case model.CaseClosed:
			m.Closed++
		}
		if c.Escalated {
			m.SLABreached++
		}
		if !c.ResolvedAt.IsZero() {
			totalResolution += c.ResolvedAt.Sub(c.CreatedAt)
			resolved++
		}
		if strings.HasPrefix(c.TriageEngine, "ollama/") {
			ai++
		}
	}
	if resolved > 0 {
		m.AvgResolutionH = totalResolution.Hours() / float64(resolved)
	}
	if len(list) > 0 {
		m.AITriageShare = float64(ai) / float64(len(list))
	}
	return m, nil
}
