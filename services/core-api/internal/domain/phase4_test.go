// Phase 4 domain tests: case lifecycle (open → triage → assign → work →
// resolve → close), tasks/notes, SLA breach escalation, AI assist wiring
// (fake concierge-agent) with deterministic fallback, and metrics.
package domain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestCaseLifecycle(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	c, err := s.Cases.Open(ctx, tenantID, "", "Airport transfer issue",
		"My chauffeur was 40 minutes late and I missed the meeting, I want a refund", "case-k1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Assist unreachable in tests -> core rules: complaint/refund -> incident P2.
	if c.Kind != model.CaseKindIncident || c.Priority != "P2" || c.TriageEngine != "core-rules" {
		t.Fatalf("triage wrong: %+v", c)
	}
	if c.State != model.CaseOpen || c.SLADueAt.Before(s.Now()) {
		t.Fatalf("initial state/sla wrong: %+v", c)
	}

	// Idempotent open.
	if _, err := s.Cases.Open(ctx, tenantID, "", "Airport transfer issue",
		"same text", "case-k1"); err != httpx.ErrConflict {
		t.Fatalf("duplicate idem key must conflict, got %v", err)
	}

	// Assignment implies triage acknowledgment.
	c, err = s.Cases.Assign(ctx, tenantID, c.ID, "concierge-7")
	if err != nil || c.State != model.CaseTriaged || c.OwnerID != "concierge-7" {
		t.Fatalf("assign: %+v, %v", c, err)
	}

	// Illegal transition rejected (TRIAGED -> RESOLVED not allowed).
	if _, err := s.Cases.Transition(ctx, tenantID, c.ID, model.CaseResolved); err != httpx.ErrConflict {
		t.Fatalf("illegal transition must conflict, got %v", err)
	}

	// Tasks and notes.
	task, err := s.Cases.AddTask(ctx, tenantID, c.ID, "Call client to apologize")
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	if _, err := s.Cases.AddNote(ctx, tenantID, c.ID, "concierge-7", "Client called back, calmer now"); err != nil {
		t.Fatalf("add note: %v", err)
	}
	done, err := s.Cases.CompleteTask(ctx, tenantID, c.ID, task.ID)
	if err != nil || !done.Done || done.DoneAt.IsZero() {
		t.Fatalf("complete task: %+v, %v", done, err)
	}
	// Idempotent re-complete.
	again, err := s.Cases.CompleteTask(ctx, tenantID, c.ID, task.ID)
	if err != nil || !again.Done {
		t.Fatalf("re-complete must be idempotent: %v", err)
	}

	// Link to the CRM/booking spine.
	c, err = s.Cases.Link(ctx, tenantID, c.ID, "req-1", "bk-1", "conv-1")
	if err != nil || c.RequestID != "req-1" || c.BookingID != "bk-1" {
		t.Fatalf("link: %+v, %v", c, err)
	}

	// Work → wait → resolve → close.
	for _, to := range []string{model.CaseInProgress, model.CaseWaitingClient, model.CaseInProgress, model.CaseResolved, model.CaseClosed} {
		c, err = s.Cases.Transition(ctx, tenantID, c.ID, to)
		if err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
	}
	if c.ClosedAt.IsZero() || c.ResolvedAt.IsZero() {
		t.Fatal("closed case must carry resolved+closed timestamps")
	}
	// Closed is terminal.
	if _, err := s.Cases.Transition(ctx, tenantID, c.ID, model.CaseInProgress); err != httpx.ErrConflict {
		t.Fatalf("closed must be terminal, got %v", err)
	}

	// Tenant isolation.
	if _, err := s.Cases.Get(ctx, "other-tenant", c.ID); err != httpx.ErrNotFound {
		t.Fatalf("cross-tenant read must be not-found, got %v", err)
	}
}

func TestCaseSLAScan(t *testing.T) {
	base := time.Now()
	s := testServices(t, Deps{Now: func() time.Time { return base }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	c, err := s.Cases.Open(ctx, tenantID, "", "Late pickup", "driver late, complaint", "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Advance the clock past the P2 4h SLA.
	s.now = func() time.Time { return base.Add(5 * time.Hour) }
	n, err := s.Cases.ScanSLA(ctx, tenantID)
	if err != nil || n != 1 {
		t.Fatalf("scan: %d, %v", n, err)
	}
	// Second scan: already escalated, no double-escalation.
	n, _ = s.Cases.ScanSLA(ctx, tenantID)
	if n != 0 {
		t.Fatalf("rescan must not re-escalate, got %d", n)
	}
	updated, _ := s.Cases.Get(ctx, tenantID, c.ID)
	if !updated.Escalated {
		t.Fatal("overdue case must be flagged escalated")
	}
}

func TestCaseAssistWiring(t *testing.T) {
	// Fake concierge-agent: triage endpoint answers as ollama/qwen.
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/ai/triage" {
			json.NewEncoder(w).Encode(map[string]string{
				"kind": "vip_request", "priority": "P2",
				"summary": "Helicopter to the F1 paddock", "reason": "high-value vertical",
				"engine": "ollama/qwen3:8b",
			})
			return
		}
		if r.URL.Path == "/v1/ai/next-action" {
			json.NewEncoder(w).Encode(map[string]string{
				"action": "Call the heli operator", "rationale": "slots are scarce",
				"engine": "ollama/qwen3:8b",
			})
			return
		}
		if r.URL.Path == "/v1/ai/summarize" {
			json.NewEncoder(w).Encode(map[string]string{"summary": "VIP heli request, operator callback pending"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer fake.Close()

	s := testServices(t, Deps{AgentURL: fake.URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	c, err := s.Cases.Open(ctx, tenantID, "", "F1 weekend", "Need a helicopter to the paddock Saturday", "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if c.Kind != model.CaseKindVIP || c.Priority != "P2" || c.TriageEngine != "ollama/qwen3:8b" {
		t.Fatalf("assist triage wrong: %+v", c)
	}
	if c.Summary != "Helicopter to the F1 paddock" {
		t.Fatalf("summary wrong: %q", c.Summary)
	}

	nxt, err := s.Cases.NextAction(ctx, tenantID, c.ID)
	if err != nil || nxt.Action != "Call the heli operator" || nxt.Engine != "ollama/qwen3:8b" {
		t.Fatalf("next action: %+v, %v", nxt, err)
	}

	summary, engine, err := s.Cases.Summarize(ctx, tenantID, c.ID)
	if err != nil || !strings.Contains(summary, "heli") || engine != "assist" {
		t.Fatalf("summary: %q (%s), %v", summary, engine, err)
	}
}

func TestCaseMetrics(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	c1, _ := s.Cases.Open(ctx, tenantID, "", "A", "book a car", "")
	if _, err := s.Cases.Open(ctx, tenantID, "", "B", "what time is checkout?", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cases.Transition(ctx, tenantID, c1.ID, model.CaseInProgress); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cases.Transition(ctx, tenantID, c1.ID, model.CaseResolved); err != nil {
		t.Fatal(err)
	}
	m, err := s.Cases.Metrics(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Open != 1 || m.Resolved != 1 || m.AITriageShare != 0 {
		t.Fatalf("metrics wrong: %+v", m)
	}
}

func TestCaseSLATenantOverride(t *testing.T) {
	base := time.Now()
	s := testServices(t, Deps{Now: func() time.Time { return base }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	// Tenant overrides P2 to 2 hours (default 4h).
	tenant, err := s.Tenants.Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	tenant.SLAConfig.CaseSLAHours = map[string]int{"P2": 2}
	if _, err := s.Tenants.UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}

	c, err := s.Cases.Open(ctx, tenantID, "", "Late pickup", "driver late, complaint", "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := c.SLADueAt.Sub(base); got != 2*time.Hour {
		t.Fatalf("tenant SLA override not applied: due in %v", got)
	}
}

func TestCaseSimilarRecall(t *testing.T) {
	// Fake agent with embeddings: semantic-ish vectors distinguishing
	// transport cases from billing cases.
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		vec := []float32{0.9, 0.1} // transport cluster
		if strings.Contains(body.Text, "invoice") || strings.Contains(body.Text, "refund") || strings.Contains(body.Text, "billing") {
			vec = []float32{0.1, 0.9} // money cluster
		}
		json.NewEncoder(w).Encode(map[string]any{"embedding": vec, "engine": "ollama/nomic-embed-text"})
	}))
	defer fake.Close()

	s := testServices(t, Deps{AgentURL: fake.URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	carA, _ := s.Cases.Open(ctx, tenantID, "", "Car to Schiphol", "book airport transfer", "")
	if _, err := s.Cases.Open(ctx, tenantID, "", "Refund question", "invoice dispute billing", ""); err != nil {
		t.Fatal(err)
	}
	carB, err := s.Cases.Open(ctx, tenantID, "", "Car to Eindhoven", "book another airport transfer", "")
	if err != nil {
		t.Fatal(err)
	}

	sim, err := s.Cases.Similar(ctx, tenantID, carB.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(sim) != 2 {
		t.Fatalf("expected 2 similar cases, got %d", len(sim))
	}
	// The other transport case must outrank the billing case.
	if sim[0].CaseID != carA.ID {
		t.Fatalf("ranking wrong: %+v", sim)
	}
	if sim[0].Score <= sim[1].Score {
		t.Fatalf("scores not descending: %+v", sim)
	}

	// No embeddings stored -> empty, no error.
	s2 := testServices(t, Deps{})
	t2 := seedTenant(t, s2)
	cx, _ := s2.Cases.Open(ctx, t2, "", "X", "book car", "")
	out, err := s2.Cases.Similar(ctx, t2, cx.ID, 5)
	if err != nil || len(out) != 0 {
		t.Fatalf("no embeddings must mean no recall, got %+v, %v", out, err)
	}
}

// ---- Phase 5: channel intake policy ------------------------------------

func TestCaseIntakePolicy(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	// Incident -> always opens, conversation linked.
	r1, err := s.Cases.Intake(ctx, tenantID, "client-1", "My driver never showed up, I want a refund", "conv-1", "wa:m1")
	if err != nil || !r1.Opened || r1.Case == nil {
		t.Fatalf("incident must open: %+v, %v", r1, err)
	}
	if r1.Kind != model.CaseKindIncident || r1.Engine != "core-rules" {
		t.Fatalf("triage evidence wrong: %+v", r1)
	}
	if r1.Case.ConversationID != "conv-1" {
		t.Fatalf("conversation not linked: %+v", r1.Case)
	}

	// Plain inquiry -> stays in conversation flow, no case.
	r2, err := s.Cases.Intake(ctx, tenantID, "client-1", "What are your opening hours?", "conv-2", "wa:m2")
	if err != nil || r2.Opened || r2.Case != nil {
		t.Fatalf("inquiry must not open: %+v, %v", r2, err)
	}
	if r2.Kind == "" || r2.Engine == "" {
		t.Fatalf("triage evidence required even when skipped: %+v", r2)
	}

	// Routine P3 booking -> no case.
	r3, err := s.Cases.Intake(ctx, tenantID, "client-1", "I would like to book a car for next Tuesday", "conv-3", "wa:m3")
	if err != nil || r3.Opened {
		t.Fatalf("P3 booking must not open: %+v, %v", r3, err)
	}
}

func TestCaseIntakeIdempotentReplay(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	r1, err := s.Cases.Intake(ctx, tenantID, "client-1", "There was a safety threat against my principal", "conv-9", "wa:dup")
	if err != nil || !r1.Opened || r1.Case == nil {
		t.Fatalf("first delivery opens: %+v, %v", r1, err)
	}
	r2, err := s.Cases.Intake(ctx, tenantID, "client-1", "There was a safety threat against my principal", "conv-9", "wa:dup")
	if err != nil || !r2.Opened || r2.Case != nil {
		t.Fatalf("replay reports opened without duplicating: %+v, %v", r2, err)
	}
	m, err := s.Cases.Metrics(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Open != 1 {
		t.Fatalf("replay must not create a second case, metrics: %+v", m)
	}
}
