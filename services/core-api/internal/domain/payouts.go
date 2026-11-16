// Partner payout saga (Master Spec section 11.3, Phase 2):
//
//	PENDING --quote--> QUOTED --execute--> IN_FLIGHT --fulfil--> SETTLED
//	                                        |
//	                                        +-- timeout/abort --> COMPENSATED
//
// Ledger movements (TigerBeetle first, Postgres mirror second):
//
//	execute:  debit PARTNER_PAYABLE      credit IN_FLIGHT_SETTLEMENT  (amount)
//	fulfil:   debit IN_FLIGHT_SETTLEMENT credit PARTNER_CLEARING      (receive)
//	          debit IN_FLIGHT_SETTLEMENT credit FX_GAIN_LOSS          (remainder)
//	compensate: debit IN_FLIGHT_SETTLEMENT credit PARTNER_PAYABLE     (amount)
//
// Every posting is idempotent; saga state is persisted so any crash resumes
// cleanly, and compensation returns the payable to pending + pages ops.
package domain

import (
	"context"
	"fmt"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type PayoutService struct {
	s       *Services
	ml      *MojaloopClient
	baseURL string // public base URL for adapter callbacks
}

func NewPayoutService(s *Services, ml *MojaloopClient, baseURL string) *PayoutService {
	return &PayoutService{s: s, ml: ml, baseURL: baseURL}
}

// Create registers a partner payable (usually from a completed, settled
// booking). Idempotent on the caller key.
func (svc *PayoutService) Create(ctx context.Context, tenantID, partnerID, bookingID string, amountMinor int64, currency, idem string) (*model.Payout, error) {
	if amountMinor <= 0 || idem == "" {
		return nil, httpx.ErrValidation
	}
	partner, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	if partner.Status != "VETTED" {
		return nil, fmt.Errorf("%w: partner not vetted", httpx.ErrConflict)
	}
	p := &model.Payout{
		ID: NewID(), TenantID: tenantID, PartnerID: partnerID, BookingID: bookingID,
		AmountMinor: amountMinor, Currency: currency, Rail: partner.PayoutRail,
		State: model.PayoutPending, IdempotencyKey: idem,
		CreatedAt: svc.s.Now(), UpdatedAt: svc.s.Now(),
	}
	if err := svc.s.Store.Payouts().Create(ctx, p); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "payout", p.ID, "payout.created",
		map[string]any{"partner_id": partnerID, "amount_minor": amountMinor, "rail": p.Rail})
	return p, nil
}

// Quote locks FX + fees for a MOJALOOP payout (FSPIOP quote via the Rust
// adapter). MANUAL payouts skip straight to settlement by ops.
func (svc *PayoutService) Quote(ctx context.Context, tenantID, payoutID string) (*model.Payout, error) {
	p, err := svc.s.Store.Payouts().Get(ctx, tenantID, payoutID)
	if err != nil {
		return nil, err
	}
	if p.State != model.PayoutPending {
		return nil, httpx.ErrConflict
	}
	if p.Rail != "MOJALOOP" {
		// Manual rail: no scheme quote; ops settles off-band.
		p.State = model.PayoutQuoted
		p.UpdatedAt = svc.s.Now()
		if err := svc.s.Store.Payouts().Update(ctx, p); err != nil {
			return nil, err
		}
		return p, nil
	}
	partner, err := svc.s.Store.Partners().Get(ctx, tenantID, p.PartnerID)
	if err != nil {
		return nil, err
	}
	q, err := svc.ml.Quote(ctx, partner.PayoutRef, p.AmountMinor, p.Currency)
	if err != nil {
		// Fail safe: stay PENDING; the retry is the same call (idempotent).
		svc.s.Events.Emit(ctx, tenantID, "payout", p.ID, "payout.quote_failed",
			map[string]any{"error": err.Error()})
		return nil, err
	}
	p.State = model.PayoutQuoted
	p.QuoteID = q.QuoteID
	p.FXRate = q.FXRate
	p.ReceiveMinor = q.ReceiveMinor
	p.FeesMinor = q.FeesMinor
	// Phase 48: keep the scheme's FX-lock expiry — Execute enforces it.
	if exp, err := time.Parse(time.RFC3339, q.ExpiresAt); err == nil {
		p.QuoteExpiresAt = exp
	}
	p.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.Payouts().Update(ctx, p); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "payout", p.ID, "payout.quoted",
		map[string]any{"quote_id": q.QuoteID, "receive_minor": q.ReceiveMinor})
	return p, nil
}

