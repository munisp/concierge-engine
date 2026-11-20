package domain

// Phase 66: partner bill-to-desk folios — monthly consolidated settlement
// for sourced demand. A hotel desk that books rides for its guests opts
// into desk billing; at confirm time each sourced booking's adjusted gross
// (the quote total AFTER Phase 65 rate-card repricing) accrues as a line
// on the partner's OPEN folio for the booking month and currency. At
// period end the operator issues the folio (OPEN → ISSUED, total frozen);
// settlement (ISSUED → SETTLED) posts the same net/fee ledger split as a
// card capture, because money truth must not depend on the collection
// rail. Sourcing commission (Phase 63) accrues independently — the
// partner still earns its channel rate on the same adjusted gross.

import (
	"context"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type FolioService struct{ s *Services }

// SetBillToDesk toggles desk billing for a partner (console). Idempotent.
func (svc *FolioService) SetBillToDesk(ctx context.Context, tenantID, partnerID string, enabled bool) (*model.Partner, error) {
	p, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	if p.BillToDesk == enabled {
		return p, nil
	}
	p.BillToDesk = enabled
	if err := svc.s.Store.Partners().Update(ctx, p); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "partner", p.ID, "channel.bill_to_desk_set",
		map[string]any{"partner_id": p.ID, "enabled": enabled})
	svc.s.Audit.Log(ctx, tenantID, "operator", "channel.bill_to_desk_set", "partner", p.ID,
		map[string]any{"enabled": enabled})
	return p, nil
}

// accrueOnConfirm appends the confirmed booking's adjusted gross to the
// sourcing partner's OPEN folio. Runs inside Confirm; failure is
// audit-logged, never blocks the booking. Direct demand and partners
// without desk billing accrue nothing. Idempotent per booking at the
// store layer — a retried confirm bills once.
func (svc *FolioService) accrueOnConfirm(ctx context.Context, tenantID string, b *model.Booking) {
	if b.SourcePartnerID == "" || b.SourceKind == "" || b.SourceKind == model.SourceDirect {
		return
	}
	p, err := svc.s.Store.Partners().Get(ctx, tenantID, b.SourcePartnerID)
	if err != nil || !p.BillToDesk {
		return
	}
	q, err := svc.s.Store.Quotes().Get(ctx, tenantID, b.QuoteID)
	if err != nil {
		return
	}
	var guest string
	if r, err := svc.s.Store.Requests().Get(ctx, tenantID, b.RequestID); err == nil {
		if c, err := svc.s.Store.Clients().Get(ctx, tenantID, r.ClientID); err == nil {
			guest = c.FullName
		}
	}
	now := svc.s.Now()
	f := &model.Folio{
		ID: NewID(), TenantID: tenantID, PartnerID: p.ID,
		Period: model.FolioPeriod(b.CreatedAt), Currency: q.Currency,
		State: model.FolioOpen, CreatedAt: now,
	}
	line := model.FolioLine{
		BookingID: b.ID, QuoteID: q.ID, GuestName: guest,
		AmountMinor: q.TotalMinor, CreatedAt: now,
	}
	created, err := svc.s.Store.Folios().AccrueLine(ctx, f, line)
	if err != nil {
		if err == httpx.ErrConflict {
			// The period folio is already ISSUED — the booking is real and
			// must not vanish. Surface it loudly for manual handling; do NOT
			// silently drop or silently reopen a billed statement.
			svc.s.Events.Emit(ctx, tenantID, "booking", b.ID, "channel.folio_accrual_blocked",
				map[string]any{"partner_id": p.ID, "period": f.Period, "currency": q.Currency,
					"reason": "folio already issued — bill this booking manually"})
			svc.s.Audit.Log(ctx, tenantID, "system", "channel.folio_accrual_blocked", "partner", p.ID,
				map[string]any{"booking_id": b.ID, "period": f.Period})
			return
		}
		svc.s.Audit.Log(ctx, tenantID, "system", "channel.folio_accrual_failed", "partner", p.ID,
			map[string]any{"booking_id": b.ID, "error": err.Error()})
		return
	}
	svc.s.Events.Emit(ctx, tenantID, "booking", b.ID, "channel.folio_line_accrued",
		map[string]any{"partner_id": p.ID, "period": f.Period, "currency": q.Currency,
			"amount_minor": q.TotalMinor, "folio_created": created})
}

