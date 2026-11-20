package model

import "time"

// Phase 67: folio aging & dunning. An ISSUED bill-to-desk folio is money
// owed; aging is DERIVED from IssuedAt against the tenant's payment terms
// (never stored, so it can never drift), and dunning is an escalating
// reminder cadence whose evidence lives on the folio itself — every nudge
// (or honestly-recorded skip) is a FolioReminder row.

// FolioReminderCadence is the escalation ladder in days past due:
// reminder 1 fires the day after terms lapse, then at 7, 14 and 30 days.
// A folio never gets more reminders than the ladder has rungs.
var FolioReminderCadence = []int{1, 7, 14, 30}

// Recorded reminder statuses. "sent" means the channel adapter accepted
// the message; anything else is the verbatim truth of why it did not
// leave ("skipped:no-contact", "skipped:no-adapter", "failed:…") — a
// reminder is never marked sent that was not.
const (
	FolioReminderSent = "sent"
)

// FolioReminder is one dunning touch against an issued folio.
type FolioReminder struct {
	N         int       `json:"n"`       // 1-based position on the cadence ladder
	Channel   string    `json:"channel"` // whatsapp
	Status    string    `json:"status"`  // sent | skipped:… | failed:… (verbatim)
	CreatedAt time.Time `json:"created_at"`
}

// DueAt is when payment falls due: issue time plus the terms in days.
func (f *Folio) DueAt(termsDays int) time.Time {
	return f.IssuedAt.Add(time.Duration(termsDays) * 24 * time.Hour)
}

// OverdueDays is the number of whole days the folio is past due at `now`;
// zero or negative means it is still inside terms.
func (f *Folio) OverdueDays(now time.Time, termsDays int) int {
	return int(now.Sub(f.DueAt(termsDays)).Hours() / 24)
}

// NextReminderDue returns the 1-based number of the next reminder owed at
// `now`, or 0 when none is due. Only ISSUED folios are dunned; the next
// rung is len(Reminders)+1 and it fires once the folio is at least that
// rung's cadence days overdue — one reminder per evaluation, so a
// long-overdue folio catches up one rung per sweep instead of bursting.
func (f *Folio) NextReminderDue(now time.Time, termsDays int) int {
	if f.State != FolioIssued {
		return 0
	}
	n := len(f.Reminders) + 1
	if n > len(FolioReminderCadence) {
		return 0
	}
	if f.OverdueDays(now, termsDays) >= FolioReminderCadence[n-1] {
		return n
	}
	return 0
}

// FolioAgingBucket classifies days-overdue into the operator buckets:
// current (inside terms), 1–7, 8–30, 30+.
func FolioAgingBucket(overdueDays int) string {
	switch {
	case overdueDays <= 0:
		return "current"
	case overdueDays <= 7:
		return "1_7"
	case overdueDays <= 30:
		return "8_30"
	default:
		return "30_plus"
	}
}

// FolioAgingRow aggregates one partner's outstanding issued folios for
// one currency. Currencies are never blended — each gets its own row,
// same rule as the channel margin analytics.
type FolioAgingRow struct {
	PartnerID          string `json:"partner_id"`
	PartnerName        string `json:"partner_name"`
	Currency           string `json:"currency"`
	TermsDays          int    `json:"terms_days"`
	OpenFolios         int    `json:"open_folios"` // issued, unsettled statements
	CurrentMinor       int64  `json:"current_minor"`
	Overdue1To7Minor   int64  `json:"overdue_1_7_minor"`
	Overdue8To30Minor  int64  `json:"overdue_8_30_minor"`
	Overdue30PlusMinor int64  `json:"overdue_30_plus_minor"`
	// TotalMinor is the OUTSTANDING receivable (Phase 70: after partial
	// payments, aging chases the balance, not the face value);
	// CollectedMinor is what the partner has already paid against the
	// row's open folios.
	TotalMinor     int64 `json:"total_minor"`
	CollectedMinor int64 `json:"collected_minor"`
	// OldestOverdueDays is the worst-case age across the row's folios —
	// the sort key for the operator's collection queue.
	OldestOverdueDays int `json:"oldest_overdue_days"`
}

// FolioAgingReport is the tenant-wide receivables view over issued,
// unsettled folios. Derived on read from the folio ledger — there is no
// aging table to disagree with the statements.
type FolioAgingReport struct {
	TermsDays int             `json:"terms_days"`
	Rows      []FolioAgingRow `json:"rows"`
}
