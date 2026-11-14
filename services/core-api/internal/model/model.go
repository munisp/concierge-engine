// Package model holds the shared domain model (Blueprint §13 data model).
package model

import (
	"strings"
	"time"
)

type Tenant struct {
	ID              string         `json:"id"`
	Slug            string         `json:"slug"`
	Name            string         `json:"name"`
	Archetype       string         `json:"archetype"` // flagship | smb | new_entrant
	Brand           map[string]any `json:"brand"`
	PricingRules    PricingRules   `json:"pricing_rules"`
	SLAConfig       SLAConfig      `json:"sla_config"`
	AutonomyDial    int            `json:"autonomy_dial"` // 0 draft-only .. 3 full-auto
	Tier2LimitMinor int64          `json:"tier2_limit_minor"`
	// Phase 3: data residency (§13 regional residency — EU primary, UAE
	// option) and cross-tenant referral opt-in (network effects require
	// explicit consent per tenant).
	Region           string    `json:"region"` // EU (default) | UAE
	AcceptsReferrals bool      `json:"accepts_referrals"`
	ReferralFeePct   float64   `json:"referral_fee_pct,omitempty"` // default 0.10
	CreatedAt        time.Time `json:"created_at"`
}

type PricingRules struct {
	Currency           string  `json:"currency"`
	DepositPct         float64 `json:"deposit_pct"`       // e.g. 0.30
	QuoteTTLSeconds    int64   `json:"quote_ttl_seconds"` // default 3600
	PlatformFeePct     float64 `json:"platform_fee_pct"`  // take rate 0.02–0.05
	PerKMAdjustmentPct float64 `json:"per_km_adjustment_pct"`
}

type SLAConfig struct {
	QuoteSLASeconds int64 `json:"quote_sla_seconds"` // Taylor's 1-hour promise: 3600
	// CaseSLAHours optionally overrides the platform defaults per priority
	// (P1=1, P2=4, P3=24, P4=72). Zero values fall back to the default.
	CaseSLAHours map[string]int `json:"case_sla_hours,omitempty"`
}

type Client struct {
	ID          string         `json:"id"`
	TenantID    string         `json:"tenant_id"`
	FullName    string         `json:"full_name,omitempty"`
	Email       string         `json:"email,omitempty"`
	Phone       string         `json:"phone,omitempty"`
	OrgID       string         `json:"org_id,omitempty"`
	VIPFlags    []string       `json:"vip_flags,omitempty"`
	LoyaltyTier string         `json:"loyalty_tier,omitempty"`
	KYCStatus   string         `json:"kyc_status"`
	Consent     map[string]any `json:"consent"`
	CreatedAt   time.Time      `json:"created_at"`
}

func (c Client) VIPFlagged() bool {
	for _, f := range c.VIPFlags {
		if f == "vip" || f == "executive" || f == "government" {
			return true
		}
	}
	return false
}

// ClientReferralCode derives a client's shareable referral code from their
// uuid: "VIP-" + first 8 hex chars, uppercase (Phase 14). Deterministic, so
// no column is needed — the code is always recomputed from the id.
func ClientReferralCode(clientID string) string {
	hex := strings.ToUpper(strings.ReplaceAll(clientID, "-", ""))
	if len(hex) < 8 {
		return ""
	}
	return "VIP-" + hex[:8]
}

// ReferralCodePrefix extracts the uuid-hex prefix from a referral code for
// store lookups; ok=false when the code is malformed.
func ReferralCodePrefix(code string) (string, bool) {
	c := strings.ToUpper(strings.TrimSpace(code))
	if !strings.HasPrefix(c, "VIP-") || len(c) != 12 {
		return "", false
	}
	p := c[4:]
	for _, r := range p {
		if !('0' <= r && r <= '9' || 'A' <= r && r <= 'F') {
			return "", false
		}
	}
	return p, true
}

