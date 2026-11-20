package domain

// Paystack adapter (Phase 15): hosted payment pages for the markets where
// cards alone don't convert — Nigeria (card, bank transfer, USSD), Ghana and
// Kenya (mobile money). Pure stdlib: JSON API + HMAC-SHA512 webhook
// verification, no paystack-go dependency.
//
// Sandbox discipline mirrors Stripe: with CE_PAYSTACK_KEY unset the client
// is disabled and PSP routing falls through (Stripe, then the deterministic
// sandbox URL), so dev and CI never touch the network.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

var ErrPaystackDisabled = httpx.ErrValidation

type PaystackClient struct {
	key  string
	base string
	hc   *http.Client
}

func NewPaystackClient(key, base string) *PaystackClient {
	if base == "" {
		base = "https://api.paystack.co"
	}
	return &PaystackClient{
		key: key, base: strings.TrimSuffix(base, "/"),
		hc: &http.Client{Timeout: 10 * time.Second},
	}
}

// Enabled reports whether live Paystack calls are configured.
func (c *PaystackClient) Enabled() bool { return c != nil && c.key != "" }

// InitializeTransaction mints a hosted payment page for an intent. The
// intent id is the transaction reference — idempotent by construction
// (Paystack dedupes on reference), and the signed webhook routes back on it.
func (c *PaystackClient) InitializeTransaction(ctx context.Context, intent *model.PaymentIntent, email string, channels []string) (hostedURL, reference string, err error) {
	if !c.Enabled() {
		return "", "", ErrPaystackDisabled
	}
	payload := map[string]any{
		"amount":       intent.AmountMinor, // already in kobo/minor units
		"currency":     strings.ToUpper(intent.Currency),
		"reference":    intent.ID,
		"callback_url": "https://pay.concierge.engine/paystack/callback",
		"metadata": map[string]string{
			"tenant_id": intent.TenantID, "quote_id": intent.QuoteID, "intent_id": intent.ID,
		},
	}
	if email != "" {
		payload["email"] = email
	} else {
		// Paystack requires an email; use a deterministic placeholder tied to
		// the intent so receipts still route to the tenant's ops mailbox.
		payload["email"] = fmt.Sprintf("guest-%s@pay.concierge.engine", intent.ID[:8])
	}
	if len(channels) > 0 {
		payload["channels"] = channels
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/transaction/initialize", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.key)
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("paystack unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Status bool `json:"status"`
		Data   struct {
			AuthorizationURL string `json:"authorization_url"`
			Reference        string `json:"reference"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", fmt.Errorf("paystack: malformed response")
	}
	if resp.StatusCode != http.StatusOK || !out.Status || out.Data.AuthorizationURL == "" {
		return "", "", fmt.Errorf("paystack %d: %s", resp.StatusCode, out.Message)
	}
	return out.Data.AuthorizationURL, out.Data.Reference, nil
}

// InitializeFolioTransaction (Phase 74) mints a hosted payment page for a
// partner folio's outstanding balance — the Africa-corridor rail for
// statement collection (card, bank transfer, USSD, mobile money). The
// intent id is the reference (idempotent by construction) and the
// metadata carries folio_id so the signed webhook routes the capture
// onto the folio's payment ledger.
func (c *PaystackClient) InitializeFolioTransaction(ctx context.Context, intent *model.PaymentIntent, folioID, email string, channels []string) (hostedURL, reference string, err error) {
	if !c.Enabled() {
		return "", "", ErrPaystackDisabled
	}
	payload := map[string]any{
		"amount":    intent.AmountMinor, // already in kobo/minor units
		"currency":  strings.ToUpper(intent.Currency),
		"reference": intent.ID,
		"metadata": map[string]string{
			"tenant_id": intent.TenantID, "intent_id": intent.ID, "folio_id": folioID,
		},
	}
	if email != "" {
		payload["email"] = email
	} else {
		// Paystack requires an email; deterministic placeholder so receipts
		// still route to the tenant's ops mailbox.
		payload["email"] = fmt.Sprintf("partner-%s@pay.concierge.engine", intent.ID[:8])
	}
	if len(channels) > 0 {
		payload["channels"] = channels
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/transaction/initialize", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.key)
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("paystack unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Status bool `json:"status"`
		Data   struct {
			AuthorizationURL string `json:"authorization_url"`
			Reference        string `json:"reference"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", fmt.Errorf("paystack: malformed response")
	}
	if resp.StatusCode != http.StatusOK || !out.Status || out.Data.AuthorizationURL == "" {
		return "", "", fmt.Errorf("paystack %d: %s", resp.StatusCode, out.Message)
	}
	return out.Data.AuthorizationURL, out.Data.Reference, nil
}

// VerifyWebhook validates x-paystack-signature (HMAC-SHA512 of the raw body
// with the secret key) in constant time.
func (c *PaystackClient) VerifyWebhook(signature string, body []byte) bool {
	if !c.Enabled() {
		return false
	}
	mac := hmac.New(sha512.New, []byte(c.key))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(signature))
}

// Refund initiates a refund for a captured transaction (reference = intent id).
// Paystack processes refunds asynchronously; the webhook confirms.
func (c *PaystackClient) Refund(ctx context.Context, reference string, amountMinor int64) error {
	if !c.Enabled() {
		return ErrPaystackDisabled
	}
	payload := map[string]any{"transaction": reference}
	if amountMinor > 0 {
		payload["amount"] = amountMinor
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/refund", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.key)
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("paystack unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		return fmt.Errorf("paystack %d: %s", resp.StatusCode, e.Message)
	}
	return nil
}
