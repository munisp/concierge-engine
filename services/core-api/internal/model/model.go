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
	Region           string  `json:"region"` // EU (default) | UAE
	AcceptsReferrals bool    `json:"accepts_referrals"`
	ReferralFeePct   float64 `json:"referral_fee_pct,omitempty"` // default 0.10
	// Phase 17: tax + payment configuration (EU/US launch readiness).
	TaxConfig     TaxConfig     `json:"tax_config"`
	PaymentConfig PaymentConfig `json:"payment_config"`
	// Phase 19: two-sided reward sharing program.
	RewardProgram RewardProgram `json:"reward_program"`
	// Phase 20: per-tenant outreach configuration.
	OutreachConfig OutreachConfig `json:"outreach_config"`
	// Phase 21: per-tenant operator roster — SLA/failure alert recipients.
	// Empty roster = platform fallback (CE_OPERATOR_PHONE).
	OperatorConfig OperatorConfig `json:"operator_config"`
	// Phase 22: dispatch tuning.
	DispatchConfig DispatchConfig `json:"dispatch_config"`
	// LifecycleConfig (Phase 25): always-on engagement — post-trip follow-up
	// and win-back rules evaluated by the follow-up sweeper.
	LifecycleConfig LifecycleConfig `json:"lifecycle_config"`
	CreatedAt       time.Time       `json:"created_at"`
}

// DispatchConfig tunes scheduling. TurnaroundMinutes extends every
// assignment's busy window on both sides for conflict purposes (vehicle
// repositioning / cleanup); DefaultDurationMinutes sizes the window when a
// request item carries no hours. Zeros = platform defaults (30 / 120).
type DispatchConfig struct {
	TurnaroundMinutes      int `json:"turnaround_minutes,omitempty"`
	DefaultDurationMinutes int `json:"default_duration_minutes,omitempty"`
}

// LifecycleConfig is the tenant's always-on client-engagement posture
// (Phase 25). PostTripHours after a completed trip the client gets a
// service-purpose thank-you/follow-up; WinBackDays of silence marks a
// client for a marketing win-back attempt (consent- and cap-gated by the
// outreach engine — held, never forced). Disabled = the sweeper skips the
// tenant entirely. Zeros fall back to 24h / 90d.
type LifecycleConfig struct {
	Disabled      bool `json:"disabled,omitempty"`
	PostTripHours int  `json:"post_trip_hours,omitempty"`
	WinBackDays   int  `json:"win_back_days,omitempty"`
}

// OperatorConfig is the tenant's operator roster. AlertPhones receive
// WhatsApp alerts on SLA-relevant failures (e.g. a failed delivery of an
// approved case reply). Empty = the platform-wide CE_OPERATOR_PHONE.
type OperatorConfig struct {
	AlertPhones []string `json:"alert_phones,omitempty"`
}

// TaxConfig is the tenant's tax posture. Engine picks the calculator:
// "" (default) = none (B2B/exempt or not registered — totals carry no tax);
// "flat" = tenant-configured rate (VAT/GST/sales tax) applied to the service
// subtotal; "stripe-tax" = live Stripe Tax calculation per quote.
type TaxConfig struct {
	Engine           string `json:"engine,omitempty"`             // flat | stripe-tax | "" (none)
	Country          string `json:"country,omitempty"`            // ISO 3166-1 alpha-2 of the tax registration
	VATRateBP        int    `json:"vat_rate_bp,omitempty"`        // basis points, e.g. 1900 = 19%
	PricesIncludeTax bool   `json:"prices_include_tax,omitempty"` // EU B2C convention
}

// PaymentConfig lets a tenant disable PSPs it has no account/entity for
// (e.g. an EU-only tenant disables Paystack; a Nigeria-only tenant disables
// Stripe). Both false = route purely by currency.
type PaymentConfig struct {
	DisableStripe   bool `json:"disable_stripe,omitempty"`
	DisablePaystack bool `json:"disable_paystack,omitempty"`
}

type PricingRules struct {
	Currency           string  `json:"currency"`
	DepositPct         float64 `json:"deposit_pct"`       // e.g. 0.30
	QuoteTTLSeconds    int64   `json:"quote_ttl_seconds"` // default 3600
	PlatformFeePct     float64 `json:"platform_fee_pct"`  // take rate 0.02–0.05
	PerKMAdjustmentPct float64 `json:"per_km_adjustment_pct"`
	// ReferralRewardPct is the referrer's reward as a fraction of the
	// referee's first booking value (Phase 18). Default 0.05 when unset.
	ReferralRewardPct float64 `json:"referral_reward_pct,omitempty"`
}

// RewardProgram is the tenant's two-sided reward sharing configuration
// (Phase 19). The referrer earns ReferrerPct of the referee's first booking
// value; the referee earns RefereePct as a welcome credit on the same
// booking. Zero RefereePct means no referee credit. ReferrerPct zero falls
// back to the legacy PricingRules.ReferralRewardPct, then to 0.05.
// MaxRewardMinor caps each accrual (0 = uncapped); ExpiryDays sets credit
// validity (0 = never expires — never silently burn a client's credit).
type RewardProgram struct {
	Disabled       bool    `json:"disabled,omitempty"`
	ReferrerPct    float64 `json:"referrer_pct,omitempty"`
	RefereePct     float64 `json:"referee_pct,omitempty"`
	MaxRewardMinor int64   `json:"max_reward_minor,omitempty"`
	ExpiryDays     int     `json:"expiry_days,omitempty"`
}

