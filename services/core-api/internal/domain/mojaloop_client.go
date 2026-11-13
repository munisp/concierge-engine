// Mojaloop adapter client (Master Spec section 11.3, Phase 2): the Go payout
// saga talks to the Rust mojaloop-adapter, which speaks FSPIOP (quote ->
// accept -> fulfil) to the scheme. The adapter translates; it never decides
// — TigerBeetle stays the ledger of record.
package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type MojaloopClient struct {
	base string
	hc   *http.Client
}

func NewMojaloopClient(base string) *MojaloopClient {
	return &MojaloopClient{base: base, hc: &http.Client{Timeout: 8 * time.Second}}
}

type MLQuoteRequest struct {
	PayeeRef    string `json:"payee_ref"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

type MLQuoteResponse struct {
	QuoteID      string  `json:"quote_id"`
	FXRate       float64 `json:"fx_rate"`
	ReceiveMinor int64   `json:"receive_minor"`
	FeesMinor    int64   `json:"fees_minor"`
	ExpiresAt    string  `json:"expires_at"`
}

type MLTransferRequest struct {
	QuoteID        string `json:"quote_id"`
	IdempotencyKey string `json:"idempotency_key"`
	PayoutID       string `json:"payout_id"`
	TenantID       string `json:"tenant_id"`
	CallbackURL    string `json:"callback_url"`
}

type MLTransferResponse struct {
	TransferID string `json:"transfer_id"`
	State      string  `json:"state"`
}

var ErrMojaloopUnavailable = fmt.Errorf("mojaloop adapter unavailable")

func (c *MojaloopClient) post(ctx context.Context, path string, in, out any) error {
	if c.base == "" {
		return ErrMojaloopUnavailable
	}
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("mojaloop adapter: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("mojaloop adapter: status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Quote: FSPIOP quote — locks FX rate, receive amount and scheme fees.
func (c *MojaloopClient) Quote(ctx context.Context, payeeRef string, amountMinor int64, currency string) (*MLQuoteResponse, error) {
	var out MLQuoteResponse
	if err := c.post(ctx, "/v1/quotes", MLQuoteRequest{
		PayeeRef: payeeRef, AmountMinor: amountMinor, Currency: currency,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Transfer: initiates the scheme transfer (idempotent on the key; the
// adapter dedupes and deterministic-IDs so replays are structurally safe).
func (c *MojaloopClient) Transfer(ctx context.Context, in MLTransferRequest) (*MLTransferResponse, error) {
	var out MLTransferResponse
	if err := c.post(ctx, "/v1/transfers", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