type Preference struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	ClientID     string    `json:"client_id"`
	Key          string    `json:"key"`
	Value        string    `json:"value"`
	Encrypted    bool      `json:"encrypted,omitempty"` // VIP field-level encryption (Spec section 13)
	ConsentScope string    `json:"consent_scope"`
	SourceQuote  string    `json:"source_quote,omitempty"`
	RecordedBy   string    `json:"recorded_by"`
	RecordedAt   time.Time `json:"recorded_at"`
}

type CatalogService struct {
	ID           string `json:"id"`
	TenantID     string `json:"tenant_id"`
	Vertical     string `json:"vertical"`
	Name         string `json:"name"`
	BaseMinor    int64  `json:"base_minor"`
	Currency     string `json:"currency"`
	PerKMMinor   int64  `json:"per_km_minor"`
	PerHourMinor int64  `json:"per_hour_minor"`
	AutoQuotable bool   `json:"auto_quotable"`
	Active       bool   `json:"active"`
}

// RequestState mirrors booking/v1 RequestState.
type RequestState string

const (
	StateNew             RequestState = "NEW"
	StateCapturing       RequestState = "CAPTURING"
	StateQuoting         RequestState = "QUOTING"
	StateNegotiating     RequestState = "NEGOTIATING"
	StateAwaitingPayment RequestState = "AWAITING_PAYMENT"
	StateConfirmed       RequestState = "CONFIRMED"
	StateInService       RequestState = "IN_SERVICE"
	StateCompleted       RequestState = "COMPLETED"
	StateFollowUp        RequestState = "FOLLOW_UP"
	StateEscalated       RequestState = "ESCALATED"
	StateCancelled       RequestState = "CANCELLED"
)

type ServiceItem struct {
	ID             string            `json:"id"`
	TenantID       string            `json:"tenant_id,omitempty"`
	RequestID      string            `json:"request_id,omitempty"`
	Vertical       string            `json:"vertical"`
	PickupAddress  string            `json:"pickup_address"`
	PickupLat      float64           `json:"pickup_lat,omitempty"`
	PickupLng      float64           `json:"pickup_lng,omitempty"`
	DropoffAddress string            `json:"dropoff_address,omitempty"`
	DropoffLat     float64           `json:"dropoff_lat,omitempty"`
	DropoffLng     float64           `json:"dropoff_lng,omitempty"`
	PickupAt       int64             `json:"pickup_at"`
	Passengers     int               `json:"passengers"`
	VehicleClass   string            `json:"vehicle_class,omitempty"`
	Hours          int               `json:"hours,omitempty"`
	Notes          string            `json:"notes,omitempty"`
	Attributes     map[string]string `json:"attributes,omitempty"`
}

type ServiceRequest struct {
	ID             string        `json:"id"`
	TenantID       string        `json:"tenant_id"`
	ClientID       string        `json:"client_id"`
	ConversationID string        `json:"conversation_id,omitempty"`
	State          RequestState  `json:"state"`
	Version        int           `json:"version"`
	Items          []ServiceItem `json:"items"`
	IdempotencyKey string        `json:"-"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

type QuoteLine struct {
	Description string `json:"description"`
	AmountMinor int64  `json:"amount_minor"`
	ItemID      string `json:"item_id,omitempty"`
}

type Quote struct {
	ID              string      `json:"id"`
	TenantID        string      `json:"tenant_id"`
	RequestID       string      `json:"request_id"`
	Version         int         `json:"version"`
	Origin          string      `json:"origin"` // INSTANT | HUMAN
	TotalMinor      int64       `json:"total_minor"`
	DepositMinor    int64       `json:"deposit_minor"`
	Currency        string      `json:"currency"`
	Lines           []QuoteLine `json:"lines"`
	ExpiresAt       time.Time   `json:"expires_at"`
	PolicyVerdictID string      `json:"policy_verdict_id,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
}

