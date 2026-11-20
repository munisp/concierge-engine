package domain

// Phase 67: folio aging & dunning. Phase 66 made partner money owed a
// first-class statement; this phase makes it COLLECTED:
//
//   - Aging is a derived receivables view over ISSUED folios — computed
//     from IssuedAt against the tenant's payment terms on every read,
//     bucketed current / 1–7 / 8–30 / 30+ days overdue, one row per
//     partner per currency (currencies never blend). There is no aging
//     table that could disagree with the statements.
//   - Dunning is an escalating reminder cadence (1/7/14/30 days past
//     due) delivered to the partner over their best channel —
//     WhatsApp first, the Phase 71 billing email otherwise (Phase
//     72). Every touch — sent, failed, or skipped with the verbatim
//     reason — is recorded on the folio's own reminders ledger, so
//     the statement carries its own collection evidence and the
//     cadence is derived, never stored twice. One rung per sweep: a
//     long-overdue folio catches up gradually instead of bursting
//     four messages in a minute.
//   - A reminder is never marked sent that was not, and a settled or
//     open folio is never dunned.

import (
	"context"
	"fmt"
	"sort"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// DunningResult is the sweep receipt — how many reminders were delivered
// vs recorded with a skip/failure status this run.
type DunningResult struct {
	Sent    int `json:"sent"`
	Skipped int `json:"skipped"`
}

// Aging builds the tenant's folio receivables view: every issued,
// unsettled statement grouped by partner and currency, bucketed against
// the tenant's payment terms. Sorted most-overdue first — the top row is
// the next collection call to make.
func (svc *FolioService) Aging(ctx context.Context, tenantID string) (*model.FolioAgingReport, error) {
	tenant, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	terms := tenant.PricingRules.FolioTerms()
	issued, err := svc.s.Store.Folios().ListIssued(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	now := svc.s.Now()
	rows := map[string]*model.FolioAgingRow{}
	for i := range issued {
		f := &issued[i]
		key := f.PartnerID + "/" + f.Currency
		row := rows[key]
		if row == nil {
			row = &model.FolioAgingRow{PartnerID: f.PartnerID, Currency: f.Currency, TermsDays: terms}
			if p, err := svc.s.Store.Partners().Get(ctx, tenantID, f.PartnerID); err == nil {
				row.PartnerName = p.Name
			}
			rows[key] = row
		}
		overdue := f.OverdueDays(now, terms)
		outstanding := f.OutstandingMinor() // Phase 70: chase the balance, not the face value
		row.OpenFolios++
		row.TotalMinor += outstanding
		row.CollectedMinor += f.PaidMinor()
		switch model.FolioAgingBucket(overdue) {
		case "1_7":
			row.Overdue1To7Minor += outstanding
		case "8_30":
			row.Overdue8To30Minor += outstanding
		case "30_plus":
			row.Overdue30PlusMinor += outstanding
		default:
			row.CurrentMinor += outstanding
		}
		if overdue > row.OldestOverdueDays {
			row.OldestOverdueDays = overdue
		}
	}
	report := &model.FolioAgingReport{TermsDays: terms, Rows: []model.FolioAgingRow{}}
	for _, row := range rows {
		report.Rows = append(report.Rows, *row)
	}
	sort.Slice(report.Rows, func(i, j int) bool {
		if report.Rows[i].OldestOverdueDays != report.Rows[j].OldestOverdueDays {
			return report.Rows[i].OldestOverdueDays > report.Rows[j].OldestOverdueDays
		}
		return report.Rows[i].PartnerName < report.Rows[j].PartnerName
	})
	return report, nil
}

// RunDunning is the hourly sweep: every issued folio owed its next
// reminder gets exactly one touch. Returns the receipt; per-folio
// outcomes live on the folios themselves.
func (svc *FolioService) RunDunning(ctx context.Context, tenantID string) (*DunningResult, error) {
	tenant, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	terms := tenant.PricingRules.FolioTerms()
	issued, err := svc.s.Store.Folios().ListIssued(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	res := &DunningResult{}
	now := svc.s.Now()
	for i := range issued {
		f := &issued[i]
		if f.NextReminderDue(now, terms) == 0 {
			continue
		}
		if svc.sendReminder(ctx, tenantID, f, terms) == model.FolioReminderSent {
			res.Sent++
		} else {
			res.Skipped++
		}
	}
	return res, nil
}

// RemindNow (operator, console) sends the next reminder outside the
// sweep cadence — guarded to one touch per 24 hours across sweep and
// manual sends, so the partner is never spammed. Only an ISSUED folio
// can be dunned; the reminder lands on the same ledger as sweep sends.
func (svc *FolioService) RemindNow(ctx context.Context, tenantID, folioID string) (*model.Folio, error) {
	f, err := svc.s.Store.Folios().Get(ctx, tenantID, folioID)
	if err != nil {
		return nil, err
	}
	if f.State != model.FolioIssued {
		return nil, httpx.ErrConflict // only an issued statement can be dunned
	}
	if n := len(f.Reminders); n > 0 && svc.s.Now().Sub(f.Reminders[n-1].CreatedAt) < 24*time.Hour {
		return nil, httpx.ErrConflict // one touch per 24h, sweep sends included
	}
	tenant, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	svc.sendReminder(ctx, tenantID, f, tenant.PricingRules.FolioTerms())
	return svc.s.Store.Folios().Get(ctx, tenantID, folioID)
}

// sendReminder delivers reminder N to the partner and records the
// outcome verbatim on the folio — "sent" only when the adapter accepted
// it, otherwise the honest skip/failure reason. Returns the recorded
// status. A store failure is audit-logged and reported, never swallowed.
//
// Phase 72: the channel is picked, not hardcoded — WhatsApp when a
// phone is on file (unchanged Phase 67 behavior), the partner's
// billing email otherwise (the address a hotel desk reads; the email
// quotes the same outstanding balance and links the printable
// statement document from Phase 71). The channel lands on the
// reminder row, so the ledger says exactly how each touch went out.
func (svc *FolioService) sendReminder(ctx context.Context, tenantID string, f *model.Folio, terms int) string {
	now := svc.s.Now()
	overdue := f.OverdueDays(now, terms)
	r := model.FolioReminder{N: len(f.Reminders) + 1, Channel: "whatsapp", CreatedAt: now}
	p, err := svc.s.Store.Partners().Get(ctx, tenantID, f.PartnerID)
	switch {
	case err != nil || (p.Phone == "" && p.Email == ""):
		r.Channel = "none"
		r.Status = "skipped:no-contact"
	case p.Phone == "":
		// Phase 72: email lane — phoneless partner with a billing email.
		r.Channel = "email"
		r.Status = svc.sendReminderEmail(ctx, tenantID, p, f, r.N, overdue, terms)
	case svc.s.waURL == "":
		r.Status = "skipped:no-adapter"
	default:
		outstanding := f.OutstandingMinor() // Phase 70: remind for the balance still owed
		body := fmt.Sprintf("Payment reminder %d — the %s statement for %d.%02d %s was issued on %s and is now %d days past due (terms %d days). Details in your portal: %s",
			r.N, f.Period, outstanding/100, abs64(outstanding)%100, f.Currency,
			f.IssuedAt.Format("2006-01-02"), overdue, terms,
			svc.s.PartnerOps.PortalURL(tenantID, f.PartnerID))
		status, detail, _ := postToAdapter(ctx, svc.s.waURL, []byte(fmt.Sprintf(
			`{"tenant_id":%q,"contact":%q,"body":%q}`, tenantID, p.Phone, body)))
		r.Status = status
		if status != model.FolioReminderSent && detail != "" {
			r.Status = status + ":" + detail
		}
	}
	f.Reminders = append(f.Reminders, r)
	if err := svc.s.Store.Folios().Update(ctx, f); err != nil {
		svc.s.Audit.Log(ctx, tenantID, "system", "channel.folio_reminder_failed", "partner", f.PartnerID,
			map[string]any{"folio_id": f.ID, "n": r.N, "error": err.Error()})
		return "failed:store"
	}
	event := "channel.folio_reminder_sent"
	if r.Status != model.FolioReminderSent {
		event = "channel.folio_reminder_skipped"
	}
	svc.s.Events.Emit(ctx, tenantID, "partner", f.PartnerID, event,
		map[string]any{"folio_id": f.ID, "n": r.N, "channel": r.Channel, "status": r.Status,
			"period": f.Period, "currency": f.Currency, "total_minor": f.TotalMinor,
			"overdue_days": overdue})
	svc.s.Audit.Log(ctx, tenantID, "channel", event, "partner", f.PartnerID,
		map[string]any{"folio_id": f.ID, "n": r.N, "status": r.Status})
	return r.Status
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
