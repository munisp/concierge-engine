// Settlement batching for the Mojaloop payout lane (Phase 48).
//
// Single payouts pay one scheme fee each. A partner with a dozen completed
// trips pays twelve. Batching nets one partner's PENDING payouts (same
// currency) into ONE scheme transfer — one fee, one FX lock, one callback —
// while every member payout keeps its own ledger postings with the SAME
// idempotency keys as the single-payout flow, so a line can move between
// single and batched settlement without double-posting.
//
// Batch saga (mirrors the payout saga):
//
//	OPEN --quote--> QUOTED --execute--> IN_FLIGHT --fulfil--> SETTLED
//	                                       |
//	                                       +-- timeout/abort --> COMPENSATED
//
// Member payouts move in lockstep: linked PENDING at assemble, IN_FLIGHT at
// execute, SETTLED/COMPENSATED at callback.
package domain

import (
	"context"
	"errors"
	"fmt"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type SettlementService struct {
	s       *Services
	ml      *MojaloopClient
	baseURL string
}

func NewSettlementService(s *Services, ml *MojaloopClient, baseURL string) *SettlementService {
	return &SettlementService{s: s, ml: ml, baseURL: baseURL}
}

// Assemble nets one partner's PENDING Mojaloop payouts in one currency into
// a batch. Requires at least two eligible payouts (a single payout settles
// cheaper through the single flow). Idempotent on the caller key.
func (svc *SettlementService) Assemble(ctx context.Context, tenantID, partnerID, currency, idem string) (*model.SettlementBatch, error) {
	if idem == "" || currency == "" {
		return nil, httpx.ErrValidation
	}
	// Replay first: a retried Assemble after success finds its lines already
	// batched (no longer pending) — eligibility checks must not mask replay.
	if existing, err := svc.s.Store.Settlements().GetByIdem(ctx, tenantID, idem); err == nil {
		return existing, nil
	}
	partner, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	if partner.Status != "VETTED" {
		return nil, fmt.Errorf("%w: partner not vetted", httpx.ErrConflict)
	}
	if partner.PayoutRail != "MOJALOOP" {
		return nil, fmt.Errorf("%w: partner not on the Mojaloop rail", httpx.ErrValidation)
	}
	pending, err := svc.s.Store.Payouts().ListPendingForPartner(ctx, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	var lines []model.Payout
	var total int64
	for _, p := range pending {
		if p.Currency != currency {
			continue // never blend currencies in one transfer (Phase 40 lesson)
		}
		lines = append(lines, p)
		total += p.AmountMinor
	}
	if len(lines) < 2 {
		return nil, fmt.Errorf("%w: need at least 2 pending payouts to batch (got %d)", httpx.ErrValidation, len(lines))
	}
	b := &model.SettlementBatch{
		ID: NewID(), TenantID: tenantID, PartnerID: partnerID, Currency: currency,
		AmountMinor: total, State: model.BatchOpen, IdempotencyKey: idem,
		CreatedAt: svc.s.Now(), UpdatedAt: svc.s.Now(),
	}
	if err := svc.s.Store.Settlements().Create(ctx, b); err != nil {
		if errors.Is(err, httpx.ErrConflict) {
			return svc.s.Store.Settlements().GetByIdem(ctx, tenantID, idem) // replay
		}
		return nil, err
	}
	// Link the lines. They stay PENDING (owned by the batch now);
	// ListPendingForPartner excludes batched lines from future assemblies.
	for _, p := range lines {
		p := p
		p.BatchID = b.ID
		p.UpdatedAt = svc.s.Now()
		if err := svc.s.Store.Payouts().Update(ctx, &p); err != nil {
			return nil, err
		}
	}
	svc.s.Events.Emit(ctx, tenantID, "settlement_batch", b.ID, "settlement.batch_assembled",
		map[string]any{"partner_id": partnerID, "currency": currency, "lines": len(lines), "amount_minor": total})
	return b, nil
}

// QuoteBatch locks FX + fees on the NETTED amount — one scheme quote for
// the whole batch. The fee saving versus per-payout quotes is the point.
func (svc *SettlementService) QuoteBatch(ctx context.Context, tenantID, batchID string) (*model.SettlementBatch, error) {
	b, err := svc.s.Store.Settlements().Get(ctx, tenantID, batchID)
	if err != nil {
		return nil, err
	}
	if b.State != model.BatchOpen {
		return nil, httpx.ErrConflict
	}
	partner, err := svc.s.Store.Partners().Get(ctx, tenantID, b.PartnerID)
	if err != nil {
		return nil, err
	}
	q, err := svc.ml.Quote(ctx, partner.PayoutRef, b.AmountMinor, b.Currency)
	if err != nil {
		svc.s.Events.Emit(ctx, tenantID, "settlement_batch", b.ID, "settlement.quote_failed",
			map[string]any{"error": err.Error()})
		return nil, err
	}
	b.State = model.BatchQuoted
	b.QuoteID = q.QuoteID
	b.ReceiveMinor = q.ReceiveMinor
	b.FeesMinor = q.FeesMinor
	if exp, err := time.Parse(time.RFC3339, q.ExpiresAt); err == nil {
		b.QuoteExpiresAt = exp
	}
	b.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.Settlements().Update(ctx, b); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "settlement_batch", b.ID, "settlement.batch_quoted",
		map[string]any{"quote_id": q.QuoteID, "receive_minor": q.ReceiveMinor, "fees_minor": q.FeesMinor})
	return b, nil
}

// ExecuteBatch reserves every member line in-ledger (same idempotency keys
// as the single flow) and initiates ONE scheme transfer for the netted
// amount.
func (svc *SettlementService) ExecuteBatch(ctx context.Context, tenantID, batchID string) (*model.SettlementBatch, error) {
	b, err := svc.s.Store.Settlements().Get(ctx, tenantID, batchID)
	if err != nil {
		return nil, err
	}
	if b.State != model.BatchQuoted {
		return nil, httpx.ErrConflict
	}
	if !b.QuoteExpiresAt.IsZero() && svc.s.Now().After(b.QuoteExpiresAt) {
		return nil, fmt.Errorf("%w: quote expired — re-quote", httpx.ErrConflict)
	}
	lines, err := svc.s.Store.Payouts().ListByBatch(ctx, tenantID, batchID)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("%w: batch has no member payouts", httpx.ErrConflict)
	}
	// Reserve per line FIRST: if any reserve fails, already-reserved lines
	// keep their idem keys — a retry re-posts nothing (ledger is idempotent).
	for _, p := range lines {
		if err := svc.s.Payments.post(ctx, tenantID, p.BookingID,
			"PARTNER_PAYABLE", "IN_FLIGHT_SETTLEMENT", p.AmountMinor, p.Currency,
			"payout:reserve:"+p.ID); err != nil {
			return nil, err
		}
	}
	tr, err := svc.ml.Transfer(ctx, MLTransferRequest{
		QuoteID:        b.QuoteID,
		IdempotencyKey: "batch:" + b.ID,
		PayoutID:       b.ID,
		TenantID:       tenantID,
		CallbackURL:    svc.baseURL + "/v1/payouts/callbacks/mojaloop",
	})
	if err != nil {
		svc.s.Events.Emit(ctx, tenantID, "settlement_batch", b.ID, "settlement.transfer_failed",
			map[string]any{"error": err.Error()})
		b.State = model.BatchFailed
		b.UpdatedAt = svc.s.Now()
		_ = svc.s.Store.Settlements().Update(ctx, b)
		return nil, err
	}
	b.TransferID = tr.TransferID
	b.State = model.BatchInFlight
	b.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.Settlements().Update(ctx, b); err != nil {
		return nil, err
	}
	for _, p := range lines {
		p := p
		// Lines deliberately do NOT carry the transfer id: callback routing
		// tries the payout saga first and falls back to the batch saga, so a
		// batch transfer must match only the batch row.
		p.State = model.PayoutInFlight
		p.UpdatedAt = svc.s.Now()
		if err := svc.s.Store.Payouts().Update(ctx, &p); err != nil {
			return nil, err
		}
	}
	svc.s.Events.Emit(ctx, tenantID, "settlement_batch", b.ID, "settlement.batch_in_flight",
		map[string]any{"transfer_id": tr.TransferID, "lines": len(lines)})
	return b, nil
}

