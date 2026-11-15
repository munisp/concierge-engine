package model

import "time"

// Phase 41: email list hygiene.

// EmailSuppression is a permanently suppressed address (until an operator
// removes it). hard_bounce rows come from the ESP webhook via the email
// adapter; manual rows from the console. Applies to both service and
// marketing sends — a dead address delivers nothing on either lane.
type EmailSuppression struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Email     string    `json:"email"`  // normalized lowercase
	Reason    string    `json:"reason"` // hard_bounce | complaint | manual
	SourceRef string    `json:"source_ref,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Suppression reasons (Phase 41).
const (
	SuppressHardBounce = "hard_bounce"
	SuppressComplaint  = "complaint"
	SuppressManual     = "manual"
)
