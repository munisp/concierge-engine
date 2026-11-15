package model

import "time"

// Phase 14: proactive outreach (consent-gated, frequency-capped) and
// acquisition attribution (first-touch, funnel, client referral codes).

// OutreachSend is one proactive outbound message to a client — the
// platform's only non-reactive channel act. Every send is consent-gated
// (marketing requires explicit consent), frequency-capped, idempotent per
// (tenant, idempotency key), and records the channel message id so Phase
// 11 receipts close the delivery loop.
type OutreachSend struct {
	ID             string   `json:"id"`
	TenantID       string   `json:"tenant_id"`
	ClientID       string   `json:"client_id"`
	Channel        string   `json:"channel"` // whatsapp | email (Phase 39)
	Purpose        string   `json:"purpose"` // service | marketing
	Body           string   `json:"body,omitempty"`
	Subject        string   `json:"subject,omitempty"`  // Phase 39: email lane
	Template       string   `json:"template,omitempty"` // approved WA template; outside-24h sends
	Params         []string `json:"params,omitempty"`
	ExternalRef    string   `json:"external_ref,omitempty"` // channel message id (wamid)
	Status         string   `json:"status"`                 // scheduled | sent | held | failed
	Detail         string   `json:"detail,omitempty"`
	IdempotencyKey string   `json:"-"`
	// ScheduledFor (Phase 35): when set, the send is recorded as
	// "scheduled" and the hourly sweep delivers it once due. Nil = send
	// immediately. A scheduled send still passes consent/cap gates at
	// schedule time AND re-validates consent at delivery time.
	ScheduledFor *time.Time `json:"scheduled_for,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

const (
	OutreachService   = "service"   // booking updates, safety — always allowed
	OutreachMarketing = "marketing" // offers, win-back — consent + cap gated
)

// Outreach channels (Phase 39). Empty input channel normalizes to whatsapp
// (the Phase 14 default) so existing callers keep their behavior.
const (
	OutreachChannelWhatsApp = "whatsapp"
	OutreachChannelEmail    = "email"
)

// MessageTemplate (Phase 21) is a tenant-managed outreach message body.
// Templates are rendered server-side at send time — {{client_name}} and
// {{tenant_name}} substitutions — so the stored send record and the channel
// both carry the final text (provenance: the record keeps the rendered
// body, not just the template reference). Purpose guards the cap: a
// "marketing" template cannot be sent as "service" to dodge the frequency
// cap; "any" follows the send's purpose.
type MessageTemplate struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name"`
	Purpose   string    `json:"purpose"` // service | marketing | any
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const (
	TemplatePurposeAny = "any"
)
