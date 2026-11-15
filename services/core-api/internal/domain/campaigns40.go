// Phase 40: campaign economics — the spend ledger joins the sends ledger
// and the attribution spine, so every campaign answers "what did it cost,
// what did it convert, what did it earn" (cost-per-conversion and ROAS).
// This is the "fund the next event from evidence" half of the growth loop:
// budget flows to the channels whose cost-per-converted-client is lowest,
// not the ones with the most clicks.
package domain

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type RecordSpendInput struct {
	Campaign       string `json:"campaign"`
	Channel        string `json:"channel,omitempty"` // paid_search | paid_social | ... (advisory)
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	SpentOn        string `json:"spent_on,omitempty"` // YYYY-MM-DD; default today
	Note           string `json:"note,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}

// RecordSpend books one spend entry against a campaign. Idempotent on the
// caller's key: a replay returns the original row, never a duplicate cost.
func (svc *CampaignService) RecordSpend(ctx context.Context, tenantID string, in RecordSpendInput) (*model.CampaignSpend, error) {
	campaign := strings.TrimSpace(in.Campaign)
	if campaign == "" || len(campaign) > 128 {
		return nil, httpx.ErrValidation
	}
	if in.AmountMinor <= 0 {
		return nil, fmt.Errorf("%w: amount_minor must be positive", httpx.ErrValidation)
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if err := validateCurrency(currency); err != nil {
		return nil, err
	}
	spentOn := strings.TrimSpace(in.SpentOn)
	if spentOn == "" {
		spentOn = svc.s.Now().Format("2006-01-02")
	} else if _, err := time.Parse("2006-01-02", spentOn); err != nil {
		return nil, fmt.Errorf("%w: spent_on must be YYYY-MM-DD", httpx.ErrValidation)
	}
	sp := &model.CampaignSpend{
		ID: NewID(), TenantID: tenantID, Campaign: campaign,
		Channel: strings.TrimSpace(in.Channel), AmountMinor: in.AmountMinor,
		Currency: currency, SpentOn: spentOn, Note: strings.TrimSpace(in.Note),
		IdempotencyKey: strings.TrimSpace(in.IdempotencyKey),
		CreatedAt:      svc.s.Now(),
	}
	err := svc.s.Store.CampaignSpend().Add(ctx, sp)
	if err == httpx.ErrConflict {
		// Replay: return the original entry — one cost, recorded once.
		if prev, lerr := svc.s.Store.CampaignSpend().List(ctx, tenantID, campaign, 500); lerr == nil {
			for i := range prev {
				if prev[i].IdempotencyKey == sp.IdempotencyKey {
					return &prev[i], nil
				}
			}
		}
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	svc.s.Audit.Log(ctx, tenantID, "api", "campaign.spend_recorded", "campaign_spend", sp.ID,
		map[string]any{"campaign": sp.Campaign, "channel": sp.Channel,
			"amount_minor": sp.AmountMinor, "currency": sp.Currency})
	return sp, nil
}

// SpendList returns the tenant's spend entries, newest first ("" campaign =
// all campaigns — the economics board's union source).
func (svc *CampaignService) SpendList(ctx context.Context, tenantID, campaign string, limit int) ([]model.CampaignSpend, error) {
	if limit <= 0 {
		limit = 500
	}
	return svc.s.Store.CampaignSpend().List(ctx, tenantID, strings.TrimSpace(campaign), limit)
}

// economics fills the Phase 40 money fields of a campaign funnel: total
// spend, attributed revenue (booking + referral_converted value under this
// campaign), cost per conversion, and ROAS. Advisory: a store hiccup leaves
// the fields at zero rather than failing the funnel. Mixed-currency spend
// is flagged and the ratios withheld — summing naira and dollars into one
// "currency" would be a lie, not a metric.
func (svc *CampaignService) economics(ctx context.Context, tenantID, campaign string, f *CampaignFunnel) {
	spend, err := svc.s.Store.CampaignSpend().List(ctx, tenantID, campaign, 5000)
	if err == nil {
		currencies := map[string]bool{}
		for _, sp := range spend {
			f.SpendMinor += sp.AmountMinor
			currencies[sp.Currency] = true
			f.Currency = sp.Currency
		}
		if len(currencies) > 1 {
			f.MixedCurrency = true
			f.Currency = ""
		}
	}
	rows, err := svc.s.Funnel(ctx, tenantID)
	if err == nil {
		for _, r := range rows {
			if r.Campaign != campaign {
				continue
			}
			if r.Kind == "booking" || r.Kind == "referral_converted" {
				f.AttributedValueMinor += r.ValueMinor
			}
		}
	}
	if f.MixedCurrency {
		return
	}
	if f.SpendMinor > 0 && f.Converted > 0 {
		f.CostPerConversionMinor = f.SpendMinor / int64(f.Converted)
	}
	if f.SpendMinor > 0 && f.AttributedValueMinor > 0 {
		f.ROAS = float64(f.AttributedValueMinor) / float64(f.SpendMinor)
	}
}

// Campaigns is the economics board: every campaign the tenant ran (sends
// ledger bcast keys) or paid for (spend ledger), each with its full funnel
// economics. Sorted by name for a stable console rendering.
func (svc *CampaignService) Campaigns(ctx context.Context, tenantID string) ([]*CampaignFunnel, error) {
	names := map[string]bool{}
	sends, err := svc.s.Store.Outreach().ListSends(ctx, tenantID, 20000)
	if err != nil {
		return nil, err
	}
	for _, o := range sends {
		if rest, ok := strings.CutPrefix(o.IdempotencyKey, "bcast:"); ok {
			if i := strings.IndexByte(rest, ':'); i > 0 {
				names[rest[:i]] = true
			}
		}
	}
	spend, err := svc.s.Store.CampaignSpend().List(ctx, tenantID, "", 5000)
	if err != nil {
		return nil, err
	}
	for _, sp := range spend {
		names[sp.Campaign] = true
	}
	rows := make([]*CampaignFunnel, 0, len(names))
	for name := range names {
		f, err := svc.Funnel(ctx, tenantID, name)
		if err != nil {
			return nil, err
		}
		rows = append(rows, f)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Campaign < rows[j].Campaign })
	return rows, nil
}
