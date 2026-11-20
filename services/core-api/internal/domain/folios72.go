package domain

// Phase 72: dunning over email. Phase 67's escalating reminder cadence
// only spoke WhatsApp — a phoneless partner (a hotel desk whose billing
// contact is an inbox, not a handset) was silently unreachable: every
// rung recorded skipped:no-contact and the debt aged untouched. Phase
// 71 gave the partner a billing email; this phase lets the dunning
// engine use it. The cadence, the one-rung-per-sweep discipline and the
// verbatim ledger are unchanged — only the lane widened.

import (
	"context"
	"fmt"

	"concierge-engine/services/core-api/internal/model"
)

// sendReminderEmail delivers reminder N to the partner's billing email
// and returns the status to record verbatim. Same message facts as the
// WhatsApp rung — outstanding balance (never just the face value),
// days past due against tenant terms — plus the printable statement
// document link (Phase 71), which is the page an accounts inbox
// actually needs. No adapter configured is an honest skip.
func (svc *FolioService) sendReminderEmail(ctx context.Context, tenantID string,
	p *model.Partner, f *model.Folio, n, overdue, terms int) string {
	if svc.s.emailURL == "" {
		return "skipped:no-adapter"
	}
	outstanding := f.OutstandingMinor()
	subject := fmt.Sprintf("Payment reminder %d — %s statement, %d.%02d %s, %d days past due",
		n, f.Period, outstanding/100, abs64(outstanding)%100, f.Currency, overdue)
	body := fmt.Sprintf("Dear %s,\n\nThis is reminder %d: the statement for %s was issued on %s and is now %d days past due (terms %d days).\n\nOutstanding: %d.%02d %s\n\nView, print or save your statement: %s\n\nIf payment is already on its way, please disregard this note.",
		p.Name, n, f.Period, f.IssuedAt.Format("2006-01-02"), overdue, terms,
		outstanding/100, abs64(outstanding)%100, f.Currency,
		svc.StatementURL(tenantID, f.ID))
	status, detail, _ := postToAdapter(ctx, svc.s.emailURL, []byte(fmt.Sprintf(
		`{"tenant_id":%q,"to":%q,"subject":%q,"body":%q}`, tenantID, p.Email, subject, body)))
	return deliveryStatus(status, detail)
}
