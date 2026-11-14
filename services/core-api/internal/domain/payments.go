// Payment service (M5): payment orchestration across PSPs with failover,
// hosted payment links (PCI SAQ-A — no PAN ever touches platform servers),
// deposits/pre-auth/milestones, split settlement into the TigerBeetle
// double-entry ledger (platform fee / tenant / partner).
package domain

import (
	"context"
	"fmt"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type PaymentService struct{ s *Services }

var validModes = map[string]bool{
	"FULL": true, "DEPOSIT_BALANCE": true, "PREAUTH_CAPTURE": true,
	"MILESTONE": true, "INVOICE": true,
}

// CreatePaymentLinkInput extends link minting with PSP routing (Phase 15):
// explicit PSP override, market channels, and client localization.
type CreatePaymentLinkInput struct {
	QuoteID        string   `json:"quote_id"`
	Mode           string   `json:"mode"`
	PSP            string   `json:"psp,omitempty"`      // stripe | paystack; empty = route by currency
	Channels       []string `json:"channels,omitempty"` // PSP rails, e.g. ussd / bank_transfer
	IdempotencyKey string   `json:"idempotency_key"`
}

// choosePSP routes a payment to the rail that converts in the customer's
// market: explicit request wins (unless the tenant disabled that PSP);
// NGN/GHS/KES/ZAR default to Paystack when configured (Africa wave);
// everything else — the EU/US-first launch markets — routes to Stripe;
// sandbox link when neither is live. Tenant.PaymentConfig disables rails
// the tenant has no PSP account/entity for (Phase 17).
func (svc *PaymentService) choosePSP(currency, requested string, pc model.PaymentConfig) string {
	if requested != "" {
		return strings.ToLower(requested)
	}
	if _, ok := paystackCurrencies[strings.ToUpper(currency)]; ok &&
		svc.s.Paystack.Enabled() && !pc.DisablePaystack {
		return "paystack"
	}
	if svc.s.Stripe.Enabled() && !pc.DisableStripe {
		return "stripe"
	}
	if svc.s.Paystack.Enabled() && !pc.DisablePaystack {
		return "paystack"
	}
	return "sandbox"
}

// PaymentMethods answers "how can a customer pay in this currency?" — the
// integrator discovery surface for international checkout. Tenant payment
// config filters out disabled rails (Phase 17).
func (svc *PaymentService) PaymentMethods(ctx context.Context, tenantID, currency string) ([]PaymentMethod, error) {
	if err := validateCurrency(currency); err != nil {
		return nil, err
	}
	var pc model.PaymentConfig
	if t, err := svc.s.Store.Tenants().Get(ctx, tenantID); err == nil {
		pc = t.PaymentConfig
	}
	cur := strings.ToUpper(currency)
	var out []PaymentMethod
	if channels, ok := paystackCurrencies[cur]; ok && !pc.DisablePaystack {
		out = append(out, PaymentMethod{
			PSP: "paystack", Currency: cur, Channels: channels,
			Live: svc.s.Paystack.Enabled(),
		})
	}
	if !pc.DisableStripe {
		out = append(out, PaymentMethod{
			PSP: "stripe", Currency: cur, Channels: stripeChannelsFor(cur),
			Live: svc.s.Stripe.Enabled(),
		})
	}
	return out, nil
}

// CreatePaymentLink mints a hosted payment link for a quote (FR-P2). This is
// a TIER-2 action: the policy engine verdict is attached to the intent.
// It NEVER executes a charge (AI spec §3.1 create_payment_link).
func (svc *PaymentService) CreatePaymentLink(ctx context.Context, tenantID, quoteID, mode, actor, idem string) (*model.PaymentIntent, error) {
	return svc.CreatePaymentLinkV2(ctx, tenantID, CreatePaymentLinkInput{
		QuoteID: quoteID, Mode: mode, IdempotencyKey: idem,
	})
}

// CreatePaymentLinkV2 routes across PSPs and localizes the checkout page.
func (svc *PaymentService) CreatePaymentLinkV2(ctx context.Context, tenantID string, in CreatePaymentLinkInput) (*model.PaymentIntent, error) {
	if !validModes[in.Mode] {
		return nil, httpx.ErrValidation
	}
	if in.IdempotencyKey == "" {
		return nil, httpx.ErrValidation
	}
	q, err := svc.s.Store.Quotes().Get(ctx, tenantID, in.QuoteID)
	if err != nil {
		return nil, err
	}
	if err := validateCurrency(q.Currency); err != nil {
		return nil, fmt.Errorf("%w: quote currency %q", httpx.ErrValidation, q.Currency)
	}
	amount := q.TotalMinor
	if in.Mode == "DEPOSIT_BALANCE" && q.DepositMinor > 0 {
		amount = q.DepositMinor
	}
	// Localization: resolve the client behind the quote for locale + email.
	locale, email, clientID := "", "", ""
	if r, err := svc.s.Store.Requests().Get(ctx, tenantID, q.RequestID); err == nil && r.ClientID != "" {
		clientID = r.ClientID
		if cl, err := svc.s.Store.Clients().Get(ctx, tenantID, r.ClientID); err == nil {
			locale, email = cl.Locale, cl.Email
		}
	}
	var pc model.PaymentConfig
	if t, err := svc.s.Store.Tenants().Get(ctx, tenantID); err == nil {
		pc = t.PaymentConfig
	}
	if in.PSP != "" && in.PSP != "stripe" && in.PSP != "paystack" {
		return nil, fmt.Errorf("%w: unknown psp %q", httpx.ErrValidation, in.PSP)
	}
	if in.PSP == "stripe" && pc.DisableStripe || in.PSP == "paystack" && pc.DisablePaystack {
		return nil, fmt.Errorf("%w: psp %q disabled for this tenant", httpx.ErrValidation, in.PSP)
	}
	psp := svc.choosePSP(q.Currency, in.PSP, pc)
	intent := &model.PaymentIntent{
		ID: NewID(), TenantID: tenantID, QuoteID: q.ID, Mode: in.Mode,
		AmountMinor: amount, Currency: q.Currency, PSP: psp,
		State: "LINK_SENT", IdempotencyKey: in.IdempotencyKey, CreatedAt: svc.s.Now(),
	}
	// Phase 18: apply the client's accrued referral rewards — the PSP is
	// charged AmountMinor - RewardAppliedMinor; the platform covers the
	// difference via a compensation posting at capture, so the tenant's
	// settlement is computed on the full obligation.
	if clientID != "" {
		if bal, err := svc.s.Store.Rewards().Balance(ctx, tenantID, clientID); err == nil && bal > 0 {
			if bal > amount {
				bal = amount
			}
			intent.RewardAppliedMinor = bal
		}
	}
	// Hosted link is brand-skinned and PCI-safe; no card data through chat.
	// With a PSP configured we mint a real hosted page; otherwise the
	// deterministic sandbox URL keeps dev/CI network-free.
	intent.HostedURL = fmt.Sprintf("https://pay.concierge.engine/p/%s", intent.ID)
	charge := *intent
	charge.AmountMinor = intent.AmountMinor - intent.RewardAppliedMinor
	switch psp {
	case "stripe":
		if svc.s.Stripe.Enabled() {
			hostedURL, sessionID, err := svc.s.Stripe.CreateCheckoutSession(ctx, &charge, locale, email,
				stripeChannelsFor(q.Currency))
			if err != nil {
				return nil, err
			}
			intent.HostedURL, intent.PSPRef = hostedURL, sessionID
		}
	case "paystack":
		if svc.s.Paystack.Enabled() {
			hostedURL, reference, err := svc.s.Paystack.InitializeTransaction(ctx, &charge, email, in.Channels)
			if err != nil {
				return nil, err
			}
			intent.HostedURL, intent.PSPRef = hostedURL, reference
		}
	}
	if err := svc.s.Store.Payments().CreateIntent(ctx, intent); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "payment", intent.ID, "payment.link_created",
		map[string]any{"quote_id": in.QuoteID, "amount_minor": amount, "mode": in.Mode, "psp": psp,
			"reward_applied_minor": intent.RewardAppliedMinor})
	return intent, nil
}

