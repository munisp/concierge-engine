package domain

// Phase 74: partner self-service payment links. Dunning (Phase 67/72)
// and statement delivery (Phase 71) both told the partner what they owe
// — but the only way to pay was a bank transfer the operator then
// recorded by hand (Phase 70). Now the statement carries its own
// collection rail:
//
//   - A pay/ HMAC capability token (same discipline as stmt/, track/ —
//     the token IS the credential, cross-family tokens rejected) opens a
//     public pay page for one ISSUED folio. The page quotes the derived
//     outstanding balance — after partial payments (Phase 70) and
//     credit notes (Phase 73), never the stale face value.
//   - "Pay now" mints a hosted checkout on the tenant's live rail —
//     Stripe, or Paystack for the Africa-corridor currencies (same
//     routing as guest checkout, Phase 15) — for the outstanding
//     amount, and redirects. No PAN ever touches platform servers.
//   - The signed PSP webhook (metadata-routed, never tenant headers)
//     records the capture through the SAME RecordPayment path as an
//     operator-keyed payment: the folio's payment ledger, the
//     proportional net/fee money truth and auto-settlement are
//     identical regardless of who clicked pay.
//   - Honest degrade: with no live rail for the tenant+currency the
//     page says so and gives the bank-transfer reference — no fake
//     checkout, no "payment received" that was not.
//   - A capture that arrives after the balance moved (partner also
//     paid by transfer, or a second open checkout session) can never
//     be silently absorbed: the mismatch is emitted and audit-logged
//     for manual reconciliation — Phase 73's refund is the remedy.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

const payTokenPrefix = "pay/"

// PayTokenFor mints the public pay-page token for a folio.
func (svc *TripService) PayTokenFor(tenantID, folioID string) string {
	payload := payTokenPrefix + tenantID + "/" + folioID
	mac := hmac.New(sha256.New, svc.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac.Sum(nil))
}

// ParsePayToken verifies and extracts (tenantID, folioID). Tokens from
// other families (track/, stmt/, partner/) are rejected — a trip link
// or a statement link can never open a payment page.
func (svc *TripService) ParsePayToken(token string) (string, string, error) {
	_, _, payload, err := svc.parseAny(token)
	if err != nil {
		return "", "", err
	}
	rest, ok := strings.CutPrefix(payload, payTokenPrefix)
	if !ok {
		return "", "", httpx.ErrForbidden
	}
	return splitPayload(rest)
}

// PayURL is the public pay-page address for a folio.
func (svc *FolioService) PayURL(tenantID, folioID string) string {
	return svc.s.PublicBase() + "/v1/pay/" + svc.s.Trips.PayTokenFor(tenantID, folioID)
}

// PayView is the pay page's view model: the folio's derived balance
// plus which rail (if any) can actually take the money right now.
type PayView struct {
	TenantName       string     `json:"tenant_name"`
	PartnerName      string     `json:"partner_name"`
	FolioID          string     `json:"folio_id"`
	Period           string     `json:"period"`
	Currency         string     `json:"currency"`
	State            string     `json:"state"`
	TotalMinor       int64      `json:"total_minor"`
	CreditedMinor    int64      `json:"credited_minor"`
	PaidMinor        int64      `json:"paid_minor"`
	OutstandingMinor int64      `json:"outstanding_minor"`
	Rail             string     `json:"rail"`      // stripe | paystack | "" (no live rail)
	RailLive         bool       `json:"rail_live"` // honest gate — no fake checkout
	TermsDays        int        `json:"terms_days"`
	OverdueDays      int        `json:"overdue_days"`
	DueAt            *time.Time `json:"due_at,omitempty"`
}

// PayByToken resolves the pay page's view model.
func (svc *FolioService) PayByToken(ctx context.Context, token string) (*PayView, error) {
	tenantID, folioID, err := svc.s.Trips.ParsePayToken(token)
	if err != nil {
		return nil, err
	}
	f, err := svc.s.Store.Folios().Get(ctx, tenantID, folioID)
	if err != nil {
		return nil, err
	}
	tenant, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	partner, err := svc.s.Store.Partners().Get(ctx, tenantID, f.PartnerID)
	if err != nil {
		return nil, err
	}
	terms := tenant.PricingRules.FolioTerms()
	v := &PayView{
		TenantName: tenant.Name, PartnerName: partner.Name,
		FolioID: f.ID, Period: f.Period, Currency: f.Currency, State: f.State,
		TotalMinor: f.TotalMinor, CreditedMinor: f.CreditedMinor(),
		PaidMinor: f.PaidMinor(), OutstandingMinor: f.OutstandingMinor(),
		TermsDays: terms,
	}
	if f.State == model.FolioIssued {
		d := f.DueAt(terms)
		v.DueAt = &d
		v.OverdueDays = f.OverdueDays(svc.s.Now(), terms)
		if rail := svc.s.Payments.choosePSP(f.Currency, "", tenant.PaymentConfig); rail != "sandbox" {
			v.Rail, v.RailLive = rail, true
		}
	}
	return v, nil
}

