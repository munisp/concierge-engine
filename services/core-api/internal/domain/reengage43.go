package domain

// Phase 43 — post-sunset re-engagement lane. The Phase 41/42 hygiene rules
// hold never-engaged clients out of the marketing lane; without an escape
// hatch a merely-quiet client is unreachable forever unless they organically
// reply. Two mechanisms:
//
//  1. Win-back touch: a sunset-held client whose marketing consent is still
//     on may receive ONE marketing touch per ReengagementCooldownDays on the
//     held channel. Eligibility derives from the sends ledger itself (the
//     granted touch carries a Detail marker; the cooldown counts marker sends
//     that actually went out or are in flight) — no separate counter that
//     could drift, and a failed dispatch never burns the window. The hold
//     reasons stay honest: unreachable numbers and suppressed addresses are
//     NOT exempt — a dead endpoint stays dead.
//  2. reopt/ HMAC token family (mirrors unsub/): a one-tap re-subscribe link
//     for clients whose consent is OFF. The only place Phase 43 embeds it is
//     a one-line footer on service-lane emails (transactional messages the
//     client is owed anyway) — the marketing lane itself stays closed until
//     consent comes back.
//
// Both flows land on the shared consent + attribution spines, so every
// channel and report sees them immediately.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// ReengagementCooldownDays is the win-back cadence: one marketing touch per
// held channel per window. Industry-standard win-back spacing — frequent
// enough to catch a client whose silence ended, rare enough to cost the
// sender's reputation nothing.
const ReengagementCooldownDays = 90

// reengagementMarker prefixes the Detail of a send granted through the
// sunset exemption. The marker IS the cooldown record: eligibility scans the
// sends ledger for it, and the audit trail shows exactly which send was the
// touch. Recorded on the send (not the attribution spine) so a scheduled
// touch already holds the window before it delivers.
const reengagementMarker = "re-engagement touch"

const reoptTokenPrefix = "reopt/"

// ReoptTokenFor mints the one-tap re-subscribe token for a client.
func (svc *TripService) ReoptTokenFor(tenantID, clientID string) string {
	payload := reoptTokenPrefix + tenantID + "/" + clientID
	mac := hmac.New(sha256.New, svc.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac.Sum(nil))
}

// ParseReoptToken verifies and extracts (tenantID, clientID). Tokens from
// other families (trip, track, quote, unsub) are rejected — an unsubscribe
// link can never re-subscribe anyone.
func (svc *TripService) ParseReoptToken(token string) (string, string, error) {
	_, _, payload, err := svc.parseAny(token)
	if err != nil {
		return "", "", err
	}
	rest, ok := strings.CutPrefix(payload, reoptTokenPrefix)
	if !ok {
		return "", "", httpx.ErrForbidden
	}
	return splitPayload(rest)
}

// ReoptResult is what the confirmation page renders.
type ReoptResult struct {
	TenantName string `json:"tenant_name"`
	Already    bool   `json:"already"` // consent was already on — idempotent replay
}

// ReoptInByToken restores marketing consent for the token's client.
// Idempotent: a second click reports already-subscribed rather than
// erroring. The consent lands on the shared spine (Phase 15) so every
// channel honors it immediately; the attribution spine records the opt-in so
// funnel reporting can prove win-back value.
func (svc *OutreachService) ReoptInByToken(ctx context.Context, token string) (*ReoptResult, error) {
	tenantID, clientID, err := svc.s.Trips.ParseReoptToken(token)
	if err != nil {
		return nil, err
	}
	cl, err := svc.s.Store.Clients().Get(ctx, tenantID, clientID)
	if err != nil {
		return nil, err
	}
	t, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	res := &ReoptResult{TenantName: t.Name}
	if cl.Consent["marketing"] == true {
		res.Already = true
		return res, nil
	}
	if _, err := svc.s.Clients.SetConsent(ctx, tenantID, clientID, "marketing", true); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "client", clientID, "client.reopted",
		map[string]any{"source": "reopt_link"})
	svc.s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "reengagement_optin", ClientID: clientID,
	})
	return res, nil
}

// reengagementEligible reports whether a sunset-held client may receive a
// win-back touch on this channel now: no marker send (status sent, or
// scheduled and still in flight) on this channel inside the cooldown window.
// excludeSendID lets the due-send sweep deliver the granted touch itself —
// its own marker must not hold it. Fail CLOSED on store error: the sunset
// hold is the safe default when the cooldown cannot be verified.
func (svc *OutreachService) reengagementEligible(ctx context.Context, tenantID, clientID, channel, excludeSendID string) (bool, time.Time) {
	since := svc.s.Now().Add(-ReengagementCooldownDays * 24 * time.Hour)
	sends, err := svc.s.Store.Outreach().SendsForClient(ctx, tenantID, clientID, since)
	if err != nil {
		return false, time.Time{}
	}
	var latest time.Time
	for _, o := range sends {
		if o.ID == excludeSendID || o.Channel != channel {
			continue
		}
		if strings.HasPrefix(o.Detail, reengagementMarker) &&
			(o.Status == "sent" || o.Status == "scheduled") {
			if o.CreatedAt.After(latest) {
				latest = o.CreatedAt
			}
		}
	}
	if latest.IsZero() {
		return true, time.Time{}
	}
	return false, latest.Add(ReengagementCooldownDays * 24 * time.Hour)
}

// withReoptFooter appends the re-subscribe line to a service-lane email for
// a client whose marketing consent is off. Neutral wording by design: an
// opt-in-jurisdiction client never "opted out" — they simply never joined.
// The marketing lane stays closed; the link is the only door back, and the
// client opens it themselves.
func withReoptFooter(body, reoptURL string) string {
	return strings.TrimRight(body, "\n") + "\n\n—\nYou're not on the marketing list. Want offers and updates? Subscribe here: " + reoptURL + "\n"
}
