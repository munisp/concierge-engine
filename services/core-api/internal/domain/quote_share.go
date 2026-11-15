// Shareable quote links (Phase 35, A4): a quote becomes a forwardable
// offer. The operator (or the client, via the console) mints an HMAC
// capability token in the "quote/" family; the public page at
// /quote/{token} renders the priced offer — lines, total, deposit, expiry —
// with a one-tap CTA into the capture funnel stamped campaign=quote_share.
//
// The token is the credential (same model as trip/track links): anyone
// holding the link can view the quote. That is the point — a quote the
// client cannot forward to the person paying cannot convert.
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

const quoteTokenPrefix = "quote/"

// QuoteTokenFor mints the share token for a quote.
func (svc *TripService) QuoteTokenFor(tenantID, quoteID string) string {
	payload := quoteTokenPrefix + tenantID + "/" + quoteID
	mac := hmac.New(sha256.New, svc.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac.Sum(nil))
}

// ParseQuoteToken verifies and extracts (tenantID, quoteID). Tokens from
// other families (trip, track) are rejected — a tracking link must never
// expose pricing.
func (svc *TripService) ParseQuoteToken(token string) (string, string, error) {
	_, _, payload, err := svc.parseAny(token)
	if err != nil {
		return "", "", err
	}
	rest, ok := strings.CutPrefix(payload, quoteTokenPrefix)
	if !ok {
		return "", "", httpx.ErrForbidden
	}
	return splitPayload(rest)
}

// QuoteShareView is the public page's data: the quote itself plus the
// request's itinerary summary. No client PII — the page is forwardable.
type QuoteShareView struct {
	TenantName string                `json:"tenant_name"`
	Quote      *model.Quote          `json:"quote"`
	Request    *model.ServiceRequest `json:"request"`
	Expired    bool                  `json:"expired"`
	// CTA into the capture funnel with quote_share attribution.
	CaptureURL string `json:"capture_url"`
	// WhatsApp deep link when the tenant configured a number.
	WhatsApp string `json:"whatsapp,omitempty"`
}

// Share mints the token and returns the public URL.
func (svc *TripService) Share(ctx context.Context, tenantID, quoteID string) (*model.QuoteShare, error) {
	if _, err := svc.s.Store.Quotes().Get(ctx, tenantID, quoteID); err != nil {
		return nil, err
	}
	tok := svc.QuoteTokenFor(tenantID, quoteID)
	return &model.QuoteShare{
		Token: tok, QuoteID: quoteID,
		URL:       svc.s.PublicBase() + "/quote/" + tok,
		CreatedAt: svc.s.Now(),
	}, nil
}

// QuoteView resolves a share token into the public page's data. An expired
// quote still renders — marked expired, CTA intact (the conversation can
// re-quote; a dead page converts nobody).
func (svc *TripService) QuoteView(ctx context.Context, token, captureURL string) (*QuoteShareView, error) {
	tenantID, quoteID, err := svc.ParseQuoteToken(token)
	if err != nil {
		return nil, err
	}
	q, err := svc.s.Store.Quotes().Get(ctx, tenantID, quoteID)
	if err != nil {
		return nil, err
	}
	t, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	v := &QuoteShareView{
		TenantName: t.Name, Quote: q,
		Expired:    svc.s.Now().After(q.ExpiresAt),
		CaptureURL: captureURL,
	}
	if sr, err := svc.s.Store.Requests().Get(ctx, tenantID, q.RequestID); err == nil {
		v.Request = sr
	}
	if n, _ := t.Brand["whatsapp_number"].(string); n != "" {
		v.WhatsApp = "https://wa.me/" + strings.TrimPrefix(strings.TrimSpace(n), "+")
	}
	return v, nil
}
