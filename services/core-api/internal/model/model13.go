package model

// Phase 47: KYC / identity verification lane.
//
// Client.KYCStatus existed from Phase 1 as a write-only field — set to
// "none" at registration, never checked, never advanced. This model makes
// it real: a verification CASE per attempt, operator decision, and the
// client status that only ever advances on an APPROVED case.
//
// Data minimization is structural: a case carries document METADATA
// (type + operator-side reference into the tenant's own secure document
// channel), never the document itself. Erasure (Art. 17) scrubs the
// document references immediately; the case row (level, decision,
// timestamps, decided_by) survives as the AML accountability record, in
// the same exempt class as financial records.

import "time"

// Case levels. Client-facing statuses are the lowercase equivalents on
// Client.KYCStatus: "none" | "standard" | "enhanced".
const (
	KycLevelStandard = "STANDARD"
	KycLevelEnhanced = "ENHANCED"
)

// Case statuses. PENDING = opened, awaiting document submission;
// IN_REVIEW = documents submitted, awaiting operator decision. APPROVED /
// REJECTED / EXPIRED are terminal.
const (
	KycStatusPending  = "PENDING"
	KycStatusInReview = "IN_REVIEW"
	KycStatusApproved = "APPROVED"
	KycStatusRejected = "REJECTED"
	KycStatusExpired  = "EXPIRED"
)

// KycLevelValid reports whether level is a known verification level.
func KycLevelValid(level string) bool {
	return level == KycLevelStandard || level == KycLevelEnhanced
}

// KycLevelRank orders client statuses for the satisfaction check:
// "enhanced" satisfies an ENHANCED or STANDARD requirement, "standard"
// satisfies STANDARD only, anything else satisfies nothing.
func KycLevelRank(clientStatus string) int {
	switch clientStatus {
	case "enhanced":
		return 2
	case "standard":
		return 1
	default:
		return 0
	}
}

// KycCase is one identity-verification attempt. Money-style discipline:
// one open case per (tenant, client, level) enforced by the store;
// terminal states are absorbing so decision replays are no-ops.
type KycCase struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	ClientID string `json:"client_id"`
	Level    string `json:"level"`
	Status   string `json:"status"`
	// Provider is the verification rail. "manual" is the operator-review
	// lane (the honest default: no third-party KYC vendor is wired, and
	// nothing here pretends otherwise). External providers (Smile
	// Identity, Onfido) attach behind the same case shape later.
	Provider string `json:"provider"`
	// DocumentType / DocumentRef are METADATA ONLY — the document itself
	// lives in the tenant's secure channel; the platform records that a
	// passport was sighted and where the operator filed it.
	DocumentType string `json:"document_type,omitempty"`
	DocumentRef  string `json:"document_ref,omitempty"`
	// DocumentRefsScrubbed is set by the Art. 17 erasure companion: the
	// case row survives as the AML record, the document pointers do not.
	DecisionReason string    `json:"decision_reason,omitempty"`
	DecidedBy      string    `json:"decided_by,omitempty"`
	SubmittedAt    time.Time `json:"submitted_at,omitempty"`
	DecidedAt      time.Time `json:"decided_at,omitempty"`
	IdempotencyKey string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// KycTerminal reports whether the case is in an absorbing state.
func (c KycCase) KycTerminal() bool {
	return c.Status == KycStatusApproved || c.Status == KycStatusRejected || c.Status == KycStatusExpired
}