// OutreachConfig holds per-tenant proactive-outreach settings (Phase 20).
// MarketingCap7d overrides the platform default of 3 marketing sends per
// client per rolling 7 days; 0 = platform default.
type OutreachConfig struct {
	MarketingCap7d int `json:"marketing_cap_7d,omitempty"`
}

// PromoCampaign is a fixed-credit promo code (Phase 20): a client redeeming
// the code accrues credit_minor of reward credit, once per client. The
// credit expires with the campaign (expires_at); max_redemptions 0 =
// unlimited.
type PromoCampaign struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	Code           string    `json:"code"`
	CreditMinor    int64     `json:"credit_minor"`
	Currency       string    `json:"currency"`
	MaxRedemptions int       `json:"max_redemptions,omitempty"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
	Active         bool      `json:"active"`
	CreatedAt      time.Time `json:"created_at"`
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
	Locale      string         `json:"locale,omitempty"`   // BCP-47, e.g. "en-NG" — checkout/outreach localization
	Timezone    string         `json:"timezone,omitempty"` // IANA, e.g. "Africa/Lagos"
	// Phase 16 (GDPR Art. 17 / CCPA): erasure tombstone. Once set the record
	// is anonymized (PII fields scrubbed) and kept only as a pseudonymous
	// anchor for financial/booking records the platform must retain.
	ErasedAt  time.Time `json:"erased_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Erased reports whether this client has exercised the right to erasure.
func (c Client) Erased() bool { return !c.ErasedAt.IsZero() }

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
	Origin          string      `json:"origin"`      // INSTANT | HUMAN
	TotalMinor      int64       `json:"total_minor"` // grand total, tax included
	DepositMinor    int64       `json:"deposit_minor"`
	Currency        string      `json:"currency"`
	TaxMinor        int64       `json:"tax_minor"`            // Phase 17: tax component of TotalMinor
	TaxEngine       string      `json:"tax_engine,omitempty"` // flat | stripe-tax | "" (none) — provenance
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
	// Phase 22: the service window. Resolved from the booking's request item
	// (pickup_at + hours, else tenant default duration) or set explicitly.
	// Zero window = schedule unknown; conflict gating only applies to
	// windowed assignments.
	StartAt   time.Time `json:"start_at,omitempty"`
	EndAt     time.Time `json:"end_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
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
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	BookingID   string `json:"booking_id,omitempty"`
	QuoteID     string `json:"quote_id,omitempty"`
	Mode        string `json:"mode"` // FULL | DEPOSIT_BALANCE | PREAUTH_CAPTURE | MILESTONE | INVOICE
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	PSP         string `json:"psp"`
	PSPRef      string `json:"psp_ref,omitempty"`
	HostedURL   string `json:"hosted_url,omitempty"`
	State       string `json:"state"`
	// RewardAppliedMinor is the portion of AmountMinor covered by the
	// client's accrued referral rewards instead of the PSP charge
	// (Phase 18). PSP charge = AmountMinor - RewardAppliedMinor; the
	// platform subsidizes the reward via a compensation posting.
	RewardAppliedMinor int64     `json:"reward_applied_minor,omitempty"`
	IdempotencyKey     string    `json:"-"`
	CreatedAt          time.Time `json:"created_at"`
}

// ClientReward is credit accrued under the tenant's reward sharing program
// (Phase 18 referrer credit; Phase 19 two-sided: the referee also earns a
// welcome credit). ACCRUED rewards reduce the client's next payment link
// (platform-subsidized — the ledger compensation posting keeps the tenant
// whole); REDEEMED rewards are spent; EXPIRED rewards lapsed unused
// (only when the program sets ExpiryDays — credit never expires silently).
type ClientReward struct {
	ID               string    `json:"id"`
	TenantID         string    `json:"tenant_id"`
	ClientID         string    `json:"client_id"`            // who holds the credit (referrer, referee, promo redeemer)
	SourceBookingID  string    `json:"source_booking_id"`    // referee's first booking; empty for promo credit
	Kind             string    `json:"kind"`                 // referral_reward | referee_welcome | promo_credit
	PromoCode        string    `json:"promo_code,omitempty"` // set for promo_credit (Phase 20)
	AmountMinor      int64     `json:"amount_minor"`
	Currency         string    `json:"currency"`
	State            string    `json:"state"` // ACCRUED | REDEEMED | EXPIRED
	RedeemedIntentID string    `json:"redeemed_intent_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	RedeemedAt       time.Time `json:"redeemed_at,omitempty"`
	ExpiresAt        time.Time `json:"expires_at,omitempty"`
}

const (
	RewardAccrued  = "ACCRUED"
	RewardRedeemed = "REDEEMED"
	RewardExpired  = "EXPIRED"
)

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
