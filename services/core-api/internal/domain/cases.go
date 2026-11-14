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
	"math"
	"net/http"
	"sort"
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

func (c *CaseAssistClient) Summarize(ctx context.Context, timeline []string) (string, string, error) {
	var out struct {
		Summary string `json:"summary"`
		Engine  string `json:"engine"`
	}
	if err := c.post(ctx, "/v1/ai/summarize", map[string]any{"timeline": timeline}, &out); err != nil {
		return "", "", err
	}
	if out.Engine == "" {
		out.Engine = "assist"
	}
	return out.Summary, out.Engine, nil
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

// Embed fetches a semantic vector for text (Ollama embeddings model).
// Advisory: callers treat an error as "semantic memory unavailable".
func (c *CaseAssistClient) Embed(ctx context.Context, text string) ([]float32, error) {
	var out struct {
		Embedding []float32 `json:"embedding"`
		Engine    string    `json:"engine"`
	}
	if err := c.post(ctx, "/v1/ai/embed", map[string]string{"text": text}, &out); err != nil {
		return nil, err
	}
	if len(out.Embedding) == 0 {
		return nil, fmt.Errorf("empty embedding")
	}
	return out.Embedding, nil
}

// Draft produces an operator-approval reply draft for a case.
func (c *CaseAssistClient) Draft(ctx context.Context, intent, text string) (string, string, error) {
	var out struct {
		Draft  string `json:"draft"`
		Engine string `json:"engine"`
	}
	if err := c.post(ctx, "/v1/ai/draft", map[string]string{"intent": intent, "text": text}, &out); err != nil {
		return "", "", err
	}
	if out.Engine == "" {
		out.Engine = "assist"
	}
	return out.Draft, out.Engine, nil
}

// ------------------------------------------------------------- case service

type CaseService struct {
	s      *Services
	assist *CaseAssistClient
	waURL  string // whatsapp-adapter base (CE_WHATSAPP_ADAPTER_URL); empty = no outbound
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
	kind, priority, summary, engine := svc.triage(ctx, text)
	return svc.openTriaged(ctx, tenantID, clientID, title, idem, kind, priority, summary, engine)
}

// openTriaged persists a case from an already-computed triage result.
func (svc *CaseService) openTriaged(ctx context.Context, tenantID, clientID, title, idem, kind, priority, summary, engine string) (*model.Case, error) {
	now := svc.s.Now()
	c := &model.Case{
		ID: NewID(), TenantID: tenantID, ClientID: clientID, Title: title,
		Kind: kind, Priority: priority, State: model.CaseOpen,
		Summary: summary, TriageEngine: engine,
		SLADueAt:       now.Add(svc.slaFor(ctx, tenantID, priority)),
		IdempotencyKey: idem, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.s.Store.Cases().Create(ctx, c); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "case", c.ID, "case.opened",
		map[string]any{"kind": kind, "priority": priority, "engine": engine})
	// Semantic memory: embed title+summary so similar-case recall works.
	// Advisory — embedding failure never blocks intake.
	if emb, err := svc.embed(ctx, title+"\n"+summary); err == nil && len(emb) > 0 {
		_ = svc.s.Store.Embeddings().StoreEmbedding(ctx, tenantID, c.ID, emb)
	}
	return c, nil
}

// IntakeResult reports an automated intake decision: whether the inbound
// text became a case, plus the triage evidence either way.
type IntakeResult struct {
	Opened   bool        `json:"opened"`
	Case     *model.Case `json:"case,omitempty"`
	Kind     string      `json:"kind"`
	Priority string      `json:"priority"`
	Engine   string      `json:"engine"`
	Reason   string      `json:"reason"`
}

// caseIntakeKinds always warrant a case regardless of priority.
var caseIntakeKinds = map[string]bool{
	model.CaseKindIncident: true, model.CaseKindVIP: true,
}

// Intake applies the channel-intake policy: triage once, then open a case
// only when the message warrants accountable follow-up — incidents and VIP
// requests always; anything at P1/P2 (time-critical, service recovery);
// plain inquiries and routine P3/P4 bookings stay in the conversation flow
// and do NOT create cases. Idempotent per (tenant, idem) so redelivered
// channel webhooks never double-open. conversationID links the case back
// to the CRM conversation spine.
func (svc *CaseService) Intake(ctx context.Context, tenantID, clientID, text, conversationID, idem string) (*IntakeResult, error) {
	if clientID == "" || text == "" {
		return nil, httpx.ErrValidation
	}
	kind, priority, summary, engine := svc.triage(ctx, text)
	res := &IntakeResult{Kind: kind, Priority: priority, Engine: engine}
	if !caseIntakeKinds[kind] && priority != "P1" && priority != "P2" {
		res.Reason = "routine message — stays in conversation flow"
		// Skips feed the analytics bronze stream so the intake funnel
		// (offered vs opened) is measurable per tenant and channel.
		svc.s.Events.Emit(ctx, tenantID, "case", conversationID, "case.intake_skipped",
			map[string]any{"kind": kind, "priority": priority, "engine": engine})
		return res, nil
	}
	res.Reason = "warrants accountable follow-up"
	title := summary
	if title == "" {
		title = text
	}
	if len(title) > 80 {
		title = title[:80]
	}
	c, err := svc.openTriaged(ctx, tenantID, clientID, title, idem, kind, priority, summary, engine)
	if err == httpx.ErrConflict {
		// Idempotent replay (channel webhook redelivery): the case already
		// exists — report opened without duplicating.
		res.Opened, res.Reason = true, "duplicate delivery — case already open"
		return res, nil
	}
	if err != nil {
		return nil, err
	}
	if conversationID != "" {
		linked, err := svc.Link(ctx, tenantID, c.ID, "", "", conversationID)
		if err != nil {
			return nil, err
		}
		c = linked
	}
	res.Opened, res.Case = true, c
	return res, nil
}

// slaFor resolves the SLA clock: tenant override (SLAConfig.CaseSLAHours)
// beats the platform default.
func (svc *CaseService) slaFor(ctx context.Context, tenantID, priority string) time.Duration {
	if t, err := svc.s.Store.Tenants().Get(ctx, tenantID); err == nil {
		if h := t.SLAConfig.CaseSLAHours[priority]; h > 0 {
			return time.Duration(h) * time.Hour
		}
	}
	return caseSLAHours[priority]
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
		if s, eng, err := svc.assist.Summarize(ctx, timeline); err == nil && s != "" {
			return s, eng, nil
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

// DraftReply produces an operator-approval reply draft from the case context
// (title, summary, recent notes). Advisory: returns the engine label; when
// the model is unavailable the draft is empty and the operator writes from
// scratch — nothing is ever sent without an explicit SendReply call.
func (svc *CaseService) DraftReply(ctx context.Context, tenantID, id string) (string, string, error) {
	c, err := svc.s.Store.Cases().Get(ctx, tenantID, id)
	if err != nil {
		return "", "", err
	}
	notes, err := svc.s.Store.Cases().Notes(ctx, tenantID, id)
	if err != nil {
		return "", "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "case: %s [%s/%s, state %s]\nsummary: %s\n", c.Title, c.Kind, c.Priority, c.State, c.Summary)
	start := 0
	if len(notes) > 5 {
		start = len(notes) - 5
	}
	for _, n := range notes[start:] {
		fmt.Fprintf(&b, "note %s: %s\n", n.Author, n.Body)
	}
	if svc.assist == nil {
		return "", "rules", nil
	}
	draft, engine, err := svc.assist.Draft(ctx, "case-reply", b.String())
	if err != nil {
		return "", "rules", nil // advisory: empty draft on assist failure
	}
	return draft, engine, nil
}

// ReplyResult reports an approved outbound reply.
type ReplyResult struct {
	Sent        bool   `json:"sent"`                   // delivered to the channel adapter
	Recorded    bool   `json:"recorded"`               // appended to the conversation timeline
	Engine      string `json:"engine"`                 // draft provenance passed through by the operator
	ExternalRef string `json:"external_ref,omitempty"` // channel message id — receipts attach here
	Detail      string `json:"detail"`
}

// SendReply is the operator-approval gate: the operator reviews/edits the
// draft and posts it here; only then does anything reach the client. The
// reply is appended to the linked conversation (HUMAN_STAFF), noted on the
// case, audited, and — when CE_WHATSAPP_ADAPTER_URL is configured and the
// client has a phone — handed to the WhatsApp adapter for delivery.
func (svc *CaseService) SendReply(ctx context.Context, tenantID, id, body, operator, engine string) (*ReplyResult, error) {
	if body == "" || operator == "" {
		return nil, httpx.ErrValidation
	}
	c, err := svc.s.Store.Cases().Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	res := &ReplyResult{Engine: engine}

	if c.ConversationID != "" {
		m := &model.Message{
			ID: NewID(), TenantID: tenantID, ConversationID: c.ConversationID,
			Author: "HUMAN_STAFF", Body: body, SentAt: svc.s.Now(),
		}
		if err := svc.s.Store.Conversations().AppendMessage(ctx, m); err != nil {
			return nil, err
		}
		res.Recorded = true
	}

	if svc.waURL != "" && c.ClientID != "" {
		if cl, err := svc.s.Store.Clients().Get(ctx, tenantID, c.ClientID); err == nil && cl.Phone != "" {
			payload, _ := json.Marshal(map[string]string{
				"tenant_id": tenantID, "contact": cl.Phone, "body": body})
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
				strings.TrimSuffix(svc.waURL, "/")+"/v1/send", strings.NewReader(string(payload)))
			req.Header.Set("Content-Type", "application/json")
			hc := &http.Client{Timeout: 10 * time.Second}
			if resp, err := hc.Do(req); err == nil {
				res.Sent = resp.StatusCode < 300
				// Capture the channel's message id so delivery/read receipts
				// (Phase 11) attach to this reply.
				var sendBody struct {
					Body struct {
						Messages []struct {
							ID string `json:"id"`
						} `json:"messages"`
					} `json:"body"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&sendBody); err == nil &&
					len(sendBody.Body.Messages) > 0 {
					res.ExternalRef = sendBody.Body.Messages[0].ID
				}
				resp.Body.Close()
				if !res.Sent {
					res.Detail = fmt.Sprintf("channel adapter returned %d", resp.StatusCode)
				}
			} else {
				res.Detail = "channel adapter unreachable: " + err.Error()
			}
		}
	}
	if !res.Sent && res.Detail == "" {
		res.Detail = "recorded on the timeline; no channel adapter configured"
	}

	author := "operator:" + operator
	_, _ = svc.AddNote(ctx, tenantID, id, author, "reply approved & sent: "+body)
	svc.s.Audit.Log(ctx, tenantID, author, "case.reply_sent", "case", id,
		map[string]any{"sent": res.Sent, "engine": engine})
	svc.s.Events.Emit(ctx, tenantID, "case", id, "case.reply_sent",
		map[string]any{"sent": res.Sent, "engine": engine})
	return res, nil
}

// embed is the advisory embedding helper: assist client first, nil on any
// failure (semantic memory is a bonus, never a blocker).
func (svc *CaseService) embed(ctx context.Context, text string) ([]float32, error) {
	if svc.assist == nil {
		return nil, fmt.Errorf("assist disabled")
	}
	return svc.assist.Embed(ctx, text)
}

// SimilarCase is one ranked recall hit.
type SimilarCase struct {
	CaseID     string  `json:"case_id"`
	Title      string  `json:"title"`
	Kind       string  `json:"kind"`
	State      string  `json:"state"`
	Score      float64 `json:"score"` // cosine similarity 0..1
	ResolvedAt string  `json:"resolved_at,omitempty"`
}

// Similar recalls the tenant's most similar past cases by cosine similarity
// over stored embeddings — how a new incident finds its precedent. Returns
// nil when semantic memory is unavailable (no embeddings stored / daemon
// down at intake).
func (svc *CaseService) Similar(ctx context.Context, tenantID, caseID string, limit int) ([]SimilarCase, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	all, err := svc.s.Store.Embeddings().All(ctx, tenantID)
	if err != nil || len(all) < 2 {
		return nil, err
	}
	self, ok := all[caseID]
	if !ok {
		return nil, nil // this case was opened without an embedding
	}
	type hit struct {
		id    string
		score float64
	}
	hits := []hit{}
	for id, vec := range all {
		if id == caseID {
			continue
		}
		if s := cosine(self, vec); s > 0 {
			hits = append(hits, hit{id, s})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := []SimilarCase{}
	for _, h := range hits {
		c, err := svc.s.Store.Cases().Get(ctx, tenantID, h.id)
		if err != nil {
			continue
		}
		sc := SimilarCase{CaseID: c.ID, Title: c.Title, Kind: c.Kind, State: c.State, Score: h.score}
		if !c.ResolvedAt.IsZero() {
			sc.ResolvedAt = c.ResolvedAt.Format(time.RFC3339)
		}
		out = append(out, sc)
	}
	return out, nil
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
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
	Open           int            `json:"open"`
	Triaged        int            `json:"triaged"`
	InProgress     int            `json:"in_progress"`
	Waiting        int            `json:"waiting"`
	Resolved       int            `json:"resolved"`
	Closed         int            `json:"closed"`
	SLABreached    int            `json:"sla_breached"`
	AvgResolutionH float64        `json:"avg_resolution_hours"`
	AITriageShare  float64        `json:"ai_triage_share"` // fraction triaged by a model, not rules
	ByKind         map[string]int `json:"by_kind"`
	ByPriority     map[string]int `json:"by_priority"`
	ChannelIntake  int            `json:"channel_intake"` // cases opened via channel intake (wa:* idem keys)
}

func (svc *CaseService) Metrics(ctx context.Context, tenantID string) (*CaseMetrics, error) {
	list, err := svc.s.Store.Cases().List(ctx, tenantID, "", "")
	if err != nil {
		return nil, err
	}
	m := &CaseMetrics{ByKind: map[string]int{}, ByPriority: map[string]int{}}
	var totalResolution time.Duration
	resolved := 0
	ai := 0
	for _, c := range list {
		m.ByKind[c.Kind]++
		m.ByPriority[c.Priority]++
		if strings.HasPrefix(c.IdempotencyKey, "wa:") {
			m.ChannelIntake++
		}
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
