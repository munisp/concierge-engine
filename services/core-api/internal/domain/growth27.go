package domain

// Acquisition innovation pack (Phase 27): ten channel plays that all reuse
// the existing engines (promos, rewards, outreach, attribution, reviews,
// the SEO site) instead of building parallel machinery:
//
//  1. QR booking codes (server-rendered PNG, channel=qr)
//  2. Link-in-bio social page (/site/{slug}/links)
//  3. WhatsApp click-to-chat deep links per service
//  4. Post-trip reviews → consented aggregateRating rich snippets
//  5. Gift vouchers (one-redemption promo a client hands a non-client)
//  6. Time-boxed referral boost windows (RewardProgram multiplier)
//  7. Dormant-lead activation nudges (lifecycle engine, marketing lane)
//  8. Embeddable booking widget for existing websites (channel=widget)
//  9. Competitor switch-offer codes (redemption pings the operator roster)
// 10. Partner co-marketing links (channel=partner, campaign=partner:{id})
//
// Everything terminates in the same attribution spine, so the Phase 26
// acquisition fee and cohort tracker see these channels without changes —
// "qr", "widget" and "gift" join the default acquisition taxonomy.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// ---------------------------------------------------------- reviews

// SubmitReview records a post-trip review. The caller authenticates the
// passenger out-of-band (the server layer uses the track token, which
// proves custody of the assignment link). One review per assignment —
// structural idempotency, replays return the original.
func (svc *Services) SubmitReview(ctx context.Context, tenantID, clientID, assignmentID string, rating int, text string, publishConsent bool) (*model.Review, error) {
	if rating < 1 || rating > 5 || assignmentID == "" {
		return nil, httpx.ErrValidation
	}
	if len(text) > 1000 {
		text = text[:1000]
	}
	r := &model.Review{
		ID: NewID(), TenantID: tenantID, ClientID: clientID, AssignmentID: assignmentID,
		Rating: rating, Text: strings.TrimSpace(text), PublishConsent: publishConsent,
		CreatedAt: svc.Now(),
	}
	if err := svc.Store.Reviews().Create(ctx, r); err != nil {
		if err == httpx.ErrConflict {
			return svc.Store.Reviews().GetByAssignment(ctx, tenantID, assignmentID) // replay-safe
		}
		return nil, err
	}
	svc.Events.Emit(ctx, tenantID, "review", r.ID, "review.submitted",
		map[string]any{"rating": rating, "publish_consent": publishConsent, "assignment_id": assignmentID})
	return r, nil
}

// ReviewSummary is the public-facing aggregate: only publish-consented
// reviews count, so the number rendered on the SEO site is exactly the
// evidence behind it.
type ReviewSummary struct {
	Count   int      `json:"count"`
	Average float64  `json:"average"`
	Recent  []string `json:"recent,omitempty"` // up to 3 consented review texts
}

