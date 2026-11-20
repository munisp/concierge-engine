// Phase 63: demand channels. Every request now carries WHERE the demand
// came from — a direct client (zero commission, the most lucrative
// channel), a hotel concierge desk, a travel agency, another transport
// company farming overflow in, or a tour operator. Sourced demand enters
// through the partner surface (book-on-behalf) and flows through the
// standard quoting → booking → dispatch pipeline unchanged; the source
// travels with the request onto the booking so commission accrual and
// margin analytics attribute every confirmed booking to its channel.
package domain

import (
	"context"
	"math"
	"sort"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type ChannelService struct{ s *Services }

// SourcingCommissionKey idempotently keys one booking's sourcing
// commission to one partner — distinct from the fulfilment key
// (PartnerCommissionKey) so a partner who both sources AND fulfils a
// booking is commission-paid exactly once (see AccrueCommission's skip).
func SourcingCommissionKey(bookingID, partnerID string) string {
	return "src-commission:" + bookingID + ":" + partnerID
}

// SourcedRequestInput is the partner-surface book-on-behalf payload: the
// guest's identity plus the trip items. The partner's own key supplies
// tenant + sourcing partner; the platform resolves (or creates) the guest
// client through the same progressive-registration path as any capture.
type SourcedRequestInput struct {
	GuestName      string              `json:"guest_name"`
	GuestPhone     string              `json:"guest_phone"`
	GuestEmail     string              `json:"guest_email"`
	GuestLocale    string              `json:"guest_locale,omitempty"`
	Items          []model.ServiceItem `json:"items"`
	IdempotencyKey string              `json:"idempotency_key"`
}

// SourcedRequestView is what the sourcing partner sees of the demand they
// brought: state and timestamps, never the tenant's internal notes.
type SourcedRequestView struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	GuestName string `json:"guest_name,omitempty"`
	Items     int    `json:"items"`
	CreatedAt string `json:"created_at"`
}

// SetPartnerKind reclassifies a partner's demand-channel kind (console).
func (svc *ChannelService) SetPartnerKind(ctx context.Context, tenantID, partnerID, kind string) (*model.Partner, error) {
	if !model.ValidPartnerKind(kind) {
		return nil, httpx.ErrValidation
	}
	p, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	if p.Kind == kind {
		return p, nil // idempotent
	}
	p.Kind = kind
	if err := svc.s.Store.Partners().Update(ctx, p); err != nil {
		return nil, err
	}
	svc.s.Audit.Log(ctx, tenantID, "partner", "partner.kind_set", "partner", partnerID,
		map[string]any{"kind": kind})
	return p, nil
}

// CreateSourcedRequest is the book-on-behalf workflow: a hotel desk,
// travel agency or transport partner creates a request for their guest.
// The request enters the standard pipeline (state machine, instant quote
// engine, SLA timers) — sourcing only attributes it. The partner must not
// be SUSPENDED; a PENDING partner's demand is accepted (vetting gates
// fulfilment, not demand) but flagged in the audit trail.
func (svc *ChannelService) CreateSourcedRequest(ctx context.Context, tenantID, partnerID string, in *SourcedRequestInput) (*model.ServiceRequest, error) {
	if in == nil || len(in.Items) == 0 || in.IdempotencyKey == "" {
		return nil, httpx.ErrValidation
	}
	if in.GuestPhone == "" && in.GuestEmail == "" {
		return nil, httpx.ErrValidation // a guest we cannot reach is not demand
	}
	p, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	if p.Status == "SUSPENDED" {
		return nil, httpx.ErrForbidden
	}
	kind := p.Kind
	if kind == "" {
		kind = model.PartnerKindTourOperator // legacy partner rows
	}
	res, err := svc.s.Clients.ResolveV2(ctx, tenantID, ResolveInput{
		Phone: in.GuestPhone, Email: in.GuestEmail, Name: in.GuestName, Locale: in.GuestLocale,
	})
	if err != nil {
		return nil, err
	}
	r := &model.ServiceRequest{
		TenantID:        tenantID,
		ClientID:        res.Client.ID,
		Items:           in.Items,
		SourceKind:      kind,
		SourcePartnerID: partnerID,
		IdempotencyKey:  "src:" + partnerID + ":" + in.IdempotencyKey,
	}
	created, err := svc.s.Booking.CreateRequest(ctx, r)
	if err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "service_request", created.ID, "channel.request_sourced",
		map[string]any{"partner_id": partnerID, "source_kind": kind, "client_id": res.Client.ID, "new_client": res.IsNew})
	svc.s.Audit.Log(ctx, tenantID, "partner", "channel.request_sourced", "service_request", created.ID,
		map[string]any{"partner_id": partnerID, "source_kind": kind, "partner_status": p.Status})
	return created, nil
}

// SourcedRequests lists the demand one partner has brought, newest first —
// the partner portal's "my guests" view.
func (svc *ChannelService) SourcedRequests(ctx context.Context, tenantID, partnerID string, limit int) ([]SourcedRequestView, error) {
	if _, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID); err != nil {
		return nil, err
	}
	list, err := svc.s.Store.Requests().ForSource(ctx, tenantID, partnerID, limit)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	out := make([]SourcedRequestView, 0, len(list))
	for _, r := range list {
		v := SourcedRequestView{
			ID: r.ID, State: string(r.State), Items: len(r.Items),
			CreatedAt: r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if name, ok := names[r.ClientID]; ok {
			v.GuestName = name
		} else if c, err := svc.s.Store.Clients().Get(ctx, tenantID, r.ClientID); err == nil {
			names[r.ClientID] = c.FullName
			v.GuestName = c.FullName
		}
		out = append(out, v)
	}
	return out, nil
}

