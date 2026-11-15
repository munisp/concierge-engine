package server_test

// Phase 40 server tests: the spend-ledger endpoints, the campaigns
// economics board, and funnel economics over HTTP.

import (
	"net/http"
	"testing"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
)

func TestCampaignSpendEndpoints40(t *testing.T) {
	env := newEnv(t)

	// Validation: zero amount → 422.
	env.do("POST", "/v1/outreach/campaigns/lagos-launch/spend", env.tenantID, map[string]any{
		"amount_minor": 0, "currency": "NGN",
	}, http.StatusUnprocessableEntity, nil)

	// Happy path. The path campaign is authoritative — a mismatched body
	// campaign is overridden, not trusted.
	var sp model.CampaignSpend
	env.do("POST", "/v1/outreach/campaigns/lagos-launch/spend", env.tenantID, map[string]any{
		"campaign": "forged", "channel": "paid_search", "amount_minor": 100000,
		"currency": "ngn", "note": "Google Ads week 1", "idempotency_key": "sp-1",
	}, http.StatusCreated, &sp)
	if sp.Campaign != "lagos-launch" || sp.Currency != "NGN" || sp.AmountMinor != 100000 {
		t.Fatalf("spend row: %+v", sp)
	}

	// Idempotent replay returns the original row.
	var replay model.CampaignSpend
	env.do("POST", "/v1/outreach/campaigns/lagos-launch/spend", env.tenantID, map[string]any{
		"amount_minor": 100000, "currency": "NGN", "idempotency_key": "sp-1",
	}, http.StatusCreated, &replay)
	if replay.ID != sp.ID {
		t.Fatalf("replay must return the original: %+v vs %+v", replay, sp)
	}

	// The campaign's spend ledger.
	var list struct {
		Spend []model.CampaignSpend `json:"spend"`
	}
	env.do("GET", "/v1/outreach/campaigns/lagos-launch/spend", env.tenantID, nil, http.StatusOK, &list)
	if len(list.Spend) != 1 || list.Spend[0].IdempotencyKey != "sp-1" {
		t.Fatalf("spend list: %+v", list)
	}

	// The economics board carries the campaign even before any send.
	var board struct {
		Campaigns []domain.CampaignFunnel `json:"campaigns"`
	}
	env.do("GET", "/v1/outreach/campaigns", env.tenantID, nil, http.StatusOK, &board)
	if len(board.Campaigns) != 1 || board.Campaigns[0].Campaign != "lagos-launch" ||
		board.Campaigns[0].SpendMinor != 100000 {
		t.Fatalf("board: %+v", board)
	}
}

func TestCampaignFunnelEconomicsEndpoint40(t *testing.T) {
	env := newEnv(t)

	env.do("POST", "/v1/outreach/campaigns/lagos-launch/spend", env.tenantID, map[string]any{
		"amount_minor": 100000, "currency": "NGN",
	}, http.StatusCreated, nil)

	// Two attributed bookings under the campaign via the public spine.
	for _, v := range []int64{250000, 150000} {
		env.do("POST", "/v1/attribution", env.tenantID, map[string]any{
			"kind": "booking", "channel": "paid_search",
			"campaign": "lagos-launch", "value_minor": v,
		}, http.StatusAccepted, nil)
	}

	var f domain.CampaignFunnel
	env.do("GET", "/v1/outreach/campaigns/lagos-launch/funnel", env.tenantID, nil, http.StatusOK, &f)
	if f.Converted != 2 || f.SpendMinor != 100000 || f.AttributedValueMinor != 400000 {
		t.Fatalf("funnel: %+v", f)
	}
	if f.CostPerConversionMinor != 50000 || f.ROAS != 4.0 || f.Currency != "NGN" {
		t.Fatalf("economics: %+v", f)
	}
}
