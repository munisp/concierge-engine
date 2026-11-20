package model

import "time"

// Phase 70: partial payments against issued folios. A hotel desk rarely
// pays a statement in one shot — it pays what it has collected, when it
// has collected it. The payment ledger lives on the folio row (like the
// Phase 67 reminders ledger): every amount received is a verbatim
// FolioPayment row, and paid/outstanding are DERIVED from it — never
// stored twice, so the ledger and the balance can never disagree.

// FolioPayment is one amount received against an issued folio.
type FolioPayment struct {
	ID          string    `json:"id"`
	AmountMinor int64     `json:"amount_minor"`
	Method      string    `json:"method"` // bank_transfer | cash | card | settle | other
	Reference   string    `json:"reference,omitempty"`
	ReceivedAt  time.Time `json:"received_at"`
	RecordedBy  string    `json:"recorded_by"` // operator | system
}

// PaidMinor is the sum received so far, derived from the payment ledger.
func (f *Folio) PaidMinor() int64 {
	var paid int64
	for _, p := range f.Payments {
		paid += p.AmountMinor
	}
	return paid
}

// OutstandingMinor is what is still owed. Clamped at zero — an
// overpayment is rejected upstream, so a negative balance cannot occur,
// but derivation never trusts that blindly.
func (f *Folio) OutstandingMinor() int64 {
	out := f.TotalMinor - f.PaidMinor()
	if out < 0 {
		return 0
	}
	return out
}
