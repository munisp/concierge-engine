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
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	ClientID       string    `json:"client_id"`
	Channel        string    `json:"channel"`  // whatsapp (v1)
	Purpose        string    `json:"purpose"`  // service | marketing
	Body           string    `json:"body,omitempty"`
	Template       string    `json:"template,omitempty"` // approved WA template; outside-24h sends
	Params         []string  `json:"params,omitempty"`
	ExternalRef    string    `json:"external_ref,omitempty"` // channel message id (wamid)
	Status         string    `json:"status"`   // sent | held | failed
	Detail         string    `json:"detail,omitempty"`
	IdempotencyKey string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}

const (
	OutreachService   = "service"   // booking updates, safety — always allowed
	OutreachMarketing = "marketing" // offers, win-back — consent + cap gated
)
