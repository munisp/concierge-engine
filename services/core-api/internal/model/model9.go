package model

import "time"

// Phase 36: lead engine — first-class pre-client prospects in four kinds
// (person, corporate, partner, event), with a pipeline, deterministic
// scoring and conversion into the platform's real records.
//
// A lead is NOT a shadow client: the lead is the CRM/pipeline object, and
// the linked Client (ClientID, resolved at capture) is the messaging
// identity. All nurture (consent gates, frequency caps, quiet hours,
// sequences, STOP/START) therefore flows through the Phase 14/35 outreach
// engine unchanged.

const (
	LeadKindPerson    = "person"
	LeadKindCorporate = "corporate"
	LeadKindPartner   = "partner"
	LeadKindEvent     = "event"
)

const (
	LeadStatusNew       = "NEW"
	LeadStatusContacted = "CONTACTED"
	LeadStatusQualified = "QUALIFIED"
	LeadStatusConverted = "CONVERTED"
	LeadStatusLost      = "LOST"
)

// Lead: one prospect. Kind-specific fields are only meaningful for their
// kind (event_* / headcount / venue for events, verticals for partners,
// company for corporate) — capture validates the pairing.
type Lead struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Phone    string `json:"phone,omitempty"`
	Email    string `json:"email,omitempty"`
	Company  string `json:"company,omitempty"`
	// Event-kind brief.
	EventType string     `json:"event_type,omitempty"`
	EventDate *time.Time `json:"event_date,omitempty"`
	Headcount int        `json:"headcount,omitempty"`
	Venue     string     `json:"venue,omitempty"`
	// Partner-kind brief.
	Verticals []string `json:"verticals,omitempty"`
	Notes     string   `json:"notes,omitempty"`
	// Acquisition attribution (Phase 14/35 spine).
	Source       string `json:"source"` // capture surface: site | api | widget | import
	Channel      string `json:"channel,omitempty"`
	Campaign     string `json:"campaign,omitempty"`
	ReferralCode string `json:"referral_code,omitempty"`
	// Pipeline.
	Status       string   `json:"status"`
	Score        int      `json:"score"`
	ScoreReasons []string `json:"score_reasons,omitempty"`
	LostReason   string   `json:"lost_reason,omitempty"`
	// Consent mirrored onto the linked client at capture (grant-only —
	// capture never revokes an existing grant).
	ConsentMarketing bool `json:"consent_marketing"`
	// Links.
	ClientID           string `json:"client_id,omitempty"` // messaging identity
	ConvertedClientID  string `json:"converted_client_id,omitempty"`
	ConvertedPartnerID string `json:"converted_partner_id,omitempty"`
	ConvertedAccountID string `json:"converted_account_id,omitempty"`
	// Speed-to-lead SLA: stamped when the operator alert fires (once).
	SLAAlertedAt *time.Time `json:"sla_alerted_at,omitempty"`

	IdempotencyKey string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Open reports whether the lead is still workable (not converted, not lost).
func (l *Lead) Open() bool {
	return l.Status != LeadStatusConverted && l.Status != LeadStatusLost
}
