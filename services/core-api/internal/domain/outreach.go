package domain

// Phase 14: proactive outreach — the platform's only non-reactive channel
// act. Every send is consent-gated (marketing requires explicit client
// consent), frequency-capped (marketing <= 3 per rolling 7 days), idempotent
// per (tenant, idempotency key), and records the channel message id so the
// Phase 11 receipt pipeline closes the delivery loop.
//
// Honest degrade: when no channel adapter is configured the send is still
// recorded — status "failed", detail naming the cause — so the audit spine
// shows the attempt instead of silently dropping it.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// OutreachMarketingWeeklyCap bounds marketing sends per client per rolling
// 7-day window. Service-purpose sends (booking updates, safety) are never
// capped and never consent-gated.
const OutreachMarketingWeeklyCap = 3

type OutreachService struct {
	s     *Services
	waURL string // channel adapter base URL; empty = record-only
}

func NewOutreachService(s *Services, waURL string) *OutreachService {
	return &OutreachService{s: s, waURL: waURL}
}

type SendOutreachInput struct {
	ClientID       string   `json:"client_id"`
	Purpose        string   `json:"purpose"` // service | marketing (default service)
	Body           string   `json:"body,omitempty"`
	Template       string   `json:"template,omitempty"`
	Params         []string `json:"params,omitempty"`
	IdempotencyKey string   `json:"idempotency_key"`
}

// Verdict reports whether a send to this client would be allowed right now,
// without sending anything — the console composer renders this live.
type OutreachVerdict struct {
	Allowed          bool   `json:"allowed"`
	Reason           string `json:"reason,omitempty"`
	MarketingSent7d  int    `json:"marketing_sent_7d"`
	MarketingCap     int    `json:"marketing_cap"`
	MarketingConsent bool   `json:"marketing_consent"`
}

func (svc *OutreachService) Verdict(ctx context.Context, tenantID, clientID, purpose string) (*OutreachVerdict, error) {
	cl, err := svc.s.Store.Clients().Get(ctx, tenantID, clientID)
	if err != nil {
		return nil, err
	}
	v := &OutreachVerdict{Allowed: true, MarketingCap: OutreachMarketingWeeklyCap}
	v.MarketingConsent = cl.Consent["marketing"] == true
	if purpose != model.OutreachMarketing {
		return v, nil // service: always allowed
	}
	since := svc.s.Now().Add(-7 * 24 * time.Hour)
	sends, err := svc.s.Store.Outreach().SendsForClient(ctx, tenantID, clientID, since)
	if err != nil {
		return nil, err
	}
	for _, o := range sends {
		if o.Purpose == model.OutreachMarketing && o.Status == "sent" {
			v.MarketingSent7d++
		}
	}
	if !v.MarketingConsent {
		v.Allowed = false
		v.Reason = "marketing consent not granted"
	} else if v.MarketingSent7d >= OutreachMarketingWeeklyCap {
		v.Allowed = false
		v.Reason = fmt.Sprintf("frequency cap: %d marketing sends per 7 days", OutreachMarketingWeeklyCap)
	}
	return v, nil
}

