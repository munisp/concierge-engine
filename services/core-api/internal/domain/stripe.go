// Stripe sandbox adapter (FR-P2): hosted Checkout Sessions keep the platform
// PCI SAQ-A — no PAN ever touches our servers. Pure stdlib: form-encoded API
// calls and HMAC-SHA256 webhook verification, no stripe-go dependency.
//
// Sandbox discipline: with CE_STRIPE_KEY unset the client is disabled and
// payment links fall back to the deterministic brand-hosted URL, so dev and
// CI never touch the network.
package domain

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

const stripeSignatureTolerance = 300 * time.Second

// ErrStripeDisabled marks calls into Stripe with no key configured (sandbox).
var ErrStripeDisabled = httpx.ErrValidation

type StripeClient struct {
	key           string
	base          string
	webhookSecret string
	hc            *http.Client
	now           func() time.Time
}

func NewStripeClient(key, base, webhookSecret string, now func() time.Time) *StripeClient {
	if base == "" {
		base = "https://api.stripe.com"
	}
	if now == nil {
		now = time.Now
	}
	return &StripeClient{
		key: key, base: strings.TrimSuffix(base, "/"), webhookSecret: webhookSecret,
		hc: &http.Client{Timeout: 10 * time.Second}, now: now,
	}
}

// Enabled reports whether live Stripe calls are configured.
func (c *StripeClient) Enabled() bool { return c != nil && c.key != "" }

// WebhookSecret exposes the endpoint secret for the webhook route.
func (c *StripeClient) WebhookSecret() string {
	if c == nil {
		return ""
	}
	return c.webhookSecret
}

// CreateCheckoutSession creates a hosted checkout session for an intent.
// Metadata carries tenant/quote/intent so the signed webhook can route back
// without trusting caller-supplied tenant headers. Idempotent on the intent's
// idempotency key via Stripe's Idempotency-Key header. Phase 15: locale
// (BCP-47 from the client profile) localizes the checkout page;
// customer_email pre-fills and feeds Stripe receipts.
func (c *StripeClient) CreateCheckoutSession(ctx context.Context, intent *model.PaymentIntent, locale, customerEmail string) (hostedURL, sessionID string, err error) {
	if !c.Enabled() {
		return "", "", ErrStripeDisabled
	}
	form := url.Values{
		"mode":                                   {"payment"},
		"success_url":                            {"https://pay.concierge.engine/success?session_id={CHECKOUT_SESSION_ID}"},
		"cancel_url":                             {"https://pay.concierge.engine/cancel"},
		"line_items[0][quantity]":                {"1"},
		"line_items[0][price_data][currency]":    {strings.ToLower(intent.Currency)},
		"line_items[0][price_data][unit_amount]": {strconv.FormatInt(intent.AmountMinor, 10)},
		"line_items[0][price_data][product_data][name]": {"Concierge service — " + intent.Mode},
		"metadata[tenant_id]":                           {intent.TenantID},
		"metadata[quote_id]":                            {intent.QuoteID},
		"metadata[intent_id]":                           {intent.ID},
	}
	if locale != "" {
		form.Set("locale", locale)
	}
	if customerEmail != "" {
		form.Set("customer_email", customerEmail)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/v1/checkout/sessions", strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Idempotency-Key", intent.IdempotencyKey)
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("stripe unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("stripe %d: %s", resp.StatusCode, string(body[:min(len(body), 300)]))
	}
	var session struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &session); err != nil || session.ID == "" || session.URL == "" {
		return "", "", fmt.Errorf("stripe: malformed session response")
	}
	return session.URL, session.ID, nil
}

// VerifyStripeSignature validates the Stripe-Signature header
// (t=<unix>,v1=<hex hmac-sha256 of "t.payload">) with a 5-minute tolerance.
// Constant-time compare; unsigned or stale payloads are rejected.
func VerifyStripeSignature(header string, payload []byte, secret string, now time.Time) bool {
	if secret == "" || header == "" {
		return false
	}
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			ts = kv[1]
		case "v1":
			sigs = append(sigs, kv[1])
		}
	}
	if ts == "" || len(sigs) == 0 {
		return false
	}
	tsUnix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if now.Unix()-tsUnix > int64(stripeSignatureTolerance/time.Second) ||
		tsUnix-now.Unix() > int64(stripeSignatureTolerance/time.Second) {
		return false // stale or future-dated: replay guard
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(payload)
	expected := mac.Sum(nil)
	for _, sig := range sigs {
		got, err := hex.DecodeString(sig)
		if err == nil && hmac.Equal(got, expected) {
			return true
		}
	}
	return false
}

// StripeEvent is the subset of a Stripe event the platform routes on.
type StripeEvent struct {
	Type      string
	SessionID string
	TenantID  string
	IntentID  string
	QuoteID   string
}

// ParseStripeEvent extracts routing fields from a checkout.session event.
// Metadata comes from what CreateCheckoutSession attached — never from the
// client's request headers.
func ParseStripeEvent(payload []byte) (*StripeEvent, error) {
	var ev struct {
		Type string `json:"type"`
		Data struct {
			Object struct {
				ID       string `json:"id"`
				Metadata struct {
					TenantID string `json:"tenant_id"`
					IntentID string `json:"intent_id"`
					QuoteID  string `json:"quote_id"`
				} `json:"metadata"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil, httpx.ErrValidation
	}
	out := &StripeEvent{
		Type: ev.Type, SessionID: ev.Data.Object.ID,
		TenantID: ev.Data.Object.Metadata.TenantID,
		IntentID: ev.Data.Object.Metadata.IntentID,
		QuoteID:  ev.Data.Object.Metadata.QuoteID,
	}
	if out.Type == "" {
		return nil, httpx.ErrValidation
	}
	return out, nil
}

// Refund initiates a full refund for a captured Checkout Session (Phase 15).
// The session id is expanded to its payment_intent, then refunded; Stripe
// confirms asynchronously via the charge.refunded webhook.
func (c *StripeClient) Refund(ctx context.Context, sessionID string) error {
	if !c.Enabled() {
		return ErrStripeDisabled
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.base+"/v1/checkout/sessions/"+sessionID, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("stripe unreachable: %w", err)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("stripe %d: %s", resp.StatusCode, string(raw[:min(len(raw), 300)]))
	}
	var session struct {
		PaymentIntent string `json:"payment_intent"`
	}
	if err := json.Unmarshal(raw, &session); err != nil || session.PaymentIntent == "" {
		return fmt.Errorf("stripe: session carries no payment_intent (not captured?)")
	}
	form := url.Values{"payment_intent": {session.PaymentIntent}}
	req, err = http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/v1/refunds", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Idempotency-Key", "refund:"+sessionID)
	resp, err = c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("stripe unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("stripe %d: %s", resp.StatusCode, string(raw[:min(len(raw), 300)]))
	}
	return nil
}
