// Lifecycle follow-ups (Phase 25): how a tenant keeps up with a client all
// year without manual remembering. Two rules, both running through the
// Phase 14 outreach engine — so consent gates, frequency caps, idempotency
// and delivery receipts apply unchanged:
//
//	post_trip — PostTripHours (default 24) after a trip completes, the
//	  client gets a service-purpose thank-you that asks for one preference.
//	  Service lane: exempt from the marketing cap, no marketing consent
//	  required (it is part of the service they bought).
//	win_back — WinBackDays (default 90) without a booking marks the client
//	  for a marketing win-back. The engine's consent gate decides: clients
//	  who never opted in are recorded HELD, never contacted.
//
// Idempotency keys make re-runs safe: post_trip keys per assignment (fires
// once ever), win_back keys per client per ISO week (one attempt per week,
// retryable next week). The sweeper runs hourly per tenant; operators can
// also trigger a run via POST /v1/followups/run.
package domain

import (
	"context"
	"fmt"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

type FollowUpService struct {
	s *Services
}

type FollowUpResult struct {
	PostTripSent   int `json:"post_trip_sent"`
	WinBackSent    int `json:"win_back_sent"`
	WinBackHeld    int `json:"win_back_held"` // consent/cap gate — counted, never forced
	ClientsScanned int `json:"clients_scanned"`
}

func (svc *FollowUpService) lifecycleConfig(ctx context.Context, tenantID string) (model.LifecycleConfig, error) {
	t, err := svc.s.Tenants.Get(ctx, tenantID)
	if err != nil {
		return model.LifecycleConfig{}, err
	}
	cfg := t.LifecycleConfig
	if cfg.PostTripHours <= 0 {
		cfg.PostTripHours = 24
	}
	if cfg.WinBackDays <= 0 {
		cfg.WinBackDays = 90
	}
	return cfg, nil
}

// Run evaluates both rules for one tenant. Advisory per send: a failed
// delivery is recorded by the outreach engine and never aborts the sweep.
// Counters reflect THIS run: an idempotency replay returns the original
// (older) record and is not recounted, so hourly sweeps stay quiet once
// the work is done.
func (svc *FollowUpService) Run(ctx context.Context, tenantID string) (*FollowUpResult, error) {
	cfg, err := svc.lifecycleConfig(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	res := &FollowUpResult{}
	if cfg.Disabled {
		return res, nil
	}
	runStart := svc.s.Now()
	if err := svc.runPostTrip(ctx, tenantID, cfg, runStart, res); err != nil {
		return res, err
	}
	if err := svc.runWinBack(ctx, tenantID, cfg, runStart, res); err != nil {
		return res, err
	}
	return res, nil
}

// fresh reports whether a send record was created by this run (as opposed
// to an idempotency replay returning the original record).
func fresh(o *model.OutreachSend, runStart time.Time) bool {
	return !o.CreatedAt.Before(runStart)
}

func (svc *FollowUpService) runPostTrip(ctx context.Context, tenantID string, cfg model.LifecycleConfig, runStart time.Time, res *FollowUpResult) error {
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
		b, ok := byID[a.BookingID]
		if !ok {
			continue
		}
		// Completion timestamp: the service window's end is the honest proxy
		// (assignments carry no completed_at). Zero-window trips anchor on
		// the booking's creation instead of firing immediately.
		completedAt := a.EndAt
		if completedAt.IsZero() {
			completedAt = b.CreatedAt
		}
		if now.Sub(completedAt) < time.Duration(cfg.PostTripHours)*time.Hour {
			continue // too soon
		}
		sr, err := svc.s.Store.Requests().Get(ctx, tenantID, b.RequestID)
		if err != nil || sr.ClientID == "" {
			continue
		}
		body := "Thank you for riding with us — we hope the trip was seamless. " +
			"Reply with one thing you'd like us to remember for next time " +
			"(a preferred car, a usual pickup point, a driver's name) and it " +
			"will be on file before you even ask."
		o, err := svc.s.Outreach.Send(ctx, tenantID, SendOutreachInput{
			ClientID: sr.ClientID, Purpose: model.OutreachService, Body: body,
			IdempotencyKey: "followup:post_trip:" + a.ID,
		})
		if err == nil && o.Status == "sent" && fresh(o, runStart) {
			res.PostTripSent++
		}
	}
	return nil
}

func (svc *FollowUpService) runWinBack(ctx context.Context, tenantID string, cfg model.LifecycleConfig, runStart time.Time, res *FollowUpResult) error {
	clients, err := svc.s.Store.Clients().List(ctx, tenantID, 5000)
	if err != nil {
		return err
	}
	res.ClientsScanned = len(clients)
	bookings, err := svc.s.Store.Bookings().List(ctx, tenantID)
	if err != nil {
		return err
	}
	// Last booking per client, via the request's client anchor.
	reqClient := map[string]string{}
	lastBooking := map[string]time.Time{}
	for _, b := range bookings {
		cid, ok := reqClient[b.RequestID]
		if !ok {
			sr, err := svc.s.Store.Requests().Get(ctx, tenantID, b.RequestID)
			if err != nil {
				continue
			}
			cid = sr.ClientID
			reqClient[b.RequestID] = cid
		}
		if b.CreatedAt.After(lastBooking[cid]) {
			lastBooking[cid] = b.CreatedAt
		}
	}
	now := svc.s.Now()
	year, week := now.ISOWeek()
	cutoff := now.Add(-time.Duration(cfg.WinBackDays) * 24 * time.Hour)
	for _, c := range clients {
		last, ok := lastBooking[c.ID]
		if !ok || last.After(cutoff) {
			continue // never booked, or active recently
		}
		body := "It's been a while — your concierge is still here whenever you need us. " +
			"One message is all it takes to have your next trip arranged end to end."
		o, err := svc.s.Outreach.Send(ctx, tenantID, SendOutreachInput{
			ClientID: c.ID, Purpose: model.OutreachMarketing, Body: body,
			IdempotencyKey: fmt.Sprintf("followup:winback:%s:%d-W%02d", c.ID, year, week),
		})
		if err != nil {
			continue
		}
		if !fresh(o, runStart) {
			continue // idempotency replay — already counted in an earlier run
		}
		if o.Status == "sent" {
			res.WinBackSent++
		} else if o.Status == "held" {
			res.WinBackHeld++ // consent/cap gate said no — counted, never forced
		}
		// "failed" (adapter unreachable) is recorded on the send itself and
		// surfaced on the growth board; it is neither a success nor a hold.
	}
	return nil
}
