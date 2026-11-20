package model

import "time"

// Phase 66: partner bill-to-desk folios — monthly consolidated settlement
// for demand the partner sourced. A hotel desk that books rides for guests
// does not charge each guest through the platform; instead every sourced
// CONFIRMED booking's adjusted gross (the quote total, after Phase 65
// rate-card repricing) accrues as a line on the partner's OPEN folio for
// the booking month, and the platform issues one folio per
// (partner, currency, period) for settlement on terms.

const (
	FolioOpen    = "OPEN"    // accruing lines for the current period
	FolioIssued  = "ISSUED"  // closed and issued to the partner for payment
	FolioSettled = "SETTLED" // paid — ledger postings recorded
)

// FolioPeriod derives the billing period ("2006-01") from a booking's
// confirm time. UTC so operator and partner never argue about the cutoff.
func FolioPeriod(t time.Time) string { return t.UTC().Format("2006-01") }

// FolioLine is one sourced booking's charge on the folio. AmountMinor is
// the quote's adjusted gross at confirm time — snapshot semantics: later
// rate-card changes never rewrite an issued folio.
type FolioLine struct {
	BookingID   string    `json:"booking_id"`
	QuoteID     string    `json:"quote_id"`
	GuestName   string    `json:"guest_name,omitempty"`
	AmountMinor int64     `json:"amount_minor"`
	CreatedAt   time.Time `json:"created_at"`
}

// Folio is one partner's bill-to-desk statement for one period and
// currency. TotalMinor is the sum of lines, recomputed on every accrual
// and frozen at issue.
type Folio struct {
	ID         string      `json:"id"`
	TenantID   string      `json:"tenant_id"`
	PartnerID  string      `json:"partner_id"`
	Period     string      `json:"period"` // YYYY-MM
	Currency   string      `json:"currency"`
	State      string      `json:"state"` // OPEN | ISSUED | SETTLED
	TotalMinor int64       `json:"total_minor"`
	Lines      []FolioLine `json:"lines,omitempty"`
	CreatedAt  time.Time   `json:"created_at"`
	IssuedAt   time.Time   `json:"issued_at,omitempty"`
	SettledAt  time.Time   `json:"settled_at,omitempty"`
}