// HandleBatchCallback settles or compensates the whole batch from the
// scheme's transfer notification. The callback router tries single payouts
// first, then batches — the transfer id decides which saga owns it.
func (svc *SettlementService) HandleBatchCallback(ctx context.Context, tenantID, transferID, kind string) (*model.SettlementBatch, error) {
	b, err := svc.s.Store.Settlements().GetByTransfer(ctx, tenantID, transferID)
	if err != nil {
		return nil, err
	}
	if b.BatchTerminal() {
		return b, nil // absorbing: replay-safe
	}
	if b.State != model.BatchInFlight {
		return nil, httpx.ErrConflict
	}
	switch kind {
	case "transfer.fulfilled":
		return svc.settleBatch(ctx, b)
	case "transfer.aborted", "transfer.timeout":
		return svc.compensateBatch(ctx, b, kind)
	default:
		return nil, httpx.ErrValidation
	}
}

// settleBatch allocates the netted receive amount pro-rata across member
// lines by amount (remainder to the last line) and settles each with the
// SAME ledger keys the single flow would use.
func (svc *SettlementService) settleBatch(ctx context.Context, b *model.SettlementBatch) (*model.SettlementBatch, error) {
	lines, err := svc.s.Store.Payouts().ListByBatch(ctx, b.TenantID, b.ID)
	if err != nil {
		return nil, err
	}
	receive := b.ReceiveMinor
	if receive <= 0 {
		receive = b.AmountMinor
	}
	var allocated int64
	for i, p := range lines {
		lineReceive := receive * p.AmountMinor / b.AmountMinor
		if i == len(lines)-1 {
			lineReceive = receive - allocated // rounding remainder to the last line
		}
		allocated += lineReceive
		if err := svc.s.Payments.post(ctx, b.TenantID, p.BookingID,
			"IN_FLIGHT_SETTLEMENT", "PARTNER_CLEARING", lineReceive, p.Currency,
			"payout:fulfil:"+p.ID); err != nil {
			return nil, err
		}
		if diff := p.AmountMinor - lineReceive; diff > 0 {
			if err := svc.s.Payments.post(ctx, b.TenantID, p.BookingID,
				"IN_FLIGHT_SETTLEMENT", "FX_GAIN_LOSS", diff, p.Currency,
				"payout:fxdiff:"+p.ID); err != nil {
				return nil, err
			}
		}
		p := p
		p.State = model.PayoutSettled
		p.ReceiveMinor = lineReceive // pro-rata share of the netted receive
		p.SettledAt = svc.s.Now()
		p.UpdatedAt = svc.s.Now()
		if err := svc.s.Store.Payouts().Update(ctx, &p); err != nil {
			return nil, err
		}
	}
	b.State = model.BatchSettled
	b.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.Settlements().Update(ctx, b); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, b.TenantID, "settlement_batch", b.ID, "settlement.batch_settled",
		map[string]any{"lines": len(lines), "receive_minor": receive, "fees_minor": b.FeesMinor})
	return b, nil
}