// Send delivers one proactive message to a client. Idempotent: a replayed
// idempotency key returns the original record and never re-sends.
func (svc *OutreachService) Send(ctx context.Context, tenantID string, in SendOutreachInput) (*model.OutreachSend, error) {
	if in.ClientID == "" || in.IdempotencyKey == "" {
		return nil, httpx.ErrValidation
	}
	if in.Purpose == "" {
		in.Purpose = model.OutreachService
	}
	if in.Purpose != model.OutreachService && in.Purpose != model.OutreachMarketing {
		return nil, httpx.ErrValidation
	}
	if in.Body == "" && in.Template == "" {
		return nil, httpx.ErrValidation
	}
	// Replay fast-path: an existing record with this key is returned as-is.
	if prev, err := svc.s.Store.Outreach().ListSends(ctx, tenantID, 500); err == nil {
		for _, o := range prev {
			if o.IdempotencyKey == in.IdempotencyKey {
				cp := o
				return &cp, nil
			}
		}
	}
	cl, err := svc.s.Store.Clients().Get(ctx, tenantID, in.ClientID)
	if err != nil {
		return nil, err
	}
	if cl.Phone == "" {
		return nil, fmt.Errorf("%w: client has no phone on file", httpx.ErrValidation)
	}

	o := &model.OutreachSend{
		ID: NewID(), TenantID: tenantID, ClientID: in.ClientID,
		Channel: "whatsapp", Purpose: in.Purpose, Body: in.Body,
		Template: in.Template, Params: in.Params,
		IdempotencyKey: in.IdempotencyKey, CreatedAt: svc.s.Now(),
	}

	// Gates: consent + frequency cap. A gated send is recorded as "held" —
	// the decision is auditable, and a replayed key returns the same verdict.
	if v, err := svc.Verdict(ctx, tenantID, in.ClientID, in.Purpose); err != nil {
		return nil, err
	} else if !v.Allowed {
		o.Status, o.Detail = "held", v.Reason
		return svc.record(ctx, o)
	}

	// Deliver through the channel adapter (freeform inside the 24h session,
	// approved template outside it; the adapter decides and may hold).
	status, detail, ref := svc.deliver(ctx, tenantID, cl.Phone, in)
	o.Status, o.Detail, o.ExternalRef = status, detail, ref
	rec, err := svc.record(ctx, o)
	if err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "outreach", o.ID, "outreach.sent",
		map[string]any{"client_id": o.ClientID, "purpose": o.Purpose,
			"status": o.Status, "external_ref": o.ExternalRef})
	svc.s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "outreach_sent", Channel: "whatsapp",
		Campaign: o.Purpose, ClientID: o.ClientID,
	})
	return rec, nil
}

// record persists the send; on an idempotency replay race it returns the
// original record instead.
func (svc *OutreachService) record(ctx context.Context, o *model.OutreachSend) (*model.OutreachSend, error) {
	err := svc.s.Store.Outreach().AddSend(ctx, o)
	if err == nil {
		return o, nil
	}
	if err != httpx.ErrConflict {
		return nil, err
	}
	if prev, lerr := svc.s.Store.Outreach().ListSends(ctx, o.TenantID, 500); lerr == nil {
		for _, p := range prev {
			if p.IdempotencyKey == o.IdempotencyKey {
				cp := p
				return &cp, nil
			}
		}
	}
	return nil, err
}

// deliver calls the adapter /v1/send. Advisory by construction: any adapter
// failure yields status "failed" with the cause in detail — never an error,
// so the send record always lands.
func (svc *OutreachService) deliver(ctx context.Context, tenantID, phone string, in SendOutreachInput) (status, detail, externalRef string) {
	if svc.waURL == "" {
		return "failed", "channel adapter not configured (CE_WHATSAPP_ADAPTER_URL)", ""
	}
	payload, _ := json.Marshal(map[string]any{
		"tenant_id": tenantID, "contact": phone,
		"body": in.Body, "template": in.Template, "params": in.Params,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(svc.waURL, "/")+"/v1/send", strings.NewReader(string(payload)))
	if err != nil {
		return "failed", "adapter request build: " + err.Error(), ""
	}
	req.Header.Set("Content-Type", "application/json")
	hc := &http.Client{Timeout: 10 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return "failed", "channel adapter unreachable: " + err.Error(), ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "failed", fmt.Sprintf("channel adapter returned %d", resp.StatusCode), ""
	}
	var ar struct {
		Mode   string `json:"mode"`
		Reason string `json:"reason"`
		Body   struct {
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
		} `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return "failed", "adapter response unreadable: " + err.Error(), ""
	}
	if ar.Mode == "held" {
		return "held", ar.Reason, ""
	}
	if len(ar.Body.Messages) > 0 {
		externalRef = ar.Body.Messages[0].ID
	}
	return "sent", ar.Mode, externalRef
}

// Sends lists recent outreach sends (console growth board).
func (svc *OutreachService) Sends(ctx context.Context, tenantID string, limit int) ([]model.OutreachSend, error) {
	return svc.s.Store.Outreach().ListSends(ctx, tenantID, limit)
}
