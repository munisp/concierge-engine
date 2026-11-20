// Phase 64: channel engagement closure. Phase 63 made the source of demand
// first-class; this phase closes the two loops the source opens:
//
//  1. Notifications — when a partner-sourced booking confirms, the GUEST
//     hears it from the platform (service lane, naming the arranger) and
//     the SOURCING PARTNER gets a WhatsApp status notice pointing at their
//     portal. Both degrade honestly: no contact channel → skipped with
//     the reason, never a fake "sent".
//  2. Operator reattribution — demand that arrives by email or phone but
//     was really partner-referred (a hotel desk emailing a booking) can be
//     attributed after the fact; the source travels to the booking and an
//     unaccrued sourcing commission accrues then. Attribution is audit-
//     logged; already-accrued money is never silently rewritten.
package domain

import (
	"context"
	"fmt"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// notifySourcedConfirmedDetached fires the confirm notifications without
// holding the booking response hostage to channel latency (mirrors the
// Phase 57 offer-notify pattern).
func (svc *ChannelService) notifySourcedConfirmedDetached(ctx context.Context, tenantID string, b *model.Booking) {
	if b == nil || b.SourcePartnerID == "" || b.SourceKind == model.SourceDirect {
		return
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	go func() {
		defer cancel()
		svc.NotifySourcedConfirmed(bg, tenantID, b)
	}()
}

// NotifySourcedConfirmed runs at booking confirm for sourced bookings. It
// never blocks the confirm path and never panics out of it — channel
// latency and adapter blips are recorded, not propagated.
func (svc *ChannelService) NotifySourcedConfirmed(ctx context.Context, tenantID string, b *model.Booking) {
	if b == nil || b.SourcePartnerID == "" || b.SourceKind == "" || b.SourceKind == model.SourceDirect {
		return
	}
	defer func() { _ = recover() }()
	p, err := svc.s.Store.Partners().Get(ctx, tenantID, b.SourcePartnerID)
	if err != nil {
		return
	}
	r, err := svc.s.Store.Requests().Get(ctx, tenantID, b.RequestID)
	if err != nil || r.ClientID == "" {
		return
	}

	// Guest first: the arranger is named so the message reads as service,
	// not marketing. Channel by contact: WhatsApp when a phone exists,
	// email otherwise; neither → the send records its own skip reason.
	guestBody := fmt.Sprintf("Your booking with us is confirmed — arranged for you by %s. "+
		"Your chauffeur details and live tracking will follow here before pickup.", p.Name)
	in := SendOutreachInput{
		ClientID: r.ClientID, Purpose: model.OutreachService, Body: guestBody,
		IdempotencyKey: "src-confirm:" + b.ID,
	}
	if c, err := svc.s.Store.Clients().Get(ctx, tenantID, r.ClientID); err == nil {
		if c.Phone == "" && c.Email != "" {
			in.Channel = model.OutreachChannelEmail
			in.Subject = "Your booking is confirmed"
		}
	}
	o, serr := svc.s.Outreach.Send(ctx, tenantID, in)
	sent := "failed"
	if serr == nil && o != nil {
		sent = o.Status // "sent" | "skipped:…" — the recorded truth
	}
	svc.s.Audit.Log(ctx, tenantID, "channel", "channel.guest_notified", "booking", b.ID,
		map[string]any{"source_partner_id": p.ID, "result": sent})

	// Then the sourcing partner: a WhatsApp status notice pointing at the
	// portal, where the referred-guest feed (Phase 63) shows live states.
	if p.Phone == "" || svc.s.waURL == "" {
		svc.s.Audit.Log(ctx, tenantID, "channel", "channel.partner_notify_skipped", "partner", p.ID,
			map[string]any{"booking_id": b.ID, "reason": "no phone or no adapter"})
		return
	}
	body := fmt.Sprintf("Good news — the booking you arranged is confirmed. "+
		"Track it in your portal: %s", svc.s.PartnerOps.PortalURL(tenantID, p.ID))
	status, detail, _ := postToAdapter(ctx, svc.s.waURL, []byte(fmt.Sprintf(
		`{"tenant_id":%q,"contact":%q,"body":%q}`, tenantID, p.Phone, body)))
	if status == "failed" && detail != "" {
		status = "failed:" + detail
	}
	svc.s.Audit.Log(ctx, tenantID, "channel", "channel.partner_notified", "partner", p.ID,
		map[string]any{"booking_id": b.ID, "channel": "whatsapp", "result": status})
}

// AttributeSource (operator, console) points an existing request at the
// partner who really sourced it — the email/phone-booked demand a hotel
// desk or agency actually sent. partnerID "" returns the request to
// "direct". The change is audit-logged, the booking follows, and a
// confirmed booking accrues the sourcing commission if it hasn't yet —
// idempotent, and existing payouts are never touched.
func (svc *ChannelService) AttributeSource(ctx context.Context, tenantID, requestID, partnerID string) (*model.ServiceRequest, error) {
	r, err := svc.s.Store.Requests().Get(ctx, tenantID, requestID)
	if err != nil {
		return nil, err
	}
	kind := model.SourceDirect
	if partnerID != "" {
		p, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID)
		if err != nil {
			return nil, err
		}
		if p.Status == "SUSPENDED" {
			return nil, httpx.ErrForbidden
		}
		kind = p.Kind
		if kind == "" {
			kind = model.PartnerKindTourOperator
		}
	}
	if r.SourcePartnerID == partnerID && r.SourceKind == kind {
		return r, nil // idempotent
	}
	prevKind, prevPartner := r.SourceKind, r.SourcePartnerID
	r.SourceKind, r.SourcePartnerID = kind, partnerID
	r.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.Requests().Update(ctx, r); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "service_request", r.ID, "channel.source_attributed",
		map[string]any{"from_kind": prevKind, "from_partner": prevPartner, "to_kind": kind, "to_partner": partnerID})
	svc.s.Audit.Log(ctx, tenantID, "channel", "channel.source_attributed", "service_request", r.ID,
		map[string]any{"from": prevKind + ":" + prevPartner, "to": kind + ":" + partnerID})

	// The booking follows its request; a confirmed booking accrues the
	// sourcing commission now if the lane is still empty.
	bookings, err := svc.s.Store.Bookings().List(ctx, tenantID)
	if err != nil {
		return r, nil // request attributed; booking sync is best-effort
	}
	for i := range bookings {
		b := &bookings[i]
		if b.RequestID != r.ID {
			continue
		}
		if err := svc.s.Store.Bookings().SetSource(ctx, tenantID, b.ID, kind, partnerID); err != nil {
			svc.s.Audit.Log(ctx, tenantID, "channel", "channel.booking_source_failed", "booking", b.ID,
				map[string]any{"error": err.Error()})
			continue
		}
		if partnerID != "" && b.State == "CONFIRMED" && !svc.sourcingPaid(ctx, tenantID, b.ID, partnerID) {
			b.SourceKind, b.SourcePartnerID = kind, partnerID
			svc.AccrueSourcingCommission(ctx, tenantID, b)
		}
	}
	return r, nil
}