type Booking struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	RequestID string    `json:"request_id"`
	QuoteID   string    `json:"quote_id"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

type FleetResource struct {
	ID           string  `json:"id"`
	TenantID     string  `json:"tenant_id"`
	Kind         string  `json:"kind"`
	Label        string  `json:"label"`
	PartnerID    string  `json:"partner_id,omitempty"`
	QualityScore float64 `json:"quality_score"`
	Active       bool    `json:"active"`
}

type CrewMember struct {
	ID           string   `json:"id"`
	TenantID     string   `json:"tenant_id"`
	FullName     string   `json:"full_name"`
	Certificates []string `json:"certificates"`
	QualityScore float64  `json:"quality_score"`
	Active       bool     `json:"active"`
}

type Assignment struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenant_id"`
	BookingID       string    `json:"booking_id"`
	FleetResourceID string    `json:"fleet_resource_id,omitempty"`
	CrewMemberID    string    `json:"crew_member_id,omitempty"`
	PartnerID       string    `json:"partner_id,omitempty"`
	Status          string    `json:"status"`
	SLADeadline     time.Time `json:"sla_deadline"`
	CreatedAt       time.Time `json:"created_at"`
}

type GeoStamp struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	AssignmentID string    `json:"assignment_id"`
	Kind         string    `json:"kind"`
	Lat          float64   `json:"lat"`
	Lng          float64   `json:"lng"`
	AccuracyM    float64   `json:"accuracy_m"`
	At           time.Time `json:"at"`
}

type PaymentIntent struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	BookingID      string    `json:"booking_id,omitempty"`
	QuoteID        string    `json:"quote_id,omitempty"`
	Mode           string    `json:"mode"` // FULL | DEPOSIT_BALANCE | PREAUTH_CAPTURE | MILESTONE | INVOICE
	AmountMinor    int64     `json:"amount_minor"`
	Currency       string    `json:"currency"`
	PSP            string    `json:"psp"`
	PSPRef         string    `json:"psp_ref,omitempty"`
	HostedURL      string    `json:"hosted_url,omitempty"`
	State          string    `json:"state"`
	IdempotencyKey string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}

type LedgerPosting struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	BookingID      string    `json:"booking_id,omitempty"`
	DebitCode      string    `json:"debit_code"`
	CreditCode     string    `json:"credit_code"`
	AmountMinor    int64     `json:"amount_minor"`
	Currency       string    `json:"currency"`
	TBTransferID   string    `json:"tb_transfer_id,omitempty"`
	IdempotencyKey string    `json:"idempotency_key"`
	CreatedAt      time.Time `json:"created_at"`
}

type Conversation struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	ClientID    string    `json:"client_id,omitempty"`
	Channel     string    `json:"channel"`
	State       string    `json:"state"`
	ExternalRef string    `json:"external_ref,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type Message struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenant_id"`
	ConversationID  string    `json:"conversation_id"`
	Author          string    `json:"author"` // CLIENT | AI | HUMAN_STAFF | SYSTEM
	Body            string    `json:"body"`
	PolicyVerdictID string    `json:"policy_verdict_id,omitempty"`
	SentAt          time.Time `json:"sent_at"`
}

type HandoffItem struct {
	ID             string         `json:"id"`
	TenantID       string         `json:"tenant_id"`
	ConversationID string         `json:"conversation_id"`
	Trigger        string         `json:"trigger"` // VALUE | CONFIDENCE | SENTIMENT | POLICY | SLA
	Reason         string         `json:"reason"`
	Brief          map[string]any `json:"brief"`
	PendingAction  map[string]any `json:"pending_action,omitempty"`
	Status         string         `json:"status"` // OPEN | APPROVED | EDITED | REJECTED | RESOLVED
	Owner          string         `json:"owner,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

type OutboxEvent struct {
	ID          int64          `json:"id"`
	TenantID    string         `json:"tenant_id"`
	Aggregate   string         `json:"aggregate"`
	AggregateID string         `json:"aggregate_id"`
	EventType   string         `json:"event_type"`
	Payload     map[string]any `json:"payload"`
	CreatedAt   time.Time      `json:"created_at"`
}

type AttributionEvent struct {
	TenantID   string `json:"tenant_id"`
	Kind       string `json:"kind"`
	Channel    string `json:"channel,omitempty"`
	Campaign   string `json:"campaign,omitempty"`
	ClientID   string `json:"client_id,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
	ValueMinor int64  `json:"value_minor,omitempty"`
}
