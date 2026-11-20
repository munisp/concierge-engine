// Phase 57: partner PWA + WhatsApp offers. The partner portal is a
// magic-link surface (HMAC token, no login) — small tour operators don't
// install apps and don't have developers; a link on their phone is the
// whole product. WhatsApp is the notification channel: an offer lands as a
// message with the portal link, and the partner accepts/declines in the
// portal (one tap, real buttons — never keyword parsing).
package domain

import (
	"context"
	"fmt"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// PartnerPortalView is everything the partner PWA renders: who they are,
// their assignments (offered first), and their money.
type PartnerPortalView struct {
	PartnerName string                  `json:"partner_name"`
	Verticals   []string                `json:"verticals"`
	Kind        string                  `json:"kind,omitempty"` // Phase 63: demand channel kind
	Assignments []PartnerAssignmentView `json:"assignments"`
	Settlement  *PartnerSettlement      `json:"settlement"`
	// Phase 63: demand this partner has sourced (book-on-behalf) — the
	// hotel desk sees its guests' request states next to its offers.
	SourcedRequests []SourcedRequestView `json:"sourced_requests,omitempty"`
	// Phase 68: the partner's own bill-to-desk statements — accruing,
	// payable (with derived due date / days past due) and paid, with the
	// verbatim reminders ledger.
	Folios []PortalFolio `json:"folios,omitempty"`
}

// PortalView builds the partner PWA payload. Suspended partners are locked
// out — suspending a partner revokes every outstanding portal link at once
// (tokens are stateless HMAC; status is checked on every request).
func (svc *PartnerOpsService) PortalView(ctx context.Context, tenantID, partnerID string) (*PartnerPortalView, error) {
	p, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	if p.Status == "SUSPENDED" {
		return nil, httpx.ErrForbidden
	}
	assignments, err := svc.AssignmentsFor(ctx, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	settlement, err := svc.Settlement(ctx, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	kind := p.Kind
	if kind == "" {
		kind = model.PartnerKindTourOperator
	}
	view := &PartnerPortalView{
		PartnerName: p.Name, Verticals: p.Verticals, Kind: kind,
		Assignments: assignments, Settlement: settlement,
	}
	// Phase 63: sourced demand is best-effort — a listing failure never
	// locks the partner out of their offers and money.
	if svc.s.Channels != nil {
		if sourced, err := svc.s.Channels.SourcedRequests(ctx, tenantID, partnerID, 25); err == nil {
			view.SourcedRequests = sourced
		}
	}
	// Phase 68: the partner's statements — same best-effort discipline: a
	// folio listing failure never locks the partner out of their offers.
	if folios, err := svc.s.Folios.PortalFolios(ctx, tenantID, partnerID); err == nil {
		view.Folios = folios
	}
	return view, nil
}

// PortalURL is the shareable partner portal link (console "copy link", and
// the URL embedded in WhatsApp offers).
func (svc *PartnerOpsService) PortalURL(tenantID, partnerID string) string {
	base := svc.s.PublicBase()
	if base == "" {
		base = svc.s.ConsoleBase()
	}
	return strings.TrimSuffix(base, "/") + "/partner?token=" + svc.s.Trips.PartnerTokenFor(tenantID, partnerID)
}

// NotifyResult reports honestly what happened to a WhatsApp offer.
type NotifyResult struct {
	PortalURL string `json:"portal_url"`
	WhatsApp  string `json:"whatsapp"` // sent | skipped:no-phone | skipped:no-adapter | failed:…
}

// NotifyOffer sends the partner a WhatsApp nudge pointing at their portal.
// Degrades honestly: no partner phone or no channel adapter configured →
// "skipped" with the reason, never a fake "sent".
func (svc *PartnerOpsService) NotifyOffer(ctx context.Context, tenantID, partnerID string) (*NotifyResult, error) {
	p, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	res := &NotifyResult{PortalURL: svc.PortalURL(tenantID, partnerID)}
	if p.Phone == "" {
		res.WhatsApp = "skipped:no-phone"
		return res, nil
	}
	if svc.s.waURL == "" {
		res.WhatsApp = "skipped:no-adapter"
		return res, nil
	}
	body := fmt.Sprintf("New assignment offer from your concierge partner network. "+
		"Open your portal to accept or decline: %s", res.PortalURL)
	status, detail, _ := postToAdapter(ctx, svc.s.waURL, []byte(fmt.Sprintf(
		`{"tenant_id":%q,"contact":%q,"body":%q}`, tenantID, p.Phone, body)))
	res.WhatsApp = status
	if status == "failed" && detail != "" {
		res.WhatsApp = "failed:" + detail
	}
	svc.s.Audit.Log(ctx, tenantID, "partner", "partner.notified", "partner", partnerID,
		map[string]any{"channel": "whatsapp", "result": res.WhatsApp})
	return res, nil
}

// notifyOfferDetached fires the WhatsApp offer after an assignment without
// holding the booking request hostage to channel latency (the adapter POST
// has a 10s timeout — unacceptable inside Assign). Background context:
// request cancellation must not kill the notification.
func (svc *PartnerOpsService) notifyOfferDetached(ctx context.Context, tenantID, partnerID string) {
	p, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID)
	if err != nil || p.Phone == "" || svc.s.waURL == "" {
		return // nothing to send on — the console Notify button stays manual
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	go func() {
		defer cancel()
		defer func() { _ = recover() }() // a channel blip never takes the API down
		if _, err := svc.NotifyOffer(bg, tenantID, partnerID); err != nil {
			svc.s.Audit.Log(bg, tenantID, "partner", "partner.notify_failed", "partner", partnerID,
				map[string]any{"error": err.Error()})
		}
	}()
}

// OfferedOnAssign is the Dispatch.Assign hook: partner-routed work nudges
// the partner's WhatsApp (when reachable) in addition to the webhook feed.
func (svc *PartnerOpsService) OfferedOnAssign(ctx context.Context, tenantID, partnerID string) {
	svc.notifyOfferDetached(ctx, tenantID, partnerID)
}
