package domain

// Phase 42 — WhatsApp list hygiene: unreachable-number hold + engagement
// sunset. Parity with Phase 41's email hygiene: WhatsApp is the primary
// lane for the platform's markets, and Meta's quality rating punishes a
// tenant exactly the way mailbox providers punish senders — repeatedly
// messaging dead numbers and never-repliers gets the business number
// restricted. The signals:
//
//   - wa_failed / wa_delivered / wa_read: tracked on the attribution spine
//     when a receipt's wamid resolves to an outreach send (receipts.go).
//   - client_replied: tracked when a CLIENT-author message lands on a
//     conversation (conversations.go) — WhatsApp has no open pixel; a reply
//     is THE engagement proof.
//
// Unreachable hold: 3+ failed WA deliveries to a client with no confirmed
// delivery/read after the latest failure → the number is dead or blocked;
// marketing holds until evidence changes. Sunset: WaSunsetMinSends or more
// all-time marketing WA sends with zero replies ever → held; one reply
// resets it permanently. Both advisory (fail open), marketing-lane only —
// service messages are owed regardless.

import (
	"context"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// WaUnreachableMinFailures is the failed-delivery count at which a WA
// number with no subsequent confirmed delivery is treated as unreachable.
const WaUnreachableMinFailures = 3

// WaSunsetMinSends mirrors EmailSunsetMinSends for the WhatsApp lane.
const WaSunsetMinSends = 8

// waUnreachable reports whether the client's number looks dead: enough
// failures, and nothing delivered or read after the most recent one.
// Advisory: store failures fail open.
func (svc *OutreachService) waUnreachable(ctx context.Context, tenantID, clientID string) bool {
	events, err := svc.s.Store.Attribution().List(ctx, tenantID, clientID, 500)
	if err != nil {
		return false
	}
	failures := 0
	var latestFailure time.Time
	confirmedAfter := false
	for _, ev := range events {
		switch ev.Kind {
		case "wa_failed":
			failures++
			if ev.At.After(latestFailure) {
				latestFailure = ev.At
			}
		case "wa_delivered", "wa_read":
			if !latestFailure.IsZero() && ev.At.After(latestFailure) {
				confirmedAfter = true
			}
		}
	}
	return failures >= WaUnreachableMinFailures && !confirmedAfter
}

// waSunsetApplies reports whether a never-replying client has hit the WA
// sunset threshold. Advisory: store failures fail open.
func (svc *OutreachService) waSunsetApplies(ctx context.Context, tenantID, clientID string) bool {
	if clientID == "" {
		return false
	}
	sent, err := svc.s.Store.Outreach().SendsForClient(ctx, tenantID, clientID, time.Time{})
	if err != nil {
		return false
	}
	sentCount := 0
	for _, s := range sent {
		if s.Channel == model.OutreachChannelWhatsApp && s.Purpose == model.OutreachMarketing && s.Status == "sent" {
			sentCount++
		}
	}
	if sentCount < WaSunsetMinSends {
		return false
	}
	events, err := svc.s.Store.Attribution().List(ctx, tenantID, clientID, 500)
	if err != nil {
		return false
	}
	for _, ev := range events {
		if ev.Kind == "client_replied" && ev.Channel == model.OutreachChannelWhatsApp {
			return false // one reply ever resets the sunset
		}
	}
	return true
}