func (svc *Services) ReviewSummary(ctx context.Context, tenantID string) (*ReviewSummary, error) {
	revs, err := svc.Store.Reviews().Consented(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	sum := &ReviewSummary{Count: len(revs)}
	if len(revs) == 0 {
		return sum, nil
	}
	total := 0
	for _, r := range revs {
		total += r.Rating
		if r.Text != "" && len(sum.Recent) < 3 {
			sum.Recent = append(sum.Recent, r.Text)
		}
	}
	sum.Average = float64(total) / float64(len(revs))
	return sum, nil
}

func (svc *Services) ListReviews(ctx context.Context, tenantID string, limit int) ([]model.Review, error) {
	revs, err := svc.Store.Reviews().ListAll(ctx, tenantID, limit)
	if err != nil {
		return nil, err
	}
	if revs == nil {
		revs = []model.Review{}
	}
	return revs, nil
}

// ---------------------------------------------------------- gift vouchers & switch offers

// redeemSideEffects runs the acquisition side effects of a promo
// redemption. Called from RedeemPromo after the credit accrues. Advisory:
// side effects never fail a redemption.
func (svc *Services) redeemSideEffects(ctx context.Context, tenantID, clientID string, c *model.PromoCampaign) {
	if c.Gift {
		// The recipient's first booking will trace back to gift:<code> —
		// "gift" is in the default acquisition taxonomy, so the platform's
		// 10% applies to gifted clients exactly like SEO-sourced ones.
		svc.TrackInherited(ctx, &model.AttributionEvent{
			TenantID: tenantID, Kind: "gift_redeemed", Channel: "gift",
			Campaign: "gift:" + c.Code, ClientID: clientID,
		})
	}
	if c.SwitchOffer {
		svc.Events.Emit(ctx, tenantID, "promo", c.ID, "promo.switch_redeemed",
			map[string]any{"code": c.Code, "client_id": clientID})
		svc.pushOperatorNotice(ctx, tenantID, fmt.Sprintf(
			"SWITCH SIGNAL — a client just redeemed %s (%d %s credit). High-intent competitor switch; consider a personal welcome within the hour.",
			c.Code, c.CreditMinor, c.Currency))
	}
}

// pushOperatorNotice pushes an advisory WhatsApp notice to the tenant's
// operator roster (fallback CE_OPERATOR_PHONE). Shared transport with the
// Phase 13/26 alerts; silent when no adapter is configured.
func (svc *Services) pushOperatorNotice(ctx context.Context, tenantID, body string) {
	if svc.waURL == "" {
		return
	}
	phones := []string{}
	if t, err := svc.Store.Tenants().Get(ctx, tenantID); err == nil && len(t.OperatorConfig.AlertPhones) > 0 {
		phones = t.OperatorConfig.AlertPhones
	} else if svc.operatorPhone != "" {
		phones = []string{svc.operatorPhone}
	}
	for _, phone := range phones {
		pushWhatsAppAlert(ctx, svc.waURL, tenantID, phone, body)
	}
}

// ---------------------------------------------------------- referral boost window

// boostActive reports whether the tenant's referral boost window is live.
func boostActive(prog model.RewardProgram, now time.Time) float64 {
	if prog.BoostUntil == "" || prog.BoostMultiplier <= 1 {
		return 1
	}
	until, err := time.Parse(time.RFC3339, prog.BoostUntil)
	if err != nil || !now.Before(until) {
		return 1
	}
	return prog.BoostMultiplier
}

// ---------------------------------------------------------- partner co-marketing

// PartnerAcquisition is a partner's co-marketing surface: the trackable
// capture link they share, plus conversions it produced (from the
// attribution funnel — the same evidence the acquisition fee uses).
type PartnerAcquisition struct {
	PartnerID   string `json:"partner_id"`
	PartnerName string `json:"partner_name"`
	Link        string `json:"link"`
	Leads       int    `json:"leads"`       // page_view / conversation starts via the link
	Requests    int    `json:"requests"`    // request_created events
	Bookings    int    `json:"bookings"`    // booking events
	ValueMinor  int64  `json:"value_minor"` // attributed booking value
}

func (svc *Services) PartnerAcquisition(ctx context.Context, tenantID, partnerID string) (*PartnerAcquisition, error) {
	p, err := svc.Store.Partners().Get(ctx, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	campaign := "partner:" + p.ID
	pa := &PartnerAcquisition{
		PartnerID: p.ID, PartnerName: p.Name,
		Link: fmt.Sprintf("%s/capture?tenant=%s&channel=partner&utm_source=partner&utm_campaign=%s",
			svc.ConsoleBase(), tenantID, campaign),
	}
	rows, err := svc.Store.Attribution().Funnel(ctx, tenantID)
	if err != nil {
		return pa, nil // funnel is advisory — the link itself is the deliverable
	}
	for _, r := range rows {
		if r.Campaign != campaign {
			continue
		}
		switch r.Kind {
		case "booking", "referral_converted":
			pa.Bookings += r.Count
			pa.ValueMinor += r.ValueMinor
		case "request_created":
			pa.Requests += r.Count
		default:
			pa.Leads += r.Count
		}
	}
	return pa, nil
}
