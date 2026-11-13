package model

import "time"

// ------------------------------------------------------------- Phase 4: case management
//
// Next-generation case management layered ON TOP of the existing CRM
// (clients + preferences stay the identity/preference spine). A Case is the
// unit of work for concierge operations: it binds a client, their
// conversations, service requests and bookings into one accountable
// workflow with an owner, priority, SLA clock and full timeline.
//
// AI (local Qwen via the concierge-agent) triages, summarizes and proposes
// next actions; the engine that produced each output is recorded on the case
// for auditability ("ollama/qwen3:8b" | "rules" | "core-rules").

type Case struct {
	ID           string `json:"id"`
	TenantID     string `json:"tenant_id"`
	ClientID     string `json:"client_id,omitempty"`
	Title        string `json:"title"`
	Kind         string `json:"kind"`     // booking | incident | inquiry | vip_request
	Priority     string `json:"priority"` // P1 (safety/legal) .. P4 (fyi)
	State        string `json:"state"`    // OPEN|TRIAGED|IN_PROGRESS|WAITING_CLIENT|WAITING_PARTNER|RESOLVED|CLOSED
	OwnerID      string `json:"owner_id,omitempty"`
	Summary      string `json:"summary"`
	TriageEngine string `json:"triage_engine"` // provenance of kind/priority/summary
	// Linked entities — the CRM spine stays canonical; the case references it.
	RequestID      string    `json:"request_id,omitempty"`
	BookingID      string    `json:"booking_id,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	Tags           []string  `json:"tags,omitempty"`
	SLADueAt       time.Time `json:"sla_due_at"`
	Escalated      bool      `json:"escalated"`
	IdempotencyKey string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	ResolvedAt     time.Time `json:"resolved_at,omitempty"`
	ClosedAt       time.Time `json:"closed_at,omitempty"`
}

const (
	CaseOpen           = "OPEN"
	CaseTriaged        = "TRIAGED"
	CaseInProgress     = "IN_PROGRESS"
	CaseWaitingClient  = "WAITING_CLIENT"
	CaseWaitingPartner = "WAITING_PARTNER"
	CaseResolved       = "RESOLVED"
	CaseClosed         = "CLOSED"
)

const (
	CaseKindBooking  = "booking"
	CaseKindIncident = "incident"
	CaseKindInquiry  = "inquiry"
	CaseKindVIP      = "vip_request"
)

// CaseTask is one checklist item on a case (the operational to-do list).
type CaseTask struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	CaseID    string    `json:"case_id"`
	Title     string    `json:"title"`
	Done      bool      `json:"done"`
	DoneAt    time.Time `json:"done_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// CaseNote is an operator/AI-authored timeline entry on a case.
type CaseNote struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	CaseID    string    `json:"case_id"`
	Author    string    `json:"author"` // operator id | "ai" | "system"
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}
