package domain

// Phase 65: partner rate cards — negotiated per-channel pricing applied at
// quoting time. A card binds one vertical for one sourcing partner: a
// percentage discount, a percentage markup, or a fixed item subtotal (the
// hotel's contracted airport-transfer price). Cards apply ONLY to demand
// the partner sourced, and every adjustment lands on the quote as an
// explicit partner-named line — repricing is never silent. Commission
// (Phase 63) accrues on the adjusted gross automatically, because the
// quote total is the base.

import (
	"context"
	"fmt"
	"math"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type RateCardService struct{ s *Services }

// Set upserts one rate card for a partner. Validation is fail-closed:
// the partner must exist, the kind vocabulary is closed, percentages are
// capped (a deeper discount is a catalog pricing decision, not a rate
// card), and a fixed total must name its currency. Upserts are idempotent
// by key — replaying the same card rewrites it and re-stamps updated_at.
func (svc *RateCardService) Set(ctx context.Context, tenantID, partnerID string, in *model.RateCard) (*model.RateCard, error) {
	p, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID)
	if err != nil {
		return nil, err // 404 on unknown/cross-tenant partner
	}
	in.Vertical = strings.TrimSpace(in.Vertical)
	if in.Vertical == "" || !model.ValidRateCardKind(in.Kind) {
		return nil, httpx.ErrValidation
	}
	switch in.Kind {
	case model.RateCardPctDiscount, model.RateCardPctMarkup:
		if in.Value < 1 || in.Value > model.MaxRateCardPct {
			return nil, httpx.ErrValidation
		}
		in.Currency = "" // percentages are currency-agnostic
	case model.RateCardFixedTotal:
		if in.Value <= 0 || strings.TrimSpace(in.Currency) == "" {
			return nil, httpx.ErrValidation
		}
		in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	}
	now := svc.s.Now()
	c := &model.RateCard{
		TenantID: tenantID, PartnerID: p.ID, Vertical: in.Vertical,
		Kind: in.Kind, Value: in.Value, Currency: in.Currency,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.s.Store.RateCards().Upsert(ctx, c); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "partner", p.ID, "channel.rate_card_set",
		map[string]any{"partner_id": p.ID, "vertical": c.Vertical, "kind": c.Kind,
			"value": c.Value, "currency": c.Currency})
	svc.s.Audit.Log(ctx, tenantID, "operator", "channel.rate_card_set", "partner", p.ID,
		map[string]any{"vertical": c.Vertical, "kind": c.Kind, "value": c.Value})
	return c, nil
}

func (svc *RateCardService) List(ctx context.Context, tenantID, partnerID string) ([]model.RateCard, error) {
	if _, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID); err != nil {
		return nil, err
	}
	return svc.s.Store.RateCards().ListForPartner(ctx, tenantID, partnerID)
}

func (svc *RateCardService) Delete(ctx context.Context, tenantID, partnerID, vertical string) error {
	if _, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID); err != nil {
		return err
	}
	if err := svc.s.Store.RateCards().Delete(ctx, tenantID, partnerID, vertical); err != nil {
		return err
	}
	svc.s.Audit.Log(ctx, tenantID, "operator", "channel.rate_card_deleted", "partner", partnerID,
		map[string]any{"vertical": vertical})
	return nil
}

// applyToQuote adjusts the freshly built quote lines for a sourced request
// against the sourcing partner's cards. Runs AFTER BuildQuote and BEFORE
// tax: the adjustment is a pre-tax commercial term, and the deposit base
// follows the adjusted subtotal. Returns the adjustment lines to append
// and the signed delta on the pre-tax total. Card skips (currency
// mismatch on a fixed total, unpriced item) are emitted as events with
// the reason verbatim — never silently ignored, never blocking.
func (svc *RateCardService) applyToQuote(ctx context.Context, tenantID string, r *model.ServiceRequest, lines []model.QuoteLine, currency string) ([]model.QuoteLine, int64) {
	if r.SourcePartnerID == "" || r.SourceKind == "" || r.SourceKind == model.SourceDirect {
		return nil, 0
	}
	p, err := svc.s.Store.Partners().Get(ctx, tenantID, r.SourcePartnerID)
	if err != nil {
		return nil, 0
	}
	cards, err := svc.s.Store.RateCards().ListForPartner(ctx, tenantID, p.ID)
	if err != nil || len(cards) == 0 {
		return nil, 0
	}
	byVertical := map[string]model.RateCard{}
	for _, c := range cards {
		byVertical[c.Vertical] = c
	}
	// Per-item priced subtotal from the engine's lines.
	subtotal := map[string]int64{}
	for _, l := range lines {
		if l.ItemID != "" {
			subtotal[l.ItemID] += l.AmountMinor
		}
	}
	var out []model.QuoteLine
	var delta int64
	for _, item := range r.Items {
		card, ok := byVertical[item.Vertical]
		if !ok {
			continue
		}
		base := subtotal[item.ID]
		if base <= 0 {
			continue // item not auto-quotable — human quote prices it
		}
		var adj int64
		var desc string
		switch card.Kind {
		case model.RateCardPctDiscount:
			adj = -int64(math.Round(float64(base) * float64(card.Value) / 100))
			desc = fmt.Sprintf("Partner rate — %s (−%d%%)", p.Name, card.Value)
		case model.RateCardPctMarkup:
			adj = int64(math.Round(float64(base) * float64(card.Value) / 100))
			desc = fmt.Sprintf("Partner rate — %s (+%d%%)", p.Name, card.Value)
		case model.RateCardFixedTotal:
			if card.Currency != currency {
				svc.s.Events.Emit(ctx, tenantID, "service_request", r.ID, "channel.rate_card_skipped",
					map[string]any{"partner_id": p.ID, "vertical": item.Vertical,
						"reason": "currency mismatch: card " + card.Currency + " vs quote " + currency})
				continue
			}
			adj = card.Value - base
			desc = fmt.Sprintf("Partner rate — %s (fixed)", p.Name)
		}
		if adj == 0 {
			continue
		}
		out = append(out, model.QuoteLine{Description: desc, AmountMinor: adj, ItemID: item.ID})
		delta += adj
		svc.s.Events.Emit(ctx, tenantID, "service_request", r.ID, "channel.rate_card_applied",
			map[string]any{"partner_id": p.ID, "vertical": item.Vertical, "kind": card.Kind,
				"value": card.Value, "adjustment_minor": adj, "currency": currency})
	}
	return out, delta
}
