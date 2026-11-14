package domain

// Phase 15: CRM-360 — profile enrichment, consent management, duplicate
// merge, and the aggregated single-client view the console renders.
//
// Consent is the crown jewel here: outreach gating (Phase 14), preference
// encryption scopes, and DSAR all read client.Consent, but until now nothing
// could WRITE it through the API. SetConsent closes that loop with an audit
// record and an event per change.

import (
	"context"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// UpdateProfileInput carries editable client fields; nil/empty fields are
// left unchanged except where explicitly cleared via Clear.
type UpdateProfileInput struct {
	FullName    *string  `json:"full_name,omitempty"`
	Email       *string  `json:"email,omitempty"`
	Phone       *string  `json:"phone,omitempty"`
	Locale      *string  `json:"locale,omitempty"`
	Timezone    *string  `json:"timezone,omitempty"`
	LoyaltyTier *string  `json:"loyalty_tier,omitempty"`
	VIPFlags    []string `json:"vip_flags,omitempty"` // replaces wholesale when non-nil
}

// UpdateProfile enriches a client record (progressive registration: the CRM
// fills in as the relationship deepens). Audit-logged; emits client.updated
// with the changed field names.
func (svc *ClientService) UpdateProfile(ctx context.Context, tenantID, id string, in UpdateProfileInput) (*model.Client, error) {
	c, err := svc.s.Store.Clients().Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if err := requireNotErased(c); err != nil {
		return nil, err
	}
	var changed []string
	set := func(name string, dst *string, v *string) {
		if v != nil && *v != *dst {
			*dst = strings.TrimSpace(*v)
			changed = append(changed, name)
		}
	}
	set("full_name", &c.FullName, in.FullName)
	set("email", &c.Email, in.Email)
	set("phone", &c.Phone, in.Phone)
	set("locale", &c.Locale, in.Locale)
	set("timezone", &c.Timezone, in.Timezone)
	set("loyalty_tier", &c.LoyaltyTier, in.LoyaltyTier)
	if in.VIPFlags != nil {
		c.VIPFlags = in.VIPFlags
		changed = append(changed, "vip_flags")
	}
	if len(changed) == 0 {
		return c, nil
	}
	if err := svc.s.Store.Clients().Upsert(ctx, c); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "client", id, "client.updated",
		map[string]any{"fields": changed})
	svc.s.Audit.Log(ctx, tenantID, "api", "client.updated", "client", id,
		map[string]any{"fields": changed})
	return c, nil
}

// SetConsent grants or revokes one consent scope (marketing, service,
// data_processing, ...). Phase 14 outreach reads the marketing scope.
func (svc *ClientService) SetConsent(ctx context.Context, tenantID, clientID, scope string, granted bool) (*model.Client, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		return nil, httpx.ErrValidation
	}
	c, err := svc.s.Store.Clients().Get(ctx, tenantID, clientID)
	if err != nil {
		return nil, err
	}
	if err := requireNotErased(c); err != nil {
		return nil, err
	}
	if c.Consent == nil {
		c.Consent = map[string]any{}
	}
	c.Consent[scope] = granted
	if err := svc.s.Store.Clients().Upsert(ctx, c); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "client", clientID, "client.consent_updated",
		map[string]any{"scope": scope, "granted": granted})
	svc.s.Audit.Log(ctx, tenantID, "api", "client.consent_updated", "client", clientID,
		map[string]any{"scope": scope, "granted": granted})
	return c, nil
}

// Search finds clients by name/email/phone substring (console CRM).
func (svc *ClientService) Search(ctx context.Context, tenantID, query string, limit int) ([]model.Client, error) {
	return svc.s.Store.Clients().Search(ctx, tenantID, query, limit)
}

// Merge folds a duplicate (same human, two channel identities) into the
// winner: every client-scoped record re-points, the loser is deleted, and
// the event stream records the merge for downstream consumers.
func (svc *ClientService) Merge(ctx context.Context, tenantID, winnerID, loserID string) error {
	if winnerID == "" || loserID == "" || winnerID == loserID {
		return httpx.ErrValidation
	}
	// Never merge INTO an erased record: the winner must be a live identity,
	// otherwise the loser's personal data re-attaches to a tombstone.
	if winner, err := svc.s.Store.Clients().Get(ctx, tenantID, winnerID); err != nil {
		return err
	} else if err := requireNotErased(winner); err != nil {
		return err
	}
	if err := svc.s.Store.Clients().Merge(ctx, tenantID, winnerID, loserID); err != nil {
		return err
	}
	svc.s.Events.Emit(ctx, tenantID, "client", winnerID, "client.merged",
		map[string]any{"merged_client_id": loserID})
	svc.s.Audit.Log(ctx, tenantID, "api", "client.merged", "client", winnerID,
		map[string]any{"merged_client_id": loserID})
	return nil
}

// Client360 is the single-client aggregate the CRM console renders: identity,
// consent-scoped preference memory, commercial history, service history,
// channel presence, acquisition attribution, and outreach state.
type Client360 struct {
	Client         *model.Client             `json:"client"`
	ReferralCode   string                    `json:"referral_code"`
	Preferences    []model.Preference        `json:"preferences"`
	Requests       []model.ServiceRequest    `json:"requests"`
	Bookings       []model.Booking           `json:"bookings"`
	Cases          []model.Case              `json:"cases"`
	Conversations  []model.Conversation      `json:"conversations"`
	FirstTouch     *model.AttributionEvent   `json:"first_touch,omitempty"`
	RecentOutreach []model.OutreachSend      `json:"recent_outreach,omitempty"`
	SpendMinor     int64                     `json:"spend_minor"` // sum of booking attribution value
	Currency       string                    `json:"currency,omitempty"`
}

// View360 assembles the aggregate. Every component is best-effort — one
// store hiccup degrades a section, never the whole view.
func (svc *ClientService) View360(ctx context.Context, tenantID, id string) (*Client360, error) {
	c, err := svc.Get(ctx, tenantID, id) // audit-logged sensitive read
	if err != nil {
		return nil, err
	}
	v := &Client360{Client: c, ReferralCode: model.ClientReferralCode(id)}
	if prefs, err := svc.Preferences(ctx, tenantID, id); err == nil {
		v.Preferences = prefs
	}
	if reqs, err := svc.s.Store.Requests().ForClient(ctx, tenantID, id, 25); err == nil {
		v.Requests = reqs
	}
	if bks, err := svc.s.Store.Bookings().ForClient(ctx, tenantID, id, 25); err == nil {
		v.Bookings = bks
	}
	if cases, err := svc.s.Store.Cases().ForClient(ctx, tenantID, id, 25); err == nil {
		v.Cases = cases
	}
	if convs, err := svc.s.Store.Conversations().ForClient(ctx, tenantID, id, 25); err == nil {
		v.Conversations = convs
	}
	if events, err := svc.s.Store.Attribution().List(ctx, tenantID, id, 200); err == nil {
		for i := range events {
			if events[i].Channel != "" || events[i].Campaign != "" {
				ev := events[i]
				v.FirstTouch = &ev
				break
			}
		}
		for _, e := range events {
			if e.Kind == "booking" {
				v.SpendMinor += e.ValueMinor
			}
		}
	}
	if sends, err := svc.s.Store.Outreach().SendsForClient(ctx, tenantID, id,
		svc.s.Now().AddDate(0, 0, -90)); err == nil {
		v.RecentOutreach = sends
	}
	return v, nil
}