func (svc *SettlementService) compensateBatch(ctx context.Context, b *model.SettlementBatch, reason string) (*model.SettlementBatch, error) {
	lines, err := svc.s.Store.Payouts().ListByBatch(ctx, b.TenantID, b.ID)
	if err != nil {
		return nil, err
	}
	for _, p := range lines {
		if err := svc.s.Payments.post(ctx, b.TenantID, p.BookingID,
			"IN_FLIGHT_SETTLEMENT", "PARTNER_PAYABLE", p.AmountMinor, p.Currency,
			"payout:compensate:"+p.ID); err != nil {
			return nil, err
		}
		p := p
		p.State = model.PayoutCompensated
		p.UpdatedAt = svc.s.Now()
		if err := svc.s.Store.Payouts().Update(ctx, &p); err != nil {
			return nil, err
		}
	}
	b.State = model.BatchCompensated
	b.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.Settlements().Update(ctx, b); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, b.TenantID, "settlement_batch", b.ID, "settlement.batch_compensated",
		map[string]any{"reason": reason, "lines": len(lines)})
	// Whole-batch compensation always pages a human.
	_, _ = svc.s.Handoffs.Open(ctx, b.TenantID, "", "POLICY",
		fmt.Sprintf("settlement batch %s compensated (%s): %d partner payables restored, ops follow-up required", b.ID, reason, len(lines)), nil)
	return b, nil
}

