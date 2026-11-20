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

// OutstandingMinor is what is still owed: the credit-adjusted total
// minus what has been paid (Phase 73 — aging and dunning chase the
// adjusted balance, so a credit note immediately shrinks the debt).
// Clamped at zero — overpayments and over-credits are rejected upstream,
// so a negative balance cannot occur, but derivation never trusts that
// blindly.
func (f *Folio) OutstandingMinor() int64 {
	out := f.AdjustedTotalMinor() - f.PaidMinor()
	if out < 0 {
		return 0
	}
	return out
}

// Phase 71: statement delivery. A statement the partner never receives
// is a statement that never gets paid — every delivery of the folio
// document itself (link sent over WhatsApp or email) is recorded
// verbatim on the folio, same discipline as the Phase 67 reminders
// ledger: "sent" only when the channel adapter accepted it, otherwise
// the honest skip/failure reason. The partner-visible statement page
// is derived on read from this ledger plus the lines/payments, so the
// document and the delivery evidence can never disagree.

// FolioDelivery is one delivery of the statement document to the
// partner. Kind leaves room for later artifacts (credit notes);
// Phase 71 delivers kind "statement" only.
type FolioDelivery struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`    // statement
	Channel   string    `json:"channel"` // whatsapp | email
	Status    string    `json:"status"`  // sent | skipped:reason | failed:reason
	CreatedAt time.Time `json:"created_at"`
}
