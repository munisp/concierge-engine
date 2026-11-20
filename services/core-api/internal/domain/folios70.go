package domain

// Phase 70: partial payments against issued folios. Phase 66 made
// statements; Phase 67 made them dunned; but collection was
// all-or-nothing (ISSUED → SETTLED in one shot), which is not how a
// hotel desk pays — it pays what it has collected, when it has
// collected it. Now the operator records each amount received:
//
//   - Every payment is a verbatim FolioPayment row on the folio; paid
//     and outstanding are DERIVED from that ledger (never stored
//     twice), and an overpayment is rejected — the recorded money can
//     never exceed the statement.
//   - Each payment posts the same net/fee split as a card capture,
//     proportional to the amount received and keyed
//     folio:net:{id}:{paymentID} — a retried request posts once, and
//     every payment carries its own money truth.
//   - When outstanding reaches zero the folio settles itself
//     (ISSUED → SETTLED, same terminal state as Settle). Settle now
//     records the outstanding remainder as the final payment, so both
//     paths converge on a fully-accounted ledger: SETTLED always means
//     paid == total.
//   - Aging and dunning chase the outstanding balance, not the face
//     value — a half-paid statement is half as overdue.

import (
	"context"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// RecordPayment (operator, console) records an amount received against
// an ISSUED folio. ISSUED-only: an OPEN folio is still accruing (its
// total is not frozen — nothing to pay against) and a SETTLED folio is
// terminal. The amount must be positive and must not exceed the
// outstanding balance — overpayment is a 409, never silently absorbed.
func (svc *FolioService) RecordPayment(ctx context.Context, tenantID, folioID string, amountMinor int64, method, reference string) (*model.Folio, error) {
	if amountMinor <= 0 {
		return nil, httpx.ErrValidation
	}
	method = strings.ToLower(strings.TrimSpace(method))
	if method == "" {
		method = "other"
	}
	f, err := svc.s.Store.Folios().Get(ctx, tenantID, folioID)
	if err != nil {
		return nil, err
	}
	if f.State != model.FolioIssued {
		return nil, httpx.ErrConflict // only an issued statement can take payments
	}
	if amountMinor > f.OutstandingMinor() {
		return nil, httpx.ErrConflict // recorded money never exceeds the statement
	}
	tenant, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	pay := model.FolioPayment{
		ID: NewID(), AmountMinor: amountMinor, Method: method,
		Reference:  strings.TrimSpace(reference),
		ReceivedAt: svc.s.Now(), RecordedBy: "operator",
	}
	// Money truth, proportional to this payment: partner clearing clears
	// into tenant revenue net of the platform fee — the same split as a
	// card capture, keyed per payment so a retry can never double-post.
	fee := PlatformFeeMinor(tenant.PricingRules, amountMinor)
	net := amountMinor - fee
	if err := svc.s.Payments.post(ctx, tenantID, "",
		"PARTNER_CLEARING", "TENANT_REVENUE", net, f.Currency, "folio:net:"+f.ID+":"+pay.ID); err != nil {
		return nil, err
	}
	if err := svc.s.Payments.post(ctx, tenantID, "",
		"PARTNER_CLEARING", "PLATFORM_FEES", fee, f.Currency, "folio:fee:"+f.ID+":"+pay.ID); err != nil {
		return nil, err
	}
	f.Payments = append(f.Payments, pay)
	settled := false
	if f.OutstandingMinor() == 0 {
		f.State = model.FolioSettled
		f.SettledAt = svc.s.Now()
		settled = true
	}
	if err := svc.s.Store.Folios().Update(ctx, f); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "partner", f.PartnerID, "channel.folio_payment_recorded",
		map[string]any{"folio_id": f.ID, "payment_id": pay.ID, "amount_minor": amountMinor,
			"method": method, "currency": f.Currency,
			"outstanding_minor": f.OutstandingMinor(), "settled": settled})
	svc.s.Audit.Log(ctx, tenantID, "operator", "channel.folio_payment_recorded", "partner", f.PartnerID,
		map[string]any{"folio_id": f.ID, "payment_id": pay.ID, "amount_minor": amountMinor, "method": method})
	if settled {
		svc.emitSettled(ctx, tenantID, tenant.PricingRules, f)
	}
	return f, nil
}

// emitSettled announces a folio reaching SETTLED — shared by Settle,
// RecordPayment and RecordCredit (Phase 73: a fully-credited statement
// settles too) so downstream consumers see one event shape regardless of
// which path closed the statement. net/fee are the CUMULATIVE split
// across the payment ledger (each payment posted its own proportional
// split), and net is measured against the credit-adjusted total, so the
// event always matches the ledger exactly.
func (svc *FolioService) emitSettled(ctx context.Context, tenantID string, rules model.PricingRules, f *model.Folio) {
	var fee int64
	for _, p := range f.Payments {
		fee += PlatformFeeMinor(rules, p.AmountMinor)
	}
	net := f.AdjustedTotalMinor() - fee
	svc.s.Events.Emit(ctx, tenantID, "partner", f.PartnerID, "channel.folio_settled",
		map[string]any{"folio_id": f.ID, "period": f.Period, "currency": f.Currency,
			"total_minor": f.TotalMinor, "adjusted_total_minor": f.AdjustedTotalMinor(),
			"net_minor": net, "fee_minor": fee})
	svc.s.Audit.Log(ctx, tenantID, "operator", "channel.folio_settled", "partner", f.PartnerID,
		map[string]any{"folio_id": f.ID, "total_minor": f.TotalMinor})
}
