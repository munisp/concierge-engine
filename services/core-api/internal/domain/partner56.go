// Phase 56: partner integration layer. Tour operators and DMCs (Mr Travel,
// The Little Orange, Younique Tailor Tours) integrate programmatically:
// partner-scoped API keys authenticate the /v1/partner/* surface, partners
// accept or decline offered assignments, and commissions on partner-routed
// confirmed bookings accrue into the existing payout saga (Phase 2/48) —
// no separate money path to reconcile.
package domain

import (
	"context"
	"math"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// MaxPartnerCommissionPct bounds the partner commission rate — above 50%
// the "partner" is reselling, not supplying, and the tenant should say so.
const MaxPartnerCommissionPct = 50.0

// PartnerCommissionKey is the idempotency key for one booking's commission
// to one partner — re-assigns and retries never double-accrue.
func PartnerCommissionKey(bookingID, partnerID string) string {
	return "commission:" + bookingID + ":" + partnerID
}

type PartnerOpsService struct{ s *Services }

// PartnerAssignmentView is the partner-facing assignment record — internal
// fields (tenant SLA internals, own-fleet resource ids) stay server-side.
type PartnerAssignmentView struct {
	ID        string `json:"id"`
	BookingID string `json:"booking_id"`
	Status    string `json:"status"`
	StartAt   string `json:"start_at,omitempty"`
	EndAt     string `json:"end_at,omitempty"`
	CreatedAt string `json:"created_at"`
	// Phase 58: the client's operational preferences (music, temperature,
	// dietary, mobility, language…), consent-filtered — "private"-scoped
	// preferences never leave the tenant. Absent when none set.
	Preferences map[string]string `json:"preferences,omitempty"`
}

// AssignmentsFor lists one partner's assignments, newest last (store order).
func (svc *PartnerOpsService) AssignmentsFor(ctx context.Context, tenantID, partnerID string) ([]PartnerAssignmentView, error) {
	if _, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID); err != nil {
		return nil, err
	}
	list, err := svc.s.Store.Dispatch().ListAssignments(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]PartnerAssignmentView, 0, len(list))
	prefCache := map[string]map[string]string{} // clientID → consent-filtered prefs
	for _, a := range list {
		if a.PartnerID != partnerID {
			continue
		}
		v := PartnerAssignmentView{
			ID: a.ID, BookingID: a.BookingID, Status: a.Status,
			CreatedAt: a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if !a.StartAt.IsZero() {
			v.StartAt = a.StartAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		if !a.EndAt.IsZero() {
			v.EndAt = a.EndAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		// Phase 58: attach operational preferences (booking → request →
		// client, memoized per client). Best-effort — a failed join never
		// hides the assignment.
		if cid := svc.bookingClientID(ctx, tenantID, a.BookingID); cid != "" {
			if prefs, ok := prefCache[cid]; ok {
				v.Preferences = prefs
			} else if prefs, err := svc.s.Clients.PartnerPreferenceMap(ctx, tenantID, cid); err == nil {
				prefCache[cid] = prefs
				v.Preferences = prefs
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// bookingClientID resolves booking → request → client ("" on any miss).
func (svc *PartnerOpsService) bookingClientID(ctx context.Context, tenantID, bookingID string) string {
	b, err := svc.s.Store.Bookings().Get(ctx, tenantID, bookingID)
	if err != nil {
		return ""
	}
	sr, err := svc.s.Store.Requests().Get(ctx, tenantID, b.RequestID)
	if err != nil {
		return ""
	}
	return sr.ClientID
}

// BookingPreferenceMap exposes the consent-filtered preference map for a
// booking's client — used for the webhook offer payload at assign time.
func (svc *PartnerOpsService) BookingPreferenceMap(ctx context.Context, tenantID, bookingID string) map[string]string {
	cid := svc.bookingClientID(ctx, tenantID, bookingID)
	if cid == "" {
		return nil
	}
	prefs, err := svc.s.Clients.PartnerPreferenceMap(ctx, tenantID, cid)
	if err != nil {
		return nil
	}
	return prefs
}

// Respond records a partner's accept/decline of an offered assignment. The
// response rides the standard trip state machine (ASSIGNED →
// PARTNER_ACCEPTED | PARTNER_DECLINED) so every existing consumer — console,
// SSE, conflict gating — sees it without new wiring. A decline cancels the
// still-pending commission payout; an accept leaves it to settle.
func (svc *PartnerOpsService) Respond(ctx context.Context, tenantID, partnerID, assignmentID, action, note string) (*model.Assignment, error) {
	var target string
	switch action {
	case "accept":
		target = "PARTNER_ACCEPTED"
	case "decline":
		target = "PARTNER_DECLINED"
	default:
		return nil, httpx.ErrValidation
	}
	list, err := svc.s.Store.Dispatch().ListAssignments(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var cur *model.Assignment
	for i := range list {
		if list[i].ID == assignmentID {
			cur = &list[i]
			break
		}
	}
	if cur == nil {
		return nil, httpx.ErrNotFound
	}
	if cur.PartnerID != partnerID {
		// Deliberately indistinguishable from unknown — a partner never
		// learns whether an assignment ID exists for another partner.
		return nil, httpx.ErrNotFound
	}
	if cur.Status != "ASSIGNED" {
		return nil, httpx.ErrConflict
	}
	if err := svc.s.Dispatch.UpdateStatus(ctx, tenantID, assignmentID, target); err != nil {
		return nil, err
	}
	if action == "decline" {
		svc.cancelCommission(ctx, tenantID, cur.BookingID, partnerID)
	}
	svc.s.Events.Emit(ctx, tenantID, "assignment", assignmentID, "assignment.partner_responded",
		map[string]any{"partner_id": partnerID, "booking_id": cur.BookingID,
			"action": action, "note": note})
	svc.s.Audit.Log(ctx, tenantID, "partner", "partner.responded", "partner", partnerID,
		map[string]any{"assignment_id": assignmentID, "action": action})
	cur.Status = target
	return cur, nil
}

// AccrueCommission books the partner's commission for a confirmed booking
// as a PENDING payout on the partner's rail (MOJALOOP batches via Phase 48;
// MANUAL settles by statement). Idempotent per booking+partner. Partners
// without a commission rate accrue nothing — their payouts are quoted
// through the existing manual flow.
func (svc *PartnerOpsService) AccrueCommission(ctx context.Context, tenantID, bookingID, partnerID string) error {
	p, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID)
	if err != nil {
		return err
	}
	if p.CommissionPct <= 0 {
		return nil
	}
	b, err := svc.s.Store.Bookings().Get(ctx, tenantID, bookingID)
	if err != nil {
		return err
	}
	var totalMinor int64
	var currency string
	if b.QuoteID != "" {
		if q, err := svc.s.Store.Quotes().Get(ctx, tenantID, b.QuoteID); err == nil {
			totalMinor, currency = q.TotalMinor, q.Currency
		}
	}
	amount := int64(math.Round(float64(totalMinor) * p.CommissionPct / 100))
	if amount <= 0 {
		return nil // nothing to accrue on a zero/unknown total — no phantom zeros
	}
	rail := p.PayoutRail
	if rail == "" {
		rail = "MANUAL"
	}
	po := &model.Payout{
		ID: NewID(), TenantID: tenantID, PartnerID: partnerID,
		BookingID: bookingID, QuoteID: b.QuoteID,
		AmountMinor: amount, Currency: currency, Rail: rail,
		State:         model.PayoutPending,
		IdempotencyKey: PartnerCommissionKey(bookingID, partnerID),
		CreatedAt:     svc.s.Now(),
	}
	if err := svc.s.Store.Payouts().Create(ctx, po); err != nil {
		return err
	}
	svc.s.Events.Emit(ctx, tenantID, "payout", po.ID, "partner.commission_accrued",
		map[string]any{"partner_id": partnerID, "booking_id": bookingID,
			"amount_minor": amount, "currency": currency, "pct": p.CommissionPct})
	return nil
}

// cancelCommission voids a still-pending commission when the partner
// declines; once quoted/in-flight it is left for ops to compensate — the
// saga never silently rewrites money in flight.
func (svc *PartnerOpsService) cancelCommission(ctx context.Context, tenantID, bookingID, partnerID string) {
	list, err := svc.s.Store.Payouts().List(ctx, tenantID)
	if err != nil {
		return
	}
	for i := range list {
		po := &list[i]
		if po.PartnerID != partnerID || po.BookingID != bookingID || po.State != model.PayoutPending {
			continue
		}
		po.State = model.PayoutFailed // declined before any money moved
		if err := svc.s.Store.Payouts().Update(ctx, po); err == nil {
			svc.s.Events.Emit(ctx, tenantID, "payout", po.ID, "partner.commission_cancelled",
				map[string]any{"partner_id": partnerID, "booking_id": bookingID,
					"amount_minor": po.AmountMinor, "currency": po.Currency})
		}
	}
}

// PartnerSettlement is the partner-facing money view: accrued commission
// payouts grouped by saga state, plus minor-unit totals.
type PartnerSettlement struct {
	PartnerID     string         `json:"partner_id"`
	CommissionPct float64        `json:"commission_pct"`
	PayoutRail    string         `json:"payout_rail"`
	Totals        map[string]int64 `json:"totals"` // state → minor units
	Payouts       []model.Payout `json:"payouts"`
}

// Settlement summarizes a partner's commission payouts by saga state.
func (svc *PartnerOpsService) Settlement(ctx context.Context, tenantID, partnerID string) (*PartnerSettlement, error) {
	p, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	list, err := svc.s.Store.Payouts().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	res := &PartnerSettlement{
		PartnerID: partnerID, CommissionPct: p.CommissionPct,
		PayoutRail: p.PayoutRail, Totals: map[string]int64{},
		Payouts: []model.Payout{},
	}
	for _, po := range list {
		if po.PartnerID != partnerID {
			continue
		}
		res.Totals[po.State] += po.AmountMinor
		res.Payouts = append(res.Payouts, po)
	}
	return res, nil
}

// SetCommissionPct updates a partner's commission rate (0..50).
func (svc *PartnerOpsService) SetCommissionPct(ctx context.Context, tenantID, partnerID string, pct float64) (*model.Partner, error) {
	if pct < 0 || pct > MaxPartnerCommissionPct || math.IsNaN(pct) || math.IsInf(pct, 0) {
		return nil, httpx.ErrValidation
	}
	p, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	p.CommissionPct = pct
	if err := svc.s.Store.Partners().Update(ctx, p); err != nil {
		return nil, err
	}
	svc.s.Audit.Log(ctx, tenantID, "partner", "partner.commission_set", "partner", partnerID,
		map[string]any{"pct": pct})
	return p, nil
}
