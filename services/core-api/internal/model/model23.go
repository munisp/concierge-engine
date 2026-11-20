package model

import "time"

// Phase 73: credit notes & refunds on folios. A statement can be wrong —
// a booking was cancelled after issue, a line was mispriced, goodwill is
// owed. The correction is a verbatim FolioCredit row on the folio itself
// (same ledger discipline as reminders, payments and deliveries), never
// an edit of the frozen lines:
//
//   - Against an ISSUED folio the row is a credit note (kind "credit"):
//     it reduces what the partner still owes. Credited, paid and
//     outstanding are all DERIVED — the statement, aging and dunning
//     recompute from the adjusted total automatically.
//   - Against a SETTLED folio the row is a refund (kind "refund"): the
//     money was already collected, so the credit carries reversal
//     ledger postings (money truth flows back), and cumulative refunds
//     can never exceed what was actually paid.
//
// Over-credit is rejected: you cannot credit money the partner already
// paid on an open statement (that path is a 409, not silent absorption).

const (
	FolioCreditNote   = "credit" // reduces the balance of an issued statement
	FolioCreditRefund = "refund" // returns money already collected on a settled one
)

// FolioCredit is one credit note or refund against a folio.
type FolioCredit struct {
	ID          string    `json:"id"`
	AmountMinor int64     `json:"amount_minor"`
	Kind        string    `json:"kind"` // credit | refund
	Reason      string    `json:"reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	RecordedBy  string    `json:"recorded_by"` // operator | system
}

// CreditedMinor is the sum of credit rows on the folio, derived from the
// credit ledger — never stored twice, so the ledger and the balance can
// never disagree.
func (f *Folio) CreditedMinor() int64 {
	var credited int64
	for _, c := range f.Credits {
		credited += c.AmountMinor
	}
	return credited
}

// AdjustedTotalMinor is the frozen face value minus credits: what the
// statement is actually for after corrections. Clamped at zero — an
// over-credit is rejected upstream, but derivation never trusts blindly.
func (f *Folio) AdjustedTotalMinor() int64 {
	adj := f.TotalMinor - f.CreditedMinor()
	if adj < 0 {
		return 0
	}
	return adj
}
