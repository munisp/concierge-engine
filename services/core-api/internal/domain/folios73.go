package domain

// Phase 73: credit notes & refunds on folios. Statements get corrected —
// a guest cancelled after the period closed, a line was mispriced, the
// desk is owed goodwill. The correction is a credit row on the folio's
// own ledger, never an edit of the frozen lines:
//
//   - ISSUED folio → credit note (kind "credit"): the partner owes less.
//     Credited/paid/outstanding are all derived, so aging, dunning, the
//     statement page and the next delivery all recompute from the
//     adjusted total automatically. A credit that brings the balance to
//     zero settles the folio — a fully-credited statement is collected.
//     Over-credit is a 409: you cannot credit money the partner already
//     paid on an open statement.
//   - SETTLED folio → refund (kind "refund"): the money was collected,
//     so the credit carries money truth flowing BACK — reversal postings
//     TENANT_REVENUE → PARTNER_CLEARING (net) and PLATFORM_FEES →
//     PARTNER_CLEARING (fee), proportional to the amount and keyed
//     folio:refundnet:{id}:{creditID} so a retry can never double-post.
//     Cumulative refunds can never exceed what was actually paid.
//
// OPEN folios take no credits (409): the total is still accruing —
// dispute the line, not a moving statement.

import (
	"context"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// RecordCredit (operator, console) records a credit note against an
// ISSUED folio or a refund against a SETTLED one. Amount must be
// positive and within the correctable balance (outstanding for a credit
// note, paid-so-far for a refund) — excess is a 409, never silently
// absorbed.
func (svc *FolioService) RecordCredit(ctx context.Context, tenantID, folioID string, amountMinor int64, reason string) (*model.Folio, error) {
	if amountMinor <= 0 {
		return nil, httpx.ErrValidation
	}
	f, err := svc.s.Store.Folios().Get(ctx, tenantID, folioID)
	if err != nil {
		return nil, err
	}
	tenant, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	credit := model.FolioCredit{
		ID: NewID(), AmountMinor: amountMinor,
		Reason:    strings.TrimSpace(reason),
		CreatedAt: svc.s.Now(), RecordedBy: "operator",
	}
	switch f.State {
	case model.FolioIssued:
		if amountMinor > f.OutstandingMinor() {
			return nil, httpx.ErrConflict // cannot credit money already paid — over-credit
		}
		credit.Kind = model.FolioCreditNote
	case model.FolioSettled:
		if f.CreditedMinor()+amountMinor > f.PaidMinor() {
			return nil, httpx.ErrConflict // refunds can never exceed what was collected
		}
		credit.Kind = model.FolioCreditRefund
		// Money truth flows back: the same net/fee split that was posted
		// on collection is reversed proportionally, keyed per credit so a
		// retried request posts once.
		fee := PlatformFeeMinor(tenant.PricingRules, amountMinor)
		net := amountMinor - fee
		if err := svc.s.Payments.post(ctx, tenantID, "",
			"TENANT_REVENUE", "PARTNER_CLEARING", net, f.Currency, "folio:refundnet:"+f.ID+":"+credit.ID); err != nil {
			return nil, err
		}
		if err := svc.s.Payments.post(ctx, tenantID, "",
			"PLATFORM_FEES", "PARTNER_CLEARING", fee, f.Currency, "folio:refundfee:"+f.ID+":"+credit.ID); err != nil {
			return nil, err
		}
	default: // OPEN — still accruing, nothing frozen to correct
		return nil, httpx.ErrConflict
	}
	f.Credits = append(f.Credits, credit)
	settled := false
	if f.State == model.FolioIssued && f.OutstandingMinor() == 0 {
		f.State = model.FolioSettled
		f.SettledAt = svc.s.Now()
		settled = true
	}
	if err := svc.s.Store.Folios().Update(ctx, f); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "partner", f.PartnerID, "channel.folio_credit_recorded",
		map[string]any{"folio_id": f.ID, "credit_id": credit.ID, "kind": credit.Kind,
			"amount_minor": amountMinor, "reason": credit.Reason, "currency": f.Currency,
			"outstanding_minor": f.OutstandingMinor(), "settled": settled})
	svc.s.Audit.Log(ctx, tenantID, "operator", "channel.folio_credit_recorded", "partner", f.PartnerID,
		map[string]any{"folio_id": f.ID, "credit_id": credit.ID, "kind": credit.Kind, "amount_minor": amountMinor})
	if settled {
		svc.emitSettled(ctx, tenantID, tenant.PricingRules, f)
	}
	return f, nil
}
