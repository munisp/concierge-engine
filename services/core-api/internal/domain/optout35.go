// Keyword consent handling (Phase 35, O5): a client texting STOP must go
// quiet — immediately, structurally, and auditable. The intake path is the
// single funnel every inbound channel message passes through, so the
// keyword is intercepted there, before triage: an opt-out never opens a
// case, never waits for an operator, and never depends on a human reading
// the message. START reverses it. Both directions confirm on the service
// lane (a consent receipt is part of the service, not marketing).
package domain

import (
	"context"
	"strings"

	"concierge-engine/services/core-api/internal/model"
)

// optOutKeywords are the exact (normalized) texts that flip marketing
// consent off. Exact match only — "stop by the office" is not an opt-out.
var optOutKeywords = map[string]bool{
	"stop": true, "stopall": true, "stop all": true,
	"unsubscribe": true, "optout": true, "opt out": true, "opt-out": true,
}

var optInKeywords = map[string]bool{
	"start": true, "unstop": true, "subscribe": true, "opt in": true, "opt-in": true,
}

// maybeConsentKeyword intercepts consent keywords at intake. Returns true
// when the message was a keyword and has been fully handled (no case, no
// triage). Both transitions are idempotent — re-sending STOP to an
// opted-out client re-confirms silently without another outbound message.
func (svc *CaseService) maybeConsentKeyword(ctx context.Context, tenantID, clientID, text string) (bool, error) {
	norm := strings.ToLower(strings.TrimSpace(text))
	_, isOut := optOutKeywords[norm]
	_, isIn := optInKeywords[norm]
	if !isOut && !isIn {
		return false, nil
	}
	c, err := svc.s.Store.Clients().Get(ctx, tenantID, clientID)
	if err != nil {
		return false, err
	}
	if c.Consent == nil {
		c.Consent = map[string]any{}
	}
	want := !isOut
	if c.Consent["marketing"] == want {
		return true, nil // already in the requested state — no spammy re-confirm
	}
	c.Consent["marketing"] = want
	if err := svc.s.Store.Clients().Upsert(ctx, c); err != nil {
		return false, err
	}
	event := "client.opted_out"
	body := "You've been unsubscribed from marketing messages. Booking and trip updates are unaffected. Reply START any time to re-subscribe."
	if want {
		event = "client.opted_in"
		body = "Welcome back — you're subscribed to offers and news again. Reply STOP any time to go quiet."
	}
	svc.s.Events.Emit(ctx, tenantID, "client", clientID, event, map[string]any{"keyword": norm})
	// Service-lane confirmation; the daily key makes a keyword replayed by
	// a channel redelivery a no-op.
	if _, err := svc.s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachService, Body: body,
		IdempotencyKey: "consent:" + event + ":" + clientID + ":" + svc.s.Now().Format("2006-01-02"),
	}); err != nil {
		return true, nil // consent itself is recorded; the receipt is advisory
	}
	return true, nil
}