// AccrueSourcingCommission pays the channel for the demand it brought: a
// PENDING payout on the sourcing partner's rail at confirm time, at the
// partner's commission rate. Failure never blocks the booking — it is
// audit-logged like fulfilment commission (Phase 56).
func (svc *ChannelService) AccrueSourcingCommission(ctx context.Context, tenantID string, b *model.Booking) {
	if b.SourcePartnerID == "" || b.SourceKind == "" || b.SourceKind == model.SourceDirect {
		return
	}
	p, err := svc.s.Store.Partners().Get(ctx, tenantID, b.SourcePartnerID)
	if err != nil || p.CommissionPct <= 0 {
		return
	}
	q, err := svc.s.Store.Quotes().Get(ctx, tenantID, b.QuoteID)
	if err != nil {
		return
	}
	amount := int64(math.Round(float64(q.TotalMinor) * p.CommissionPct / 100))
	if amount <= 0 {
		return
	}
	rail := p.PayoutRail
	if rail == "" {
		rail = "MANUAL"
	}
	po := &model.Payout{
		ID: NewID(), TenantID: tenantID, PartnerID: p.ID,
		BookingID: b.ID, QuoteID: b.QuoteID,
		AmountMinor: amount, Currency: q.Currency, Rail: rail,
		State:          model.PayoutPending,
		IdempotencyKey: SourcingCommissionKey(b.ID, p.ID),
		CreatedAt:      svc.s.Now(),
	}
	if err := svc.s.Store.Payouts().Create(ctx, po); err != nil {
		svc.s.Audit.Log(ctx, tenantID, "partner", "channel.sourcing_commission_failed", "partner", p.ID,
			map[string]any{"booking_id": b.ID, "error": err.Error()})
		return
	}
	svc.s.Events.Emit(ctx, tenantID, "payout", po.ID, "channel.sourcing_commission_accrued",
		map[string]any{"partner_id": p.ID, "booking_id": b.ID, "source_kind": b.SourceKind,
			"amount_minor": amount, "currency": q.Currency, "pct": p.CommissionPct})
}

// sourcingPaid reports whether a sourcing commission payout already exists
// for this booking+partner — fulfilment accrual skips those partners so a
// source-and-fulfil partner is never paid the same rate twice on one
// booking.
func (svc *ChannelService) sourcingPaid(ctx context.Context, tenantID, bookingID, partnerID string) bool {
	list, err := svc.s.Store.Payouts().List(ctx, tenantID)
	if err != nil {
		return false
	}
	key := SourcingCommissionKey(bookingID, partnerID)
	for _, po := range list {
		if po.IdempotencyKey == key && po.State != model.PayoutFailed && po.State != model.PayoutCompensated {
			return true
		}
	}
	return false
}

// SourceReport aggregates the tenant's confirmed bookings over the window
// by demand channel: gross captured value, accrued commission (sourcing +
// fulfilment, by booking), net. Per-currency rows only — mixed currencies
// are never blended into a ratio (Phase 40 honesty). Rows sort by gross
// descending; the note names the direct channel's advantage explicitly.
func (svc *ChannelService) SourceReport(ctx context.Context, tenantID string, days int) (*model.SourceReport, error) {
	if days <= 0 {
		days = 30
	}
	from := svc.s.Now().AddDate(0, 0, -days)
	bookings, err := svc.s.Store.Bookings().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	payouts, err := svc.s.Store.Payouts().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	// commission per booking (any non-failed payout line tied to a booking)
	commissionByBooking := map[string]int64{}
	for _, po := range payouts {
		if po.BookingID == "" || po.State == model.PayoutFailed || po.State == model.PayoutCompensated {
			continue
		}
		commissionByBooking[po.BookingID] += po.AmountMinor
	}
	type acc struct {
		row        *model.SourceReportRow
		currencies map[string]bool
	}
	accs := map[string]*acc{}
	order := []string{}
	for _, b := range bookings {
		if b.CreatedAt.Before(from) {
			continue
		}
		kind := b.SourceKind
		if kind == "" {
			kind = model.SourceDirect
		}
		a, ok := accs[kind]
		if !ok {
			a = &acc{row: &model.SourceReportRow{SourceKind: kind}, currencies: map[string]bool{}}
			accs[kind] = a
			order = append(order, kind)
		}
		a.row.Bookings++
		if q, err := svc.s.Store.Quotes().Get(ctx, tenantID, b.QuoteID); err == nil {
			a.row.GrossMinor += q.TotalMinor
			a.currencies[q.Currency] = true
		}
		a.row.CommissionMinor += commissionByBooking[b.ID]
	}
	rep := &model.SourceReport{Days: days, Rows: []model.SourceReportRow{},
		Note: "direct demand carries no channel commission — every sourced booking's margin is reduced by its channel's payout"}
	for _, kind := range order {
		a := accs[kind]
		if len(a.currencies) == 1 {
			for c := range a.currencies {
				a.row.Currency = c
			}
			a.row.NetMinor = a.row.GrossMinor - a.row.CommissionMinor
		} else if len(a.currencies) > 1 {
			a.row.MixedCurrency = true
			a.row.GrossMinor, a.row.CommissionMinor, a.row.NetMinor = 0, 0, 0 // never blended
		}
		rep.Rows = append(rep.Rows, *a.row)
	}
	sort.Slice(rep.Rows, func(i, j int) bool {
		if rep.Rows[i].MixedCurrency != rep.Rows[j].MixedCurrency {
			return !rep.Rows[i].MixedCurrency // computable rows first
		}
		if rep.Rows[i].GrossMinor != rep.Rows[j].GrossMinor {
			return rep.Rows[i].GrossMinor > rep.Rows[j].GrossMinor
		}
		return rep.Rows[i].SourceKind < rep.Rows[j].SourceKind
	})
	return rep, nil
}
