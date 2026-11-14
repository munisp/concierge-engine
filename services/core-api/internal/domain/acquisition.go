package domain

// Phase 14: acquisition attribution strengthening.
//
// First-touch inheritance: downstream events (request_created, booking) copy
// channel/campaign from the client's EARLIEST attributed touch, so the funnel
// answers "which channel/campaign produced revenue" instead of only "which
// produced conversations".
//
// Client referral codes: every client has a deterministic code
// (model.ClientReferralCode). A referee arriving via a capture link carries
// campaign "ref:<code>"; their first booking fires referral.converted and a
// referral_converted attribution row carrying the booking value. Ledger
// rewards are a documented deferral (docs/phase14.md) — the conversion is
// visible in the funnel and event stream today.

import (
	"context"
	"strings"

	"concierge-engine/services/core-api/internal/model"
	"concierge-engine/services/core-api/internal/store"
)

// FirstTouch returns the client's earliest attributed channel/campaign:
// the oldest event carrying a campaign wins; otherwise the oldest carrying a
// channel. Empty strings when the client has no attributed touch.
func (svc *Services) FirstTouch(ctx context.Context, tenantID, clientID string) (channel, campaign string) {
	if clientID == "" {
		return "", ""
	}
	events, err := svc.Store.Attribution().List(ctx, tenantID, clientID, 50)
	if err != nil {
		return "", "" // attribution is advisory — never block the flow
	}
	for _, e := range events {
		if e.Campaign != "" {
			return e.Channel, e.Campaign
		}
		if channel == "" && e.Channel != "" {
			channel = e.Channel
		}
	}
	return channel, ""
}

// TrackInherited records an attribution event for a client, inheriting
// channel/campaign from first touch when the event doesn't carry its own.
func (svc *Services) TrackInherited(ctx context.Context, e *model.AttributionEvent) {
	if e.Channel == "" && e.Campaign == "" && e.ClientID != "" {
		e.Channel, e.Campaign = svc.FirstTouch(ctx, e.TenantID, e.ClientID)
	}
	svc.Events.Track(ctx, e)
}

// ReferralConversion checks whether this booking is a referee's FIRST booking
// under a "ref:<code>" campaign; if so it emits referral.converted and tracks
// the conversion value. Advisory: never blocks confirmation.
func (svc *Services) ReferralConversion(ctx context.Context, tenantID, clientID string, b *model.Booking, valueMinor int64) {
	if clientID == "" {
		return
	}
	events, err := svc.Store.Attribution().List(ctx, tenantID, clientID, 200)
	if err != nil {
		return
	}
	campaign := ""
	priorBooking := false
	for _, e := range events {
		switch {
		case e.Kind == "booking":
			priorBooking = true
		case campaign == "" && strings.HasPrefix(e.Campaign, "ref:"):
			campaign = e.Campaign
		}
	}
	if priorBooking || campaign == "" {
		return
	}
	code := strings.TrimPrefix(campaign, "ref:")
	payload := map[string]any{
		"referee_client_id": clientID, "booking_id": b.ID,
		"value_minor": valueMinor, "code": code,
	}
	if referrer, err := svc.Store.Clients().FindByReferralCode(ctx, tenantID, code); err == nil {
		payload["referrer_client_id"] = referrer.ID
	}
	svc.Events.Emit(ctx, tenantID, "referral", b.ID, "referral.converted", payload)
	svc.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "referral_converted", Channel: "referral",
		Campaign: campaign, ClientID: clientID, RequestID: b.RequestID,
		ValueMinor: valueMinor,
	})
}

// Funnel aggregates the tenant's acquisition funnel for the growth board.
func (svc *Services) Funnel(ctx context.Context, tenantID string) ([]store.FunnelRow, error) {
	return svc.Store.Attribution().Funnel(ctx, tenantID)
}