// SweepBatchTimeouts compensates IN_FLIGHT batches that have outlived the
// settlement timeout without a callback. Same recovery story as single
// payouts: dropped notifications must never strand partner funds.
func (svc *SettlementService) SweepBatchTimeouts(ctx context.Context, tenantID string) (int, error) {
	batches, err := svc.s.Store.Settlements().List(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	cutoff := svc.s.Now().Add(-PayoutSettlementTimeoutHours * time.Hour)
	n := 0
	for _, b := range batches {
		if b.State != model.BatchInFlight || b.UpdatedAt.After(cutoff) {
			continue
		}
		b := b
		if _, err := svc.compensateBatch(ctx, &b, "settlement timeout"); err != nil {
			svc.s.Events.Emit(ctx, tenantID, "settlement_batch", b.ID, "settlement.sweep_failed",
				map[string]any{"error": err.Error()})
			continue
		}
		n++
	}
	return n, nil
}

func (svc *SettlementService) GetBatch(ctx context.Context, tenantID, id string) (*model.SettlementBatch, error) {
	return svc.s.Store.Settlements().Get(ctx, tenantID, id)
}

func (svc *SettlementService) ListBatches(ctx context.Context, tenantID string) ([]model.SettlementBatch, error) {
	return svc.s.Store.Settlements().List(ctx, tenantID)
}

// SettlementLine is one currency bucket of a partner's settlement
// statement. Currencies are NEVER blended (Phase 40 lesson).
type SettlementLine struct {
	Currency            string `json:"currency"`
	Payouts             int    `json:"payouts"`
	PendingMinor        int64  `json:"pending_minor"`
	InFlightMinor       int64  `json:"in_flight_minor"`
	SettledMinor        int64  `json:"settled_minor"`         // gross settled (amount out the door)
	SettledReceiveMinor int64  `json:"settled_receive_minor"` // what partners actually received
	CompensatedMinor    int64  `json:"compensated_minor"`
	FeesMinor           int64  `json:"fees_minor"`
}

// SettlementStatement is the reconciliation surface for a partner: all
// payouts plus all batches, bucketed per currency.
type SettlementStatement struct {
	PartnerID  string                  `json:"partner_id"`
	Currencies []SettlementLine        `json:"currencies"`
	Batches    []model.SettlementBatch `json:"batches"`
}

// Statement builds the partner-facing settlement statement, grouped by
// currency — the reconciliation answer to "what did we owe, what did we
// pay, what did the scheme take".
func (svc *SettlementService) Statement(ctx context.Context, tenantID, partnerID string) (*SettlementStatement, error) {
	if _, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID); err != nil {
		return nil, err
	}
	payouts, err := svc.s.Store.Payouts().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	byCur := map[string]*SettlementLine{}
	var order []string
	for _, p := range payouts {
		if p.PartnerID != partnerID {
			continue
		}
		line, ok := byCur[p.Currency]
		if !ok {
			line = &SettlementLine{Currency: p.Currency}
			byCur[p.Currency] = line
			order = append(order, p.Currency)
		}
		line.Payouts++
		switch p.State {
		case model.PayoutPending, model.PayoutQuoted:
			line.PendingMinor += p.AmountMinor
		case model.PayoutInFlight:
			line.InFlightMinor += p.AmountMinor
		case model.PayoutSettled:
			line.SettledMinor += p.AmountMinor
			line.SettledReceiveMinor += p.ReceiveMinor
		case model.PayoutCompensated:
			line.CompensatedMinor += p.AmountMinor
		}
		line.FeesMinor += p.FeesMinor
	}
	st := &SettlementStatement{PartnerID: partnerID}
	for _, cur := range order {
		st.Currencies = append(st.Currencies, *byCur[cur])
	}
	batches, err := svc.s.Store.Settlements().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for _, b := range batches {
		if b.PartnerID == partnerID {
			st.Batches = append(st.Batches, b)
		}
	}
	return st, nil
}
