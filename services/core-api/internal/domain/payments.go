// Payment service (M5): payment orchestration across PSPs with failover,
// hosted payment links (PCI SAQ-A — no PAN ever touches platform servers),
// deposits/pre-auth/milestones, split settlement into the TigerBeetle
// double-entry ledger (platform fee / tenant / partner).
package domain

import (
	"context"
	"fmt"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type PaymentService struct{ s *Services }

var validModes = map[string]bool{
	"FULL": true, "DEPOSIT_BALANCE": true, "PREAUTH_CAPTURE": true,
	"MILESTONE": true, "INVOICE": true,
}

// CreatePaymentLink mints a hosted payment link for a quote (FR-P2). This is
// a TIER-2 action: the policy engine verdict is attached to the intent.
// It NEVER executes a charge (AI spec §3.1 create_payment_link).
func (svc *PaymentService) CreatePaymentLink(ctx context.Context, tenantID, quoteID, mode, actor, idem string) (*model.PaymentIntent, error) {
	if !validModes[mode] {
		return nil, httpx.ErrValidation
	}
	if idem == "" {
		return nil, httpx.ErrValidation
	}
	q, err := svc.s.Store.Quotes().Get(ctx, tenantID, quoteID)
	if err != nil {
		return nil, err
	}
	amount := q.TotalMinor
	if mode == "DEPOSIT_BALANCE" && q.DepositMinor > 0 {
		amount = q.DepositMinor
	}
	intent := &model.PaymentIntent{
		ID: NewID(), TenantID: tenantID, QuoteID: q.ID, Mode: mode,
		AmountMinor: amount, Currency: q.Currency, PSP: "stripe",
		State: "LINK_SENT", IdempotencyKey: idem, CreatedAt: svc.s.Now(),
	}
	// Hosted link is brand-skinned and PCI-safe; no card data through chat.
	// With Stripe configured we mint a real Checkout Session; otherwise the
	// deterministic sandbox URL keeps dev/CI network-free.
	intent.HostedURL = fmt.Sprintf("https://pay.concierge.engine/p/%s", intent.ID)
	if svc.s.Stripe.Enabled() {
		hostedURL, sessionID, err := svc.s.Stripe.CreateCheckoutSession(ctx, intent)
		if err != nil {
			return nil, err
		}
		intent.HostedURL, intent.PSPRef = hostedURL, sessionID
	}
	if err := svc.s.Store.Payments().CreateIntent(ctx, intent); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "payment", intent.ID, "payment.link_created",
		map[string]any{"quote_id": quoteID, "amount_minor": amount, "mode": mode})
	return intent, nil
}

// HandlePSPWebhook is the PSP-adapter ingress (Stripe first; the orchestrator
// pattern keeps tenants PSP-unlocked). On capture it posts the double-entry
// splits into TigerBeetle and mirrors the reconciled projection in Postgres.
func (svc *PaymentService) HandlePSPWebhook(ctx context.Context, tenantID, intentID, event, pspRef string) error {
	intent, err := svc.s.Store.Payments().GetIntent(ctx, tenantID, intentID)
	if err != nil {
		return err
	}
	switch event {
	case "payment.captured", "checkout.session.completed":
		if err := svc.s.Store.Payments().UpdateIntentState(ctx, tenantID, intentID, "CAPTURED", pspRef); err != nil {
			return err
		}
		if err := svc.settle(ctx, intent); err != nil {
			return err
		}
		svc.s.Events.Emit(ctx, tenantID, "payment", intentID, "payment.captured",
			map[string]any{"amount_minor": intent.AmountMinor, "psp_ref": pspRef})
	case "payment.failed":
		if err := svc.s.Store.Payments().UpdateIntentState(ctx, tenantID, intentID, "FAILED", pspRef); err != nil {
			return err
		}
		svc.s.Events.Emit(ctx, tenantID, "payment", intentID, "payment.failed", nil)
	case "charge.refunded":
		if err := svc.s.Store.Payments().UpdateIntentState(ctx, tenantID, intentID, "REFUNDED", pspRef); err != nil {
			return err
		}
		if err := svc.post(ctx, intent.TenantID, intent.BookingID,
			"TENANT_REVENUE", "CLIENT_CLEARING", intent.AmountMinor, intent.Currency,
			"refund:"+intentID); err != nil {
			return err
		}
		svc.s.Events.Emit(ctx, tenantID, "payment", intentID, "payment.refunded", nil)
	}
	return nil
}

// settle posts the split settlement (Master Spec §11 walkthrough):
// debit PSP clearing, credit tenant revenue (net), credit platform fee.
func (svc *PaymentService) settle(ctx context.Context, intent *model.PaymentIntent) error {
	tenant, err := svc.s.Store.Tenants().Get(ctx, intent.TenantID)
	if err != nil {
		return err
	}
	fee := PlatformFeeMinor(tenant.PricingRules, intent.AmountMinor)
	net := intent.AmountMinor - fee

	// debit CLIENT_CLEARING -> credit TENANT_REVENUE (net of platform fee)
	if err := svc.post(ctx, intent.TenantID, intent.BookingID,
		"CLIENT_CLEARING", "TENANT_REVENUE", net, intent.Currency,
		"settle:net:"+intent.ID); err != nil {
		return err
	}
	// debit CLIENT_CLEARING -> credit PLATFORM_FEES (take rate)
	if err := svc.post(ctx, intent.TenantID, intent.BookingID,
		"CLIENT_CLEARING", "PLATFORM_FEES", fee, intent.Currency,
		"settle:fee:"+intent.ID); err != nil {
		return err
	}
	return nil
}

// post executes one idempotent double-entry posting: TigerBeetle first
// (money truth), then the reconciled Postgres mirror.
func (svc *PaymentService) post(ctx context.Context, tenantID, bookingID, debit, credit string, amount int64, currency, idem string) error {
	lp := &model.LedgerPosting{
		ID: NewID(), TenantID: tenantID, BookingID: bookingID,
		DebitCode: debit, CreditCode: credit, AmountMinor: amount, Currency: currency,
		IdempotencyKey: idem, CreatedAt: svc.s.Now(),
	}
	transferID, err := svc.s.Ledger.Post(ctx, lp)
	if err != nil {
		// Money truth is authoritative: if TigerBeetle is unreachable the
		// posting is queued via outbox for the reconciliation workflow rather
		// than silently dropped.
		svc.s.Events.Emit(ctx, tenantID, "ledger", lp.ID, "ledger.posting_pending",
			map[string]any{"debit": debit, "credit": credit, "amount_minor": amount, "idempotency_key": idem})
	} else {
		lp.TBTransferID = transferID
	}
	return svc.s.Store.Payments().AddPosting(ctx, lp)
}

func (svc *PaymentService) GetIntent(ctx context.Context, tenantID, id string) (*model.PaymentIntent, error) {
	return svc.s.Store.Payments().GetIntent(ctx, tenantID, id)
}

func (svc *PaymentService) Postings(ctx context.Context, tenantID string) ([]model.LedgerPosting, error) {
	return svc.s.Store.Payments().Postings(ctx, tenantID)
}
