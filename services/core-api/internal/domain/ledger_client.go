// LedgerClient posts double-entry transfers to the Rust TigerBeetle adapter
// (Master Spec §11: TigerBeetle holds money truth; Postgres mirrors the
// reconciled projection). Postings are idempotent by key.
package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

type LedgerClient struct {
	base string
	hc   *http.Client
}

func NewLedgerClient(base string) *LedgerClient {
	return &LedgerClient{base: base, hc: &http.Client{Timeout: 5 * time.Second}}
}

type postingRequest struct {
	DebitCode      string `json:"debit_code"`
	CreditCode     string `json:"credit_code"`
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	TenantID       string `json:"tenant_id"`
	BookingID      string `json:"booking_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}

type postingResponse struct {
	TransferID string `json:"transfer_id"`
	Duplicate  bool   `json:"duplicate"`
}

// Post executes one double-entry posting. Returns the TigerBeetle transfer id.
func (c *LedgerClient) Post(ctx context.Context, lp *model.LedgerPosting) (string, error) {
	body, _ := json.Marshal(postingRequest{
		DebitCode: lp.DebitCode, CreditCode: lp.CreditCode, AmountMinor: lp.AmountMinor,
		Currency: lp.Currency, TenantID: lp.TenantID, BookingID: lp.BookingID,
		IdempotencyKey: lp.IdempotencyKey,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/postings", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("ledger adapter: %w", err)
	}
	defer resp.Body.Close()
	var pr postingResponse
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("ledger adapter: status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return "", err
	}
	return pr.TransferID, nil
}
