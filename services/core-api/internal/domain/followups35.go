// Lifecycle rules (Phase 35): the second wave of proactive outreach. Same
// discipline as Phase 25 — every rule runs through the outreach engine
// (consent gates, caps, quiet hours apply), every key is idempotent, every
// send is advisory. What differs is intent coverage:
//
//	O8  quote-abandonment nudge — an instant quote left unconfirmed
//	O10 travel anniversary — the date the relationship started
//	O11 review-ask sequencing — 72h after a completed trip, if no review
//	O13 corporate monthly summary — an org client's own month, on file
//	O14 annual re-permission — consent kept alive, once a year
package domain

import (
	"context"
	"fmt"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// runQuoteNudge (O8): an instant quote sitting in AWAITING_PAYMENT past
// QuoteNudgeHours gets ONE service-lane nudge carrying its share link (A4)
// — the client can review, forward or confirm from the link itself. The
// nudge fires once ever per quote (idempotency key per quote id); an
// expired quote is not nudged (nudging a dead price is dishonest).
func (svc *FollowUpService) runQuoteNudge(ctx context.Context, tenantID string, cfg model.LifecycleConfig, runStart time.Time, res *FollowUpResult) error {
	clients, err := svc.s.Store.Clients().List(ctx, tenantID, 5000)
	if err != nil {
		return err
	}
	now := svc.s.Now()
	cutoff := now.Add(-time.Duration(cfg.QuoteNudgeHours) * time.Hour)
	for _, c := range clients {
		if c.Erased() {
			continue
		}
		reqs, err := svc.s.Store.Requests().ForClient(ctx, tenantID, c.ID, 20)
		if err != nil {
			continue
		}
		for _, r := range reqs {
			if r.State != model.StateAwaitingPayment {
				continue
			}
			q, err := svc.s.Store.Quotes().LatestForRequest(ctx, tenantID, r.ID)
			if err != nil || q.CreatedAt.After(cutoff) || now.After(q.ExpiresAt) {
				continue // no quote, too fresh, or expired
			}
			share := svc.s.Trips.QuoteTokenFor(tenantID, q.ID)
			link := svc.s.PublicBase() + "/quote/" + share
			body := "Your quote is ready and still valid — review it here: " + link +
				"\nReply here and we'll confirm it in one message."
			o, err := svc.s.Outreach.Send(ctx, tenantID, SendOutreachInput{
				ClientID: c.ID, Purpose: model.OutreachService, Body: body,
				IdempotencyKey: "quotenudge:" + q.ID,
			})
			if err == nil && o.Status == "sent" && fresh(o, runStart) {
				res.QuoteNudgeSent++
			}
		}
	}
	return nil
}

// runAnniversary (O10): the anniversary of a client's FIRST booking — the
// date the relationship started — gets a marketing-lane touch, once per
// calendar year. Only clients whose first ride was at least 300 days ago
// qualify (a 3-week-old client has no anniversary to celebrate).
func (svc *FollowUpService) runAnniversary(ctx context.Context, tenantID string, runStart time.Time, res *FollowUpResult) error {
	clients, err := svc.s.Store.Clients().List(ctx, tenantID, 5000)
	if err != nil {
		return err
	}
	stats, err := svc.s.clientBookingStats(ctx, tenantID)
	if err != nil {
		return err
	}
	now := svc.s.Now()
	year := now.Year()
	for _, c := range clients {
		if c.Erased() {
			continue
		}
		first := stats[c.ID].FirstBooking
		if first.IsZero() || now.Sub(first) < 300*24*time.Hour {
			continue
		}
		if first.Month() != now.Month() || first.Day() != now.Day() {
			continue
		}
		body := "One year of riding with us — thank you. Your concierge is ready for the next one, " +
			"whenever you are."
		o, err := svc.s.Outreach.Send(ctx, tenantID, SendOutreachInput{
			ClientID: c.ID, Purpose: model.OutreachMarketing, Body: body,
			IdempotencyKey: fmt.Sprintf("anniv:%s:%d", c.ID, year),
		})
		if err != nil {
			continue
		}
		if !fresh(o, runStart) {
			continue
		}
		if o.Status == "sent" {
			res.AnniversarySent++
		} else if o.Status == "held" {
			res.AnniversaryHeld++
		}
	}
	return nil
}

// runReviewAsk (O11): 72h after a completed trip with NO review yet, ask
// once — service lane, with the track link that carries the review form.
// Structural idempotency: one ask per assignment, and a client who already
// reviewed is never asked.
func (svc *FollowUpService) runReviewAsk(ctx context.Context, tenantID string, runStart time.Time, res *FollowUpResult) error {
	assignments, err := svc.s.Store.Dispatch().ListAssignments(ctx, tenantID)
	if err != nil {
		return err
	}
	bookings, err := svc.s.Store.Bookings().List(ctx, tenantID)
	if err != nil {
		return err
	}
	byID := make(map[string]model.Booking, len(bookings))
	for _, b := range bookings {
		byID[b.ID] = b
	}
	now := svc.s.Now()
	for _, a := range assignments {
		if a.Status != "COMPLETED_TRIP" {
			continue
		}
		completedAt := a.EndAt
		if completedAt.IsZero() {
			if b, ok := byID[a.BookingID]; ok {
				completedAt = b.CreatedAt
			}
		}
		if completedAt.IsZero() || now.Sub(completedAt) < 72*time.Hour {
			continue
		}
		// Already reviewed (or structurally impossible to review twice):
		// skip silently.
		if rv, err := svc.s.Store.Reviews().GetByAssignment(ctx, tenantID, a.ID); err == nil && rv != nil {
			continue
		}
		b, ok := byID[a.BookingID]
		if !ok {
			continue
		}
		sr, err := svc.s.Store.Requests().Get(ctx, tenantID, b.RequestID)
		if err != nil || sr.ClientID == "" {
			continue
		}
		trackURL := svc.s.ConsoleBase() + "/track?token=" +
			svc.s.Trips.ClientTokenFor(tenantID, a.ID) + "#review"
		body := "How was the trip? One tap, thirty seconds — it directly shapes how we serve " +
			"you next time: " + trackURL
		o, err := svc.s.Outreach.Send(ctx, tenantID, SendOutreachInput{
			ClientID: sr.ClientID, Purpose: model.OutreachService, Body: body,
			IdempotencyKey: "review-ask:" + a.ID,
		})
		if err == nil && o.Status == "sent" && fresh(o, runStart) {
			res.ReviewAskSent++
		}
	}
	return nil
}

// runCorpSummary (O13): on the 1st of the month, every org-linked client
// with trips last month gets their own roll-up — service lane, once per
// client per month. The summary is the client's own data handed back to
// them; totals come from the bookings ledger, never estimated.
func (svc *FollowUpService) runCorpSummary(ctx context.Context, tenantID string, runStart time.Time, res *FollowUpResult) error {
	now := svc.s.Now()
	if now.Day() != 1 {
		return nil // monthly cadence anchored on the 1st
	}
	clients, err := svc.s.Store.Clients().List(ctx, tenantID, 5000)
	if err != nil {
		return err
	}
	bookings, err := svc.s.Store.Bookings().List(ctx, tenantID)
	if err != nil {
		return err
	}
	// Last full month [start, end).
	end := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, -1, 0)
	reqClient := map[string]string{}
	trips := map[string]int{}
	spend := map[string]int64{}
	currency := map[string]string{}
	for _, b := range bookings {
		if b.CreatedAt.Before(start) || !b.CreatedAt.Before(end) {
			continue
		}
		cid, ok := reqClient[b.RequestID]
		if !ok {
			sr, err := svc.s.Store.Requests().Get(ctx, tenantID, b.RequestID)
			if err != nil {
				continue
			}
			cid = sr.ClientID
			reqClient[b.RequestID] = cid
		}
		trips[cid]++
		if q, err := svc.s.Store.Quotes().Get(ctx, tenantID, b.QuoteID); err == nil {
			spend[cid] += q.TotalMinor
			currency[cid] = q.Currency
		}
	}
	month := start.Format("2006-01")
	for _, c := range clients {
		if c.Erased() || c.OrgID == "" || trips[c.ID] == 0 {
			continue
		}
		body := fmt.Sprintf("Your %s with us: %d trips, %s %.2f billed to your corporate account. "+
			"Next month is one message away.", start.Format("January"),
			trips[c.ID], currency[c.ID], float64(spend[c.ID])/100)
		o, err := svc.s.Outreach.Send(ctx, tenantID, SendOutreachInput{
			ClientID: c.ID, Purpose: model.OutreachService, Body: body,
			IdempotencyKey: "corpsum:" + c.ID + ":" + month,
		})
		if err == nil && o.Status == "sent" && fresh(o, runStart) {
			res.CorpSummarySent++
		}
	}
	return nil
}

