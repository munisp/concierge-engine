package model

import "time"

// Phase 35: customer acquisition & outreach innovations — shared models.

// CampaignWindow (A12): a named seasonal campaign window. While the clock
// is inside [Start, End] the public site appends utm_campaign={Name} to
// its WhatsApp/capture CTAs, so first-touch attribution and the campaign
// funnel group window traffic under the campaign name.
type CampaignWindow struct {
	Name  string `json:"name"`
	Start string `json:"start"` // RFC3339
	End   string `json:"end"`   // RFC3339
}

// Active reports whether the window covers t. Malformed or inverted dates
// = inactive (honest degrade: a config typo never breaks the site — the
// window simply no-ops).
func (w CampaignWindow) Active(t time.Time) bool {
	if w.Name == "" {
		return false
	}
	start, err1 := time.Parse(time.RFC3339, w.Start)
	end, err2 := time.Parse(time.RFC3339, w.End)
	if err1 != nil || err2 != nil || !end.After(start) {
		return false
	}
	return !t.Before(start) && t.Before(end)
}

// ActiveCampaign returns the first campaign window covering t, or "".
func (c AcquisitionConfig) ActiveCampaign(t time.Time) string {
	for _, w := range c.CampaignWindows {
		if w.Active(t) {
			return w.Name
		}
	}
	return ""
}

// Sequence (O3): a drip campaign. Steps fire AfterHours after the previous
// step (step 0: after enrollment). Per-client progress is DERIVED from the
// outreach_sends ledger via idempotency keys seq:{name}:{step}:{client} —
// replay-safe sweeps with no extra state table.
type Sequence struct {
	Name    string         `json:"name"`
	Purpose string         `json:"purpose,omitempty"` // default marketing
	Steps   []SequenceStep `json:"steps"`
	// Segment (Phase 36): when set, only clients matching the ruleset
	// enroll — e.g. {lead_kind: "event"} for an event-inquiry drip.
	// Nil = all clients (Phase 35 behavior).
	Segment *SegmentRules `json:"segment,omitempty"`
}

type SequenceStep struct {
	AfterHours int    `json:"after_hours"` // delay after previous step / enrollment
	Body       string `json:"body"`        // {{client_name}} / {{tenant_name}} render
}

// SegmentRules (O2): a declarative client segment — every present field
// must match (AND). Used by broadcast campaigns (O1) and the segment
// preview endpoint. Booking-derived fields are evaluated against the
// tenant's booking ledger at query time.
type SegmentRules struct {
	Locale             string `json:"locale,omitempty"`
	LoyaltyTier        string `json:"loyalty_tier,omitempty"`
	VIPFlag            string `json:"vip_flag,omitempty"` // must be present in Client.VIPFlags
	OrgID              string `json:"org_id,omitempty"`
	MarketingConsent   *bool  `json:"marketing_consent,omitempty"`
	HasBookings        *bool  `json:"has_bookings,omitempty"`
	MinBookings        int    `json:"min_bookings,omitempty"`
	NoBookingSinceDays int    `json:"no_booking_since_days,omitempty"` // dormant: last booking older than this (never-booked counts)
	CreatedBeforeDays  int    `json:"created_before_days,omitempty"`   // client created at least N days ago
	// LeadKind (Phase 36): only clients linked to an OPEN lead of this kind
	// match. Wires the lead engine into broadcasts and drip sequences.
	LeadKind string `json:"lead_kind,omitempty"`
}

// QuoteShare (A4): a shareable public link for a quote. The token is an
// HMAC capability (family "quote/") minted server-side; the public page at
// /quote/{token} renders the priced offer with a one-tap WhatsApp CTA —
// the prospect's forward-to-a-friend growth loop.
type QuoteShare struct {
	Token     string    `json:"token"`
	QuoteID   string    `json:"quote_id"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}