// Execute reserves the funds in-ledger and initiates the scheme transfer.
func (svc *PayoutService) Execute(ctx context.Context, tenantID, payoutID string) (*model.Payout, error) {
	p, err := svc.s.Store.Payouts().Get(ctx, tenantID, payoutID)
	if err != nil {
		return nil, err
	}
	if p.State != model.PayoutQuoted {
		return nil, httpx.ErrConflict
	}
	// Phase 48: never ride a stale FX lock — an expired quote must be
	// re-quoted, not executed.
	if !p.QuoteExpiresAt.IsZero() && svc.s.Now().After(p.QuoteExpiresAt) {
		return nil, fmt.Errorf("%w: quote expired — re-quote", httpx.ErrConflict)
	}
	// Reserve: payable -> in-flight settlement (Spec section 11.3).
	if err := svc.s.Payments.post(ctx, tenantID, p.BookingID,
		"PARTNER_PAYABLE", "IN_FLIGHT_SETTLEMENT", p.AmountMinor, p.Currency,
		"payout:reserve:"+p.ID); err != nil {
		return nil, err
	}
	if p.Rail == "MOJALOOP" {
		tr, err := svc.ml.Transfer(ctx, MLTransferRequest{
			QuoteID:        p.QuoteID,
			IdempotencyKey: "payout:" + p.ID,
			PayoutID:       p.ID,
			TenantID:       tenantID,
			CallbackURL:    svc.baseURL + "/v1/payouts/callbacks/mojaloop",
		})
		if err != nil {
			// Funds are reserved; transfer initiation is retried by ops.
			// Compensation is explicit (payout.failed -> compensate), never
			// silent.
			svc.s.Events.Emit(ctx, tenantID, "payout", p.ID, "payout.transfer_failed",
				map[string]any{"error": err.Error()})
			p.State = model.PayoutFailed
			p.UpdatedAt = svc.s.Now()
			_ = svc.s.Store.Payouts().Update(ctx, p)
			return nil, err
		}
		p.TransferID = tr.TransferID
	}
	p.State = model.PayoutInFlight
	p.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.Payouts().Update(ctx, p); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "payout", p.ID, "payout.in_flight",
		map[string]any{"transfer_id": p.TransferID})
	return p, nil
}

// HandleCallback processes adapter notifications. Idempotent by saga state:
// terminal states are absorbing, so event replays are no-ops.
//
// Kinds: transfer.fulfilled | transfer.aborted | transfer.timeout
func (svc *PayoutService) HandleCallback(ctx context.Context, tenantID, transferID, kind string) (*model.Payout, error) {
	p, err := svc.s.Store.Payouts().GetByTransfer(ctx, tenantID, transferID)
	if err != nil {
		return nil, err
	}
	if p.State == model.PayoutSettled || p.State == model.PayoutCompensated {
		return p, nil // absorbing: replay-safe
	}
	if p.State != model.PayoutInFlight {
		return nil, httpx.ErrConflict
	}
	switch kind {
	case "transfer.fulfilled":
		if err := svc.settleFulfilled(ctx, p); err != nil {
			return nil, err
		}
		return p, nil
	case "transfer.aborted", "transfer.timeout":
		return svc.compensate(ctx, p, kind)
	default:
		return nil, httpx.ErrValidation
	}
}

