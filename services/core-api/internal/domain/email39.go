// Phase 39: email channel — unsubscribe capability tokens + suppression
// flow. The "unsub/" HMAC family follows the track//quote/ discipline: the
// token is the credential (a forwarded email must still unsubscribe the
// ORIGINAL recipient), it carries (tenant, client), and other families are
// rejected — a trip tracking link can never silence a client.
package domain

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

const unsubTokenPrefix = "unsub/"

// UnsubTokenFor mints the one-click unsubscribe token for a client.
func (svc *TripService) UnsubTokenFor(tenantID, clientID string) string {
	payload := unsubTokenPrefix + tenantID + "/" + clientID
	mac := hmac.New(sha256.New, svc.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac.Sum(nil))
}

// ParseUnsubToken verifies and extracts (tenantID, clientID). Tokens from
// other families (trip, track, quote) are rejected.
func (svc *TripService) ParseUnsubToken(token string) (string, string, error) {
	_, _, payload, err := svc.parseAny(token)
	if err != nil {
		return "", "", err
	}
	rest, ok := strings.CutPrefix(payload, unsubTokenPrefix)
	if !ok {
		return "", "", httpx.ErrForbidden
	}
	return splitPayload(rest)
}

// UnsubResult is what the confirmation page renders.
type UnsubResult struct {
	TenantName string `json:"tenant_name"`
	Already    bool   `json:"already"` // consent was already off — idempotent replay
}

// UnsubscribeByToken silences marketing for the token's client. Idempotent:
// a second click reports already-unsubscribed rather than erroring. The
// suppression lands on the shared consent spine (Phase 15) so every channel
// and every future campaign honors it immediately; the attribution spine
// records the opt-out so funnel reporting reflects the cost of over-mailing.
func (svc *OutreachService) UnsubscribeByToken(ctx context.Context, token string) (*UnsubResult, error) {
	tenantID, clientID, err := svc.s.Trips.ParseUnsubToken(token)
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
	res := &UnsubResult{TenantName: t.Name}
	if cl.Consent["marketing"] != true {
		res.Already = true
		return res, nil
	}
	if _, err := svc.s.Clients.SetConsentSourced(ctx, tenantID, clientID, "marketing", false, model.ConsentSourceUnsub, "unsub/ link"); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "client", clientID, "email.unsubscribed",
		map[string]any{"channel": model.OutreachChannelEmail})
	svc.s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "email_unsubscribed",
		Channel: model.OutreachChannelEmail, ClientID: clientID,
	})
	return res, nil
}
