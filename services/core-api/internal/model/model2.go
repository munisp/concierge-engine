// Phase 2 domain models: partner network, corporate accounts, payout saga
// (Master Spec section 11.3), corporate invoicing and the audit log.
package model

import "time"

// Partner is a vetted supplier in the partner network (ground, aviation,
// security, events...). Partner routing reuses the dispatch assignment path.
type Partner struct {
	ID           string   `json:"id"`
	TenantID     string   `json:"tenant_id"`
	Name         string   `json:"name"`
	Verticals    []string `json:"verticals"`
	Markets      []string `json:"markets"`
	Status       string   `json:"status"` // PENDING | VETTED | SUSPENDED
	QualityScore float64  `json:"quality_score"`
	PayoutRail   string   `json:"payout_rail"` // MOJALOOP | MANUAL
	PayoutRef    string   `json:"payout_ref,omitempty"`
	// Phase 56: commission on partner-routed confirmed bookings. Accrues as
	// a PENDING payout (idempotent per booking+partner) that flows through
	// the Phase 48 settlement batching like any other payout.
	CommissionPct float64 `json:"commission_pct,omitempty"`
	// Phase 57: WhatsApp number (E.164) for assignment offers. Empty = the
	// partner is portal-link only; offers are shared by the operator.
	Phone string `json:"phone,omitempty"`
	// Phase 63: demand-channel kind (hotel | travel_agency |
	// transport_company | tour_operator | other). Drives the sourcing
	// workflow and margin analytics; legacy rows default to tour_operator.
	Kind string `json:"kind,omitempty"`
	// Phase 66: bill-to-desk — sourced bookings accrue onto a monthly folio
	// settled by the partner on terms, instead of per-guest payment.
	BillToDesk bool      `json:"bill_to_desk,omitempty"`
	Lat        float64   `json:"lat,omitempty"` // base-of-operations for proximity ranking (Phase 6)
	Lng        float64   `json:"lng,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// CorporateAccount: consolidated monthly billing for EA bookers (Phase 2).
type CorporateAccount struct {
	ID               string    `json:"id"`
	TenantID         string    `json:"tenant_id"`
	Name             string    `json:"name"`
	BillingEmail     string    `json:"billing_email"`
	PaymentTermsDays int       `json:"payment_terms_days"`
	CreditLimitMinor int64     `json:"credit_limit_minor"`
	Currency         string    `json:"currency"`
	CreatedAt        time.Time `json:"created_at"`
}

// Payout states for the cross-border partner payout saga (Spec section 11.3).
const (
	PayoutPending     = "PENDING"
	PayoutQuoted      = "QUOTED"
	PayoutInFlight    = "IN_FLIGHT"
	PayoutSettled     = "SETTLED"
	PayoutCompensated = "COMPENSATED"
	PayoutFailed      = "FAILED"
)

// Payout is one partner settlement saga instance. Money truth lives in
// TigerBeetle; this row tracks the saga state for recovery/compensation.
type Payout struct {
	ID           string  `json:"id"`
	TenantID     string  `json:"tenant_id"`
	PartnerID    string  `json:"partner_id"`
	BookingID    string  `json:"booking_id,omitempty"`
	AmountMinor  int64   `json:"amount_minor"`
	Currency     string  `json:"currency"`
	Rail         string  `json:"rail"` // MOJALOOP | MANUAL
	State        string  `json:"state"`
	QuoteID      string  `json:"quote_id,omitempty"`
	TransferID   string  `json:"transfer_id,omitempty"`
	FXRate       float64 `json:"fx_rate,omitempty"`
	ReceiveMinor int64   `json:"receive_minor,omitempty"`
	FeesMinor    int64   `json:"fees_minor,omitempty"`
	// Phase 48: quote_expires_at enforces the scheme's FX lock — executing
	// against an expired quote is a 409, re-quote instead. Zero = the
	// adapter quoted no expiry (legacy rows, manual rail).
	QuoteExpiresAt time.Time `json:"quote_expires_at,omitempty"`
	// BatchID links the payout into a settlement batch (Phase 48): the
	// partner's pending payouts net into ONE scheme transfer instead of
	// paying a scheme fee per payout.
	BatchID string `json:"batch_id,omitempty"`
	// SettledAt stamps terminal fulfilment for the statement/reconciliation
	// surface.
	SettledAt      time.Time `json:"settled_at,omitempty"`
	IdempotencyKey string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// CorporateInvoice: monthly consolidated invoice (payment mode INVOICE).
type CorporateInvoice struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	AccountID      string    `json:"account_id"`
	PeriodStart    time.Time `json:"period_start"`
	PeriodEnd      time.Time `json:"period_end"`
	TotalMinor     int64     `json:"total_minor"`
	Currency       string    `json:"currency"`
	State          string    `json:"state"` // ISSUED | PAID | VOID
	IdempotencyKey string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}

// AuditEntry: one sensitive-access record (Master Spec section 13).
// Append-only; written on every sensitive read.
type AuditEntry struct {
	ID         int64          `json:"id"`
	TenantID   string         `json:"tenant_id"`
	Actor      string         `json:"actor"` // api | ai | staff subject
	Action     string         `json:"action"`
	Resource   string         `json:"resource"`
	ResourceID string         `json:"resource_id"`
	Detail     map[string]any `json:"detail,omitempty"`
	At         time.Time      `json:"at"`
}