// runRepermission (O14): once a year, clients WITH marketing consent are
// asked to confirm it — consent kept alive is consent that means something.
// Marketing lane (a re-permission ask is itself a marketing contact under
// the honest reading); held for anyone who hit the weekly cap. Idempotent
// per client per year.
func (svc *FollowUpService) runRepermission(ctx context.Context, tenantID string, runStart time.Time, res *FollowUpResult) error {
	clients, err := svc.s.Store.Clients().List(ctx, tenantID, 5000)
	if err != nil {
		return err
	}
	now := svc.s.Now()
	year := now.Year()
	for _, c := range clients {
		if c.Erased() || c.Consent["marketing"] != true {
			continue
		}
		if now.Sub(c.CreatedAt) < 365*24*time.Hour {
			continue // first ask lands after a year of consent
		}
		body := "You currently hear from us about offers and news. Want to keep it that way? " +
			"Reply YES to stay on the list, or STOP and we'll go quiet — your bookings and " +
			"service messages are unaffected either way."
		o, err := svc.s.Outreach.Send(ctx, tenantID, SendOutreachInput{
			ClientID: c.ID, Purpose: model.OutreachMarketing, Body: body,
			IdempotencyKey: fmt.Sprintf("repermission:%s:%d", c.ID, year),
		})
		if err != nil {
			continue
		}
		if !fresh(o, runStart) {
			continue
		}
		if o.Status == "sent" {
			res.RepermissionSent++
		} else if o.Status == "held" {
			res.RepermissionHeld++
		}
	}
	return nil
}