func (svc *PayoutService) settleFulfilled(ctx context.Context, p *model.Payout) error {
	receive := p.ReceiveMinor
	if receive <= 0 {
		receive = p.AmountMinor // manual rail: no FX quote, full amount out
	}
	if err := svc.s.Payments.post(ctx, p.TenantID, p.BookingID,
		"IN_FLIGHT_SETTLEMENT", "PARTNER_CLEARING", receive, p.Currency,
		"payout:fulfil:"+p.ID); err != nil {
		return err
	}
	// Zero out the in-flight account: remainder is FX gain/loss + scheme fees.
	if diff := p.AmountMinor - receive; diff > 0 {
		if err := svc.s.Payments.post(ctx, p.TenantID, p.BookingID,
			"IN_FLIGHT_SETTLEMENT", "FX_GAIN_LOSS", diff, p.Currency,
			"payout:fxdiff:"+p.ID); err != nil {
			return err
		}
	}
	p.State = model.PayoutSettled
	p.SettledAt = svc.s.Now()
	p.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.Payouts().Update(ctx, p); err != nil {
		return err
	}
	svc.s.Events.Emit(ctx, p.TenantID, "payout", p.ID, "payout.settled",
		map[string]any{"receive_minor": receive, "fx_diff_minor": p.AmountMinor - receive})
	return nil
}

// Compensate returns the reserved payable to pending and pages ops — the
// partner never waits on a correspondent-banking chain, and TigerBeetle
// still shows every cent of it (Spec section 11.3).
func (svc *PayoutService) Compensate(ctx context.Context, tenantID, payoutID, reason string) (*model.Payout, error) {
	p, err := svc.s.Store.Payouts().Get(ctx, tenantID, payoutID)
	if err != nil {
		return nil, err
	}
	return svc.compensate(ctx, p, reason)
}

func (svc *PayoutService) compensate(ctx context.Context, p *model.Payout, reason string) (*model.Payout, error) {
	if p.State == model.PayoutCompensated {
		return p, nil
	}
	if p.State != model.PayoutInFlight && p.State != model.PayoutFailed {
		return nil, httpx.ErrConflict
	}
	if p.State == model.PayoutInFlight {
		if err := svc.s.Payments.post(ctx, p.TenantID, p.BookingID,
			"IN_FLIGHT_SETTLEMENT", "PARTNER_PAYABLE", p.AmountMinor, p.Currency,
			"payout:compensate:"+p.ID); err != nil {
			return nil, err
		}
	}
	p.State = model.PayoutCompensated
	p.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.Payouts().Update(ctx, p); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, p.TenantID, "payout", p.ID, "payout.compensated",
		map[string]any{"reason": reason})
	// Page ops via the HITL lane: compensation always has a human in the loop.
	_, _ = svc.s.Handoffs.Open(ctx, p.TenantID, "", "POLICY",
		fmt.Sprintf("payout %s compensated (%s): partner payable restored, ops follow-up required", p.ID, reason), nil)
	return p, nil
}

func (svc *PayoutService) Get(ctx context.Context, tenantID, id string) (*model.Payout, error) {
	return svc.s.Store.Payouts().Get(ctx, tenantID, id)
}

func (svc *PayoutService) List(ctx context.Context, tenantID string) ([]model.Payout, error) {
	return svc.s.Store.Payouts().List(ctx, tenantID)
}

// PayoutSettlementTimeoutHours is how long an IN_FLIGHT payout may go without
// a scheme callback before the core recovers it on its own. Adapter
// notifications are fire-and-forget HTTP — a crash or a dropped callback must
// never strand partner funds in the in-flight account.
const PayoutSettlementTimeoutHours = 48

// SweepTimeouts compensates IN_FLIGHT payouts whose last state change is
// older than the settlement timeout. Runs from the hourly sweep; safe to run
// concurrently with callbacks because compensation is idempotent per saga
// state and per ledger key.
func (svc *PayoutService) SweepTimeouts(ctx context.Context, tenantID string) (int, error) {
	payouts, err := svc.s.Store.Payouts().List(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	cutoff := svc.s.Now().Add(-PayoutSettlementTimeoutHours * time.Hour)
	n := 0
	for _, p := range payouts {
		if p.State != model.PayoutInFlight || p.UpdatedAt.After(cutoff) {
			continue
		}
		p := p
		if _, err := svc.compensate(ctx, &p, "settlement timeout"); err != nil {
			svc.s.Events.Emit(ctx, tenantID, "payout", p.ID, "payout.sweep_failed",
				map[string]any{"error": err.Error()})
			continue
		}
		n++
	}
	return n, nil
}