// Issue closes the OPEN folio for a partner/period/currency: the total is
// frozen and the folio becomes payable. Idempotent — re-issuing returns
// the folio unchanged; issuing a period with no OPEN folio is a 404 (an
// ISSUED/SETTLED folio is returned as-is so a replayed call converges).
func (svc *FolioService) Issue(ctx context.Context, tenantID, partnerID, period, currency string) (*model.Folio, error) {
	if _, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID); err != nil {
		return nil, err
	}
	period = strings.TrimSpace(period)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if len(period) != 7 || period[4] != '-' || currency == "" {
		return nil, httpx.ErrValidation
	}
	folios, err := svc.s.Store.Folios().ListForPartner(ctx, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	for i := range folios {
		f := &folios[i]
		if f.Period != period || f.Currency != currency {
			continue
		}
		if f.State == model.FolioOpen {
			f.State = model.FolioIssued
			f.IssuedAt = svc.s.Now()
			if err := svc.s.Store.Folios().Update(ctx, f); err != nil {
				return nil, err
			}
			svc.s.Events.Emit(ctx, tenantID, "partner", partnerID, "channel.folio_issued",
				map[string]any{"folio_id": f.ID, "period": period, "currency": currency,
					"total_minor": f.TotalMinor, "lines": len(f.Lines)})
			svc.s.Audit.Log(ctx, tenantID, "operator", "channel.folio_issued", "partner", partnerID,
				map[string]any{"folio_id": f.ID, "total_minor": f.TotalMinor, "currency": currency})
		}
		return f, nil
	}
	return nil, httpx.ErrNotFound
}

// Settle marks an ISSUED folio paid and posts the money truth: the
// partner's payment clears into tenant revenue net of the platform fee —
// the same split as a card capture, keyed folio:{id} so a retry can never
// double-post. SETTLED is terminal and idempotent.
//
// Phase 70: after partial payments, Settle records only the OUTSTANDING
// remainder as the final payment (method "settle") and posts only that
// remainder's split — both collection paths converge on a folio whose
// payment ledger always sums to the frozen total.
func (svc *FolioService) Settle(ctx context.Context, tenantID, folioID string) (*model.Folio, error) {
	f, err := svc.s.Store.Folios().Get(ctx, tenantID, folioID)
	if err != nil {
		return nil, err
	}
	if f.State == model.FolioSettled {
		return f, nil
	}
	if f.State != model.FolioIssued {
		return nil, httpx.ErrConflict // only an issued folio can settle
	}
	tenant, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	outstanding := f.OutstandingMinor()
	if outstanding > 0 {
		fee := PlatformFeeMinor(tenant.PricingRules, outstanding)
		net := outstanding - fee
		if err := svc.s.Payments.post(ctx, tenantID, "",
			"PARTNER_CLEARING", "TENANT_REVENUE", net, f.Currency, "folio:net:"+f.ID); err != nil {
			return nil, err
		}
		if err := svc.s.Payments.post(ctx, tenantID, "",
			"PARTNER_CLEARING", "PLATFORM_FEES", fee, f.Currency, "folio:fee:"+f.ID); err != nil {
			return nil, err
		}
		f.Payments = append(f.Payments, model.FolioPayment{
			ID: NewID(), AmountMinor: outstanding, Method: "settle",
			ReceivedAt: svc.s.Now(), RecordedBy: "operator",
		})
	}
	f.State = model.FolioSettled
	f.SettledAt = svc.s.Now()
	if err := svc.s.Store.Folios().Update(ctx, f); err != nil {
		return nil, err
	}
	svc.emitSettled(ctx, tenantID, tenant.PricingRules, f)
	return f, nil
}

// List returns the partner's folios, newest period first.
func (svc *FolioService) List(ctx context.Context, tenantID, partnerID string) ([]model.Folio, error) {
	if _, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID); err != nil {
		return nil, err
	}
	return svc.s.Store.Folios().ListForPartner(ctx, tenantID, partnerID)
}

// Get returns one folio with its lines — the operator statement view.
func (svc *FolioService) Get(ctx context.Context, tenantID, folioID string) (*model.Folio, error) {
	return svc.s.Store.Folios().Get(ctx, tenantID, folioID)
}
