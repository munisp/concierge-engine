package model

import "time"

// Phase 40: campaign economics — the spend side of the funnel.

// CampaignSpend is one spend entry against a campaign: an ad buy, a list
// purchase, an event sponsorship. amount_minor is minor units (kobo,
// cents); currency is ISO-4217 alpha-3. spent_on is the day the money was
// spent (YYYY-MM-DD), which may differ from created_at (backfill).
// Idempotent by (tenant_id, idempotency_key) — a retried record replays to
// the original row.
type CampaignSpend struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	Campaign       string    `json:"campaign"`
	Channel        string    `json:"channel,omitempty"` // paid_search | paid_social | ... (advisory)
	AmountMinor    int64     `json:"amount_minor"`
	Currency       string    `json:"currency"`
	SpentOn        string    `json:"spent_on"` // YYYY-MM-DD
	Note           string    `json:"note,omitempty"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}