// Refund initiates a refund on a CAPTURED intent (Phase 15). Idempotent:
// REFUND_PENDING/CAPTURED transition is the guard; the PSP webhook
// (charge.refunded) settles the final state and the ledger reversal.
func (svc *PaymentService) Refund(ctx context.Context, tenantID, intentID string) (*model.PaymentIntent, error) {
	intent, err := svc.s.Store.Payments().GetIntent(ctx, tenantID, intentID)
	if err != nil {
		return nil, err
	}
	switch intent.State {
	case "REFUND_PENDING", "REFUNDED":
		return intent, nil // replay-safe
	case "CAPTURED":
	default:
		return nil, httpx.ErrConflict // only captured money can be refunded
	}
	switch intent.PSP {
	case "stripe":
		if svc.s.Stripe.Enabled() {
			if err := svc.s.Stripe.Refund(ctx, intent.PSPRef); err != nil {
				return nil, err
			}
		}
	case "paystack":
		if svc.s.Paystack.Enabled() {
			// The PSP captured the reward-reduced amount; refund no more.
			if err := svc.s.Paystack.Refund(ctx, intent.PSPRef,
				intent.AmountMinor-intent.RewardAppliedMinor); err != nil {
				return nil, err
			}
		}
	}
	if err := svc.s.Store.Payments().UpdateIntentState(ctx, tenantID, intentID, "REFUND_PENDING", intent.PSPRef); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "payment", intentID, "payment.refund_initiated",
		map[string]any{"amount_minor": intent.AmountMinor, "psp": intent.PSP})
	svc.s.Audit.Log(ctx, tenantID, "api", "payment.refund_initiated", "payment", intentID, nil)
	intent.State = "REFUND_PENDING"
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
		// Phase 18: settle on the FULL obligation, redeem the client's
		// rewards against this intent, and post the platform compensation
		// (PLATFORM_FEES -> CLIENT_CLEARING) so the clearing account nets
		// to what the PSP actually captured.
		if err := svc.settle(ctx, intent); err != nil {
			return err
		}
		// Phase 26: pay-for-performance acquisition fee — when this is the
		// client's first booking AND first-touch attribution puts it on a
		// platform acquisition channel, FeePct moves TENANT_REVENUE →
		// PLATFORM_ACQUISITION_FEES (idempotent per intent).
		if err := svc.assessAcquisitionFee(ctx, intent); err != nil {
			return err
		}
		if intent.RewardAppliedMinor > 0 {
			clientID := ""
			if q, err := svc.s.Store.Quotes().Get(ctx, tenantID, intent.QuoteID); err == nil {
				if r, err := svc.s.Store.Requests().Get(ctx, tenantID, q.RequestID); err == nil {
					clientID = r.ClientID
				}
			}
			if clientID != "" {
				covered, err := svc.s.Store.Rewards().Redeem(ctx, tenantID, clientID,
					intentID, intent.RewardAppliedMinor)
				if err != nil {
					return err
				}
				if covered > 0 {
					if err := svc.post(ctx, tenantID, intent.BookingID,
						"PLATFORM_FEES", "CLIENT_CLEARING", covered, intent.Currency,
						"reward:redeem:"+intentID); err != nil {
						return err
					}
					svc.s.Events.Emit(ctx, tenantID, "client", clientID, "referral.reward_redeemed",
						map[string]any{"intent_id": intentID, "amount_minor": covered})
				}
			}
		}
		svc.s.Events.Emit(ctx, tenantID, "payment", intentID, "payment.captured",
			map[string]any{"amount_minor": intent.AmountMinor, "psp_ref": pspRef,
				"reward_applied_minor": intent.RewardAppliedMinor})
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
		// Reverse the reward compensation and return the rewards to the
		// client's balance — a refund never burns referral credit.
		if intent.RewardAppliedMinor > 0 {
			if err := svc.post(ctx, intent.TenantID, intent.BookingID,
				"CLIENT_CLEARING", "PLATFORM_FEES", intent.RewardAppliedMinor, intent.Currency,
				"reward:refund-clawback:"+intentID); err != nil {
				return err
			}
			if err := svc.s.Store.Rewards().RefundRedemption(ctx, intent.TenantID, intentID); err != nil {
				return err
			}
		}
		// Phase 26: claw the acquisition fee back — the platform earns it
		// only on money the tenant actually kept.
		if err := svc.clawbackAcquisitionFee(ctx, intent); err != nil {
			return err
		}
		svc.s.Events.Emit(ctx, tenantID, "payment", intentID, "payment.refunded", nil)
	}
	return nil
}

// HandlePaystackWebhook is the Paystack ingress: charge.success maps to
// capture (reference = intent id, set at initialize), refund.processed to the
// final refund settlement. tenantID comes from the SIGNED payload metadata
// (attached at initialize) — never from request headers.
func (svc *PaymentService) HandlePaystackWebhook(ctx context.Context, tenantID, eventType, reference string) error {
	if tenantID == "" || reference == "" {
		return httpx.ErrValidation
	}
	switch eventType {
	case "charge.success":
		return svc.HandlePSPWebhook(ctx, tenantID, reference, "payment.captured", reference)
	case "refund.processed":
		return svc.HandlePSPWebhook(ctx, tenantID, reference, "charge.refunded", reference)
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