// StartCheckout mints a hosted checkout for the folio's outstanding
// balance on the tenant's live rail and returns the redirect URL.
// ISSUED-only with a positive balance; with no live rail it is a
// conflict, never a fake checkout page. Each checkout is a fresh
// INVOICE intent keyed foliopay:{folio}:{intent} — the PSP webhook
// routes back on the signed metadata's folio_id.
func (svc *FolioService) StartCheckout(ctx context.Context, token string) (string, error) {
	tenantID, folioID, err := svc.s.Trips.ParsePayToken(token)
	if err != nil {
		return "", err
	}
	f, err := svc.s.Store.Folios().Get(ctx, tenantID, folioID)
	if err != nil {
		return "", err
	}
	if f.State != model.FolioIssued {
		return "", httpx.ErrConflict // open is accruing; settled is collected
	}
	outstanding := f.OutstandingMinor()
	if outstanding <= 0 {
		return "", httpx.ErrConflict
	}
	tenant, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return "", err
	}
	partner, err := svc.s.Store.Partners().Get(ctx, tenantID, f.PartnerID)
	if err != nil {
		return "", err
	}
	rail := svc.s.Payments.choosePSP(f.Currency, "", tenant.PaymentConfig)
	if rail == "sandbox" {
		return "", httpx.ErrConflict // honest: no live rail for this tenant/currency
	}
	intent := &model.PaymentIntent{
		ID: NewID(), TenantID: tenantID, Mode: "INVOICE",
		AmountMinor: outstanding, Currency: f.Currency, PSP: rail,
		State: "LINK_SENT", CreatedAt: svc.s.Now(),
	}
	intent.IdempotencyKey = "foliopay:" + f.ID + ":" + intent.ID
	returnURL := svc.PayURL(tenantID, f.ID)
	switch rail {
	case "stripe":
		u, ref, err := svc.s.Stripe.CreateFolioCheckoutSession(ctx, intent, f.ID, f.Period, partner.Email, returnURL)
		if err != nil {
			return "", err
		}
		intent.HostedURL, intent.PSPRef = u, ref
	case "paystack":
		u, ref, err := svc.s.Paystack.InitializeFolioTransaction(ctx, intent, f.ID, partner.Email, nil)
		if err != nil {
			return "", err
		}
		intent.HostedURL, intent.PSPRef = u, ref
	}
	if err := svc.s.Store.Payments().CreateIntent(ctx, intent); err != nil {
		return "", err
	}
	svc.s.Events.Emit(ctx, tenantID, "partner", f.PartnerID, "channel.folio_checkout_started",
		map[string]any{"folio_id": f.ID, "intent_id": intent.ID, "psp": rail,
			"amount_minor": outstanding, "currency": f.Currency})
	svc.s.Audit.Log(ctx, tenantID, "partner", "channel.folio_checkout_started", "partner", f.PartnerID,
		map[string]any{"folio_id": f.ID, "intent_id": intent.ID, "psp": rail, "amount_minor": outstanding})
	return intent.HostedURL, nil
}

// HandleFolioCapture is the PSP-webhook capture path for folio
// checkouts: the signed event's metadata routed us here. The capture
// records through the same path as an operator-keyed payment — the
// payment ledger, the proportional net/fee money truth and
// auto-settlement are identical regardless of who paid. Replay-safe on
// the intent state. A capture whose balance already moved is a loud
// mismatch (event + audit), never a silent drop and never a 5xx — the
// money exists, so the webhook acknowledges and the operator reconciles
// (Phase 73 refund).
func (svc *FolioService) HandleFolioCapture(ctx context.Context, tenantID, folioID, intentID, psp, reference string) error {
	intent, err := svc.s.Store.Payments().GetIntent(ctx, tenantID, intentID)
	if err != nil {
		return err
	}
	if intent.State == "CAPTURED" {
		return nil // PSP retries: the capture is already the payment ledger
	}
	if err := svc.s.Store.Payments().UpdateIntentState(ctx, tenantID, intentID, "CAPTURED", reference); err != nil {
		return err
	}
	f, err := svc.recordPayment(ctx, tenantID, folioID, intent.AmountMinor, psp, reference, "system")
	if err == httpx.ErrConflict {
		partnerID := ""
		if fl, gerr := svc.s.Store.Folios().Get(ctx, tenantID, folioID); gerr == nil {
			partnerID = fl.PartnerID
		}
		svc.s.Events.Emit(ctx, tenantID, "partner", partnerID, "channel.folio_payment_mismatch",
			map[string]any{"folio_id": folioID, "intent_id": intentID, "psp": psp,
				"reference": reference, "amount_minor": intent.AmountMinor,
				"reason": "captured amount exceeds the remaining balance — reconcile manually (refund via credit)"})
		svc.s.Audit.Log(ctx, tenantID, "system", "channel.folio_payment_mismatch", "partner", partnerID,
			map[string]any{"folio_id": folioID, "intent_id": intentID, "psp": psp,
				"reference": reference, "amount_minor": intent.AmountMinor})
		return nil
	}
	if err != nil {
		return err
	}
	svc.s.Events.Emit(ctx, tenantID, "partner", f.PartnerID, "channel.folio_payment_captured",
		map[string]any{"folio_id": folioID, "intent_id": intentID, "psp": psp,
			"reference": reference, "amount_minor": intent.AmountMinor, "currency": intent.Currency,
			"outstanding_minor": f.OutstandingMinor(), "settled": f.State == model.FolioSettled})
	return nil
}
