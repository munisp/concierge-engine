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
// capped and never consent-gated. Phase 20: tenants override the cap via
// OutreachConfig.MarketingCap7d (0 = this platform default).
const OutreachMarketingWeeklyCap = 3

// marketingCap resolves the tenant's effective weekly marketing cap.
func (svc *OutreachService) marketingCap(ctx context.Context, tenantID string) int {
	if t, err := svc.s.Store.Tenants().Get(ctx, tenantID); err == nil &&
		t.OutreachConfig.MarketingCap7d > 0 {
		return t.OutreachConfig.MarketingCap7d
	}
	return OutreachMarketingWeeklyCap
}

type OutreachService struct {
	s        *Services
	waURL    string // WhatsApp adapter base URL; empty = record-only
	emailURL string // Phase 39: email adapter base URL; empty = record-only
}

func NewOutreachService(s *Services, waURL, emailURL string) *OutreachService {
	return &OutreachService{s: s, waURL: waURL, emailURL: emailURL}
}

// EmailIntroMaxTouches bounds the Phase 39 b2b-introduction exception: a
// client reachable only through a bought/partner list (open lead with
// contact_basis = b2b_legitimate_interest, no marketing consent) may receive
// at most this many introduction emails. Every one carries one-click
// unsubscribe; after the cap the lane closes until consent is earned on a
// reply (Phase 38 repeat-touch grant). GDPR recital 47 territory — the
// platform enforces the narrow version: business contacts, few touches,
// easy out, full audit.
const EmailIntroMaxTouches = 2

type SendOutreachInput struct {
	ClientID       string   `json:"client_id"`
	Purpose        string   `json:"purpose"` // service | marketing (default service)
	Body           string   `json:"body,omitempty"`
	Template       string   `json:"template,omitempty"`
	Params         []string `json:"params,omitempty"`
	IdempotencyKey string   `json:"idempotency_key"`
	// Channel (Phase 39): whatsapp (default) | email. Email sends go to the
	// client's email address, require a subject, and every marketing email
	// carries a mandated unsubscribe footer + one-click link (server-side —
	// a template or operator cannot omit it).
	Channel string `json:"channel,omitempty"`
	// Subject is required for email sends; ignored on whatsapp.
	Subject string `json:"subject,omitempty"`
	// TemplateID (Phase 21): a tenant-managed message template. The body is
	// rendered server-side ({{client_name}}, {{tenant_name}}) and the
	// rendered text — not the template reference — is what gets recorded
	// and delivered. A template whose purpose is "marketing" cannot be
	// sent as "service" (the frequency cap is not a suggestion).
	TemplateID string `json:"template_id,omitempty"`
	// ScheduledFor (Phase 35, O6): explicit delivery time. Future = the
	// send is recorded "scheduled" and the hourly sweep delivers it;
	// past/nil = immediate (subject to quiet-hours/optimization deferral
	// for marketing sends). Operator-set schedules always win over the
	// automatic deferral.
	ScheduledFor *time.Time `json:"scheduled_for,omitempty"`
}

// Verdict reports whether a send to this client would be allowed right now,
// without sending anything — the console composer renders this live.
type OutreachVerdict struct {
	Allowed          bool   `json:"allowed"`
	Reason           string `json:"reason,omitempty"`
	MarketingSent7d  int    `json:"marketing_sent_7d"`
	MarketingCap     int    `json:"marketing_cap"`
	MarketingConsent bool   `json:"marketing_consent"`
	// Phase 39: b2b-introduction lane state (email channel only).
	IntroTouches int  `json:"intro_touches,omitempty"`
	IntroMax     int  `json:"intro_max,omitempty"`
	IntroLane    bool `json:"intro_lane,omitempty"`
	// Phase 40: tenant-level email deliverability health (populated on
	// marketing email verdicts only). Bounce rate = (bounced + complained)
	// / sent over the trailing 7 days, from the attribution spine.
	EmailSent7d     int     `json:"email_sent_7d,omitempty"`
	EmailBounceRate float64 `json:"email_bounce_rate,omitempty"`
	// Phase 41: the client's address is on the tenant suppression list
	// (hard bounce / complaint / manual) — every email lane holds.
	EmailSuppressed bool `json:"email_suppressed,omitempty"`
	// Phase 42: the client's WhatsApp number looks dead — 3+ failed
	// deliveries with none confirmed since. Marketing lane holds.
	WaUnreachable bool `json:"wa_unreachable,omitempty"`
}

// Phase 40 deliverability guard: below the minimum volume there is no
// signal worth acting on; above the bounce+complaint ceiling the tenant is
// burning sender reputation and marketing email pauses until the window
// clears. 5% is the conservative end of ESP guidance (Gmail bulk-sender
// complaint threshold is 0.3%, but that excludes bounces; a small sender's
// bounce-heavy list breaches 5% long before complaints register).
const (
	EmailGuardMinSends7d = 20
	EmailGuardMaxRate    = 0.05
)

func (svc *OutreachService) Verdict(ctx context.Context, tenantID, clientID, purpose, channel string) (*OutreachVerdict, error) {
	cl, err := svc.s.Store.Clients().Get(ctx, tenantID, clientID)
	if err != nil {
		return nil, err
	}
	maxSends := svc.marketingCap(ctx, tenantID)
	v := &OutreachVerdict{Allowed: true, MarketingCap: maxSends}
	if cl.Erased() {
		// GDPR/CCPA: an erased data subject must not be contacted on any
		// purpose — recorded as held so the refusal is auditable.
		v.Allowed = false
		v.Reason = "client erased (GDPR/CCPA)"
		return v, nil
	}
	// Phase 41 suppression list (email only): an address the ESP reported
	// hard-bounced or complained (or an operator blocked manually) is held
	// on BOTH lanes — a dead address delivers nothing, and re-mailing it is
	// the single biggest bounce driver. Advisory: a store error fails open;
	// consent and cap remain the authoritative refusals.
	if channel == model.OutreachChannelEmail && cl.Email != "" {
		if suppressed, serr := svc.s.Store.EmailSuppressions().Exists(ctx, tenantID, strings.ToLower(strings.TrimSpace(cl.Email))); serr == nil && suppressed {
			v.Allowed = false
			v.EmailSuppressed = true
			v.Reason = "email address suppressed (hard bounce, complaint or manual block) — remove it from the suppression list to resume"
			return v, nil
		}
	}
	v.MarketingConsent = cl.Consent["marketing"] == true
	if purpose != model.OutreachMarketing {
		return v, nil // service: always allowed
	}
	since := svc.s.Now().Add(-7 * 24 * time.Hour)
	sends, err := svc.s.Store.Outreach().SendsForClient(ctx, tenantID, clientID, since)
	if err != nil {
		return nil, err
	}
	// Phase 39: the frequency cap is per channel — a WhatsApp send and an
	// email are different intrusions and deplete separate budgets.
	for _, o := range sends {
		if o.Purpose == model.OutreachMarketing && o.Status == "sent" && o.Channel == channel {
			v.MarketingSent7d++
		}
	}
	if v.MarketingSent7d >= maxSends {
		v.Allowed = false
		v.Reason = fmt.Sprintf("frequency cap: %d marketing sends per 7 days", maxSends)
		return v, nil
	}
	// Phase 40 deliverability guard (email only): when the tenant's trailing
	// 7-day bounce+complaint rate breaches the ceiling at meaningful volume,
	// marketing email pauses tenant-wide — held and auditable, self-healing
	// as the bad window ages out. Service email is exempt (a safety message
	// still goes), WhatsApp is untouched.
	if channel == model.OutreachChannelEmail {
		sent, _, _, rate := svc.emailHealth(ctx, tenantID, since)
		v.EmailSent7d, v.EmailBounceRate = sent, rate
		if sent >= EmailGuardMinSends7d && rate > EmailGuardMaxRate {
			v.Allowed = false
			v.Reason = fmt.Sprintf("deliverability guard: %.1f%% of %d emails bounced or complained in 7 days (max %.0f%%) — clean the list before sending more",
				rate*100, sent, EmailGuardMaxRate*100)
			return v, nil
		}
	}
	// Phase 41 engagement sunset (marketing email only): a client sent
	// EmailSunsetMinSends or more marketing emails without ever opening or
	// clicking one is held out of the marketing lane — chronic non-openers
	// are tomorrow's complaints, and every send to them dilutes the
	// engagement signals mailbox providers score the tenant on. One open or
	// click ever resets it; service email is exempt.
	if channel == model.OutreachChannelEmail && svc.sunsetApplies(ctx, tenantID, clientID) {
		v.Allowed = false
		v.Reason = fmt.Sprintf("sunset: %d+ marketing emails sent with no opens or clicks — client held out of the marketing lane", EmailSunsetMinSends)
		return v, nil
	}
	// Phase 42 WhatsApp hygiene: a dead number (3+ failed deliveries, none
	// confirmed since) holds the marketing lane — Meta's quality rating
	// punishes re-mailing dead numbers the way mailbox providers punish
	// bounces. And a client sent WaSunsetMinSends marketing messages with
	// zero replies ever sunsets out; one reply resets it. Both advisory.
	if channel == model.OutreachChannelWhatsApp {
		if svc.waUnreachable(ctx, tenantID, clientID) {
			v.Allowed = false
			v.WaUnreachable = true
			v.Reason = fmt.Sprintf("whatsapp unreachable: %d+ deliveries failed with none confirmed since — verify the client's number", WaUnreachableMinFailures)
			return v, nil
		}
		if svc.waSunsetApplies(ctx, tenantID, clientID) {
			v.Allowed = false
			v.Reason = fmt.Sprintf("sunset: %d+ WhatsApp marketing messages with no reply ever — client held out of the marketing lane", WaSunsetMinSends)
			return v, nil
		}
	}
	if v.MarketingConsent {
		return v, nil
	}
	// Phase 39 b2b-introduction exception (email only): an open lead sourced
	// from a b2b list (Phase 38 contact_basis) earns the client up to
	// EmailIntroMaxTouches introduction emails before the lane closes.
	if channel == model.OutreachChannelEmail {
		if ok, touches := svc.introLane(ctx, tenantID, clientID, sends); ok {
			v.Allowed, v.IntroLane = true, true
			v.IntroTouches, v.IntroMax = touches, EmailIntroMaxTouches
			return v, nil
		}
	}
	v.Allowed = false
	v.Reason = "marketing consent not granted"
	return v, nil
}

// emailHealth reads the tenant's trailing-window email deliverability from
// the attribution spine: attempted email sends (outreach_sent on the email
// channel) vs. ESP-reported bounces and complaints. Advisory by
// construction — a store error returns zeros and the guard stays inert; the
// consent and frequency gates remain the authoritative refusals.
func (svc *OutreachService) emailHealth(ctx context.Context, tenantID string, since time.Time) (sent, bounces, complaints int, rate float64) {
	a := svc.s.Store.Attribution()
	var err error
	if sent, err = a.CountSince(ctx, tenantID, "outreach_sent", model.OutreachChannelEmail, since); err != nil {
		return 0, 0, 0, 0
	}
	if bounces, err = a.CountSince(ctx, tenantID, "email_bounced", "", since); err != nil {
		return 0, 0, 0, 0
	}
	if complaints, err = a.CountSince(ctx, tenantID, "email_complained", "", since); err != nil {
		return 0, 0, 0, 0
	}
	if sent > 0 {
		rate = float64(bounces+complaints) / float64(sent)
	}
	return sent, bounces, complaints, rate
}

// introLane reports whether the b2b-introduction exception applies and how
// many introduction touches the client has already received (all-time, from
// the sends ledger — no separate counter that could drift).
func (svc *OutreachService) introLane(ctx context.Context, tenantID, clientID string, recent []model.OutreachSend) (bool, int) {
	leads, err := svc.s.Store.Leads().List(ctx, tenantID, 2000)
	if err != nil {
		return false, 0
	}
	eligible := false
	for i := range leads {
		l := &leads[i]
		if l.ClientID == clientID && l.Open() && l.ContactBasis == model.ContactBasisB2B {
			eligible = true
			break
		}
	}
	if !eligible {
		return false, 0
	}
	touches := 0
	if all, err := svc.s.Store.Outreach().SendsForClient(ctx, tenantID, clientID, time.Time{}); err == nil {
		for _, o := range all {
			if o.Channel == model.OutreachChannelEmail && o.Purpose == model.OutreachMarketing && o.Status == "sent" {
				touches++
			}
		}
	} else {
		// Fall back to the recent window — never grant the lane on a store
		// error we cannot count through.
		touches = len(recent)
	}
	return touches < EmailIntroMaxTouches, touches
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
	// Phase 39: channel normalization. Empty keeps the Phase 14 default
	// (whatsapp) so every existing caller and replayed key is unchanged.
	if in.Channel == "" {
		in.Channel = model.OutreachChannelWhatsApp
	}
	if in.Channel != model.OutreachChannelWhatsApp && in.Channel != model.OutreachChannelEmail {
		return nil, fmt.Errorf("%w: unknown outreach channel %q", httpx.ErrValidation, in.Channel)
	}
	in.Subject = strings.TrimSpace(in.Subject)
	if in.Channel == model.OutreachChannelEmail && in.Subject == "" {
		return nil, fmt.Errorf("%w: subject required for email sends", httpx.ErrValidation)
	}
	if in.TemplateID != "" {
		rendered, err := svc.renderTemplate(ctx, tenantID, in)
		if err != nil {
			return nil, err
		}
		in.Body = rendered
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

	o := &model.OutreachSend{
		ID: NewID(), TenantID: tenantID, ClientID: in.ClientID,
		Channel: in.Channel, Purpose: in.Purpose, Body: in.Body, Subject: in.Subject,
		Template: in.Template, Params: in.Params,
		IdempotencyKey: in.IdempotencyKey, CreatedAt: svc.s.Now(),
	}

	// Gates: consent + frequency cap + erasure. A gated send is recorded as
	// "held" — the decision is auditable, and a replayed key returns the
	// same verdict.
	v, err := svc.Verdict(ctx, tenantID, in.ClientID, in.Purpose, in.Channel)
	if err != nil {
		return nil, err
	}
	if !v.Allowed {
		o.Status, o.Detail = "held", v.Reason
		return svc.record(ctx, o)
	}

	// Channel recipient + payload shaping.
	var recipient, unsubURL string
	switch in.Channel {
	case model.OutreachChannelEmail:
		if cl.Email == "" {
			return nil, fmt.Errorf("%w: client has no email on file", httpx.ErrValidation)
		}
		recipient = cl.Email
		if in.Purpose == model.OutreachMarketing {
			// Mandated server-side: every marketing email carries the
			// sender identity and a one-click unsubscribe link. The
			// RECORDED body includes it — provenance shows exactly what
			// was sent, and no template or operator can omit it.
			unsubURL = svc.s.PublicBase() + "/v1/unsub/" + svc.s.Trips.UnsubTokenFor(tenantID, cl.ID)
			t, terr := svc.s.Store.Tenants().Get(ctx, tenantID)
			tenantName := ""
			if terr == nil {
				tenantName = t.Name
			}
			in.Body = withUnsubFooter(in.Body, tenantName, unsubURL)
			o.Body = in.Body
			if v.IntroLane {
				o.Detail = fmt.Sprintf("b2b introduction touch %d of %d (no marketing consent yet)", v.IntroTouches+1, EmailIntroMaxTouches)
			}
		}
	default:
		if cl.Phone == "" {
			return nil, fmt.Errorf("%w: client has no phone on file", httpx.ErrValidation)
		}
		recipient = cl.Phone
	}

	// Phase 35: deferral. An explicit future schedule wins; otherwise
	// marketing sends defer to the tenant's quiet-hours window end and/or
	// the client's optimized send hour. Service sends never defer —
	// a safety message at 02:00 still goes at 02:00.
	when := in.ScheduledFor
	if when == nil && in.Purpose == model.OutreachMarketing {
		when = svc.deferral(ctx, tenantID, in.ClientID)
	}
	if when != nil && when.After(svc.s.Now()) {
		o.Status, o.ScheduledFor = "scheduled", when
		if o.Detail == "" {
			o.Detail = "scheduled for " + when.UTC().Format(time.RFC3339)
		}
		return svc.record(ctx, o)
	}

	// Deliver through the channel adapter (freeform inside the 24h session,
	// approved template outside it; the adapter decides and may hold).
	status, detail, ref := svc.deliver(ctx, tenantID, in.Channel, recipient, in, unsubURL)
	o.Status, o.ExternalRef = status, ref
	if detail != "" {
		if o.Detail != "" {
			o.Detail += "; " + detail
		} else {
			o.Detail = detail
		}
	}
	rec, err := svc.record(ctx, o)
	if err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "outreach", o.ID, "outreach.sent",
		map[string]any{"client_id": o.ClientID, "purpose": o.Purpose,
			"channel": o.Channel, "status": o.Status, "external_ref": o.ExternalRef})
	svc.s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "outreach_sent", Channel: o.Channel,
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

// deliver calls the channel adapter. Advisory by construction: any adapter
// failure yields status "failed" with the cause in detail — never an error,
// so the send record always lands.
func (svc *OutreachService) deliver(ctx context.Context, tenantID, channel, recipient string, in SendOutreachInput, unsubURL string) (status, detail, externalRef string) {
	if channel == model.OutreachChannelEmail {
		return svc.deliverEmail(ctx, tenantID, recipient, in, unsubURL)
	}
	return svc.deliverWhatsApp(ctx, tenantID, recipient, in)
}

// deliverWhatsApp calls the WA adapter /v1/send.
func (svc *OutreachService) deliverWhatsApp(ctx context.Context, tenantID, phone string, in SendOutreachInput) (status, detail, externalRef string) {
	if svc.waURL == "" {
		return "failed", "channel adapter not configured (CE_WHATSAPP_ADAPTER_URL)", ""
	}
	payload, _ := json.Marshal(map[string]any{
		"tenant_id": tenantID, "contact": phone,
		"body": in.Body, "template": in.Template, "params": in.Params,
	})
	return postToAdapter(ctx, svc.waURL, payload)
}

// deliverEmail calls the email adapter /v1/send (Phase 39). The body
// already carries the mandated unsubscribe footer for marketing sends;
// unsubscribe_url goes separately so the adapter can set the
// List-Unsubscribe header (one-click, RFC 8058 signal).
func (svc *OutreachService) deliverEmail(ctx context.Context, tenantID, email string, in SendOutreachInput, unsubURL string) (status, detail, externalRef string) {
	if svc.emailURL == "" {
		return "failed", "email adapter not configured (CE_EMAIL_ADAPTER_URL)", ""
	}
	payload, _ := json.Marshal(map[string]any{
		"tenant_id": tenantID, "to": email,
		"subject": in.Subject, "body": in.Body,
		"unsubscribe_url": unsubURL, "client_id": in.ClientID,
	})
	return postToAdapter(ctx, svc.emailURL, payload)
}

// postToAdapter posts one outbound payload to a channel adapter and maps
// the response to (status, detail, externalRef).
func postToAdapter(ctx context.Context, baseURL string, payload []byte) (status, detail, externalRef string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(baseURL, "/")+"/v1/send", strings.NewReader(string(payload)))
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
		// Email adapters return the RFC message id at top level.
		MessageID string `json:"message_id"`
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
	if ar.MessageID != "" {
		externalRef = ar.MessageID
	}
	return "sent", ar.Mode, externalRef
}

// withUnsubFooter appends the mandated marketing footer. Plain text — the
// adapter wraps it into the MIME structure.
func withUnsubFooter(body, tenantName, unsubURL string) string {
	if tenantName == "" {
		tenantName = "Your concierge"
	}
	return strings.TrimRight(body, "\n") + "\n\n—\n" + tenantName + "\nUnsubscribe from these messages: " + unsubURL + "\n"
}

// Sends lists recent outreach sends (console growth board).
func (svc *OutreachService) Sends(ctx context.Context, tenantID string, limit int) ([]model.OutreachSend, error) {
	return svc.s.Store.Outreach().ListSends(ctx, tenantID, limit)
}

// ---------------------------------------------------------- Phase 35: scheduling

// deferral computes a marketing send's delivery time under the tenant's
// quiet hours (O7) and send-time optimization (O6). Nil = send now.
func (svc *OutreachService) deferral(ctx context.Context, tenantID, clientID string) *time.Time {
	t, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil
	}
	cfg := t.OutreachConfig
	now := svc.s.Now()
	var when time.Time
	if cfg.OptimizeSendTime {
		when = svc.preferredHour(ctx, tenantID, clientID, now)
	}
	if q := quietDefer(cfg, now); q.After(when) {
		when = q
	}
	if when.IsZero() || !when.After(now) {
		return nil
	}
	return &when
}

// quietDefer returns the end of the tenant's quiet-hours window when now
// falls inside it (O7). The window wraps midnight (e.g. 21→8). Both hours
// zero = disabled. A malformed config (equal or out-of-range hours) is
// ignored — it degrades to no deferral rather than blocking sends.
func quietDefer(cfg model.OutreachConfig, now time.Time) time.Time {
	start, end := cfg.QuietStartHour, cfg.QuietEndHour
	if start == end || start < 0 || start > 23 || end < 0 || end > 23 {
		return time.Time{}
	}
	h := now.Hour()
	inQuiet := false
	if start < end {
		inQuiet = h >= start && h < end
	} else {
		inQuiet = h >= start || h < end
	}
	if !inQuiet {
		return time.Time{}
	}
	d := time.Date(now.Year(), now.Month(), now.Day(), end, 0, 0, 0, now.Location())
	if !d.After(now) {
		d = d.Add(24 * time.Hour)
	}
	return d
}

// preferredHour derives the client's most responsive send hour from their
// own booking history (O6): the modal CreatedAt hour of their bookings,
// at least one hour in the future. No history = zero (no preference — the
// send is not deferred). This is honest optimization: it uses the
// client's real behavior, never a fabricated "best time".
func (svc *OutreachService) preferredHour(ctx context.Context, tenantID, clientID string, now time.Time) time.Time {
	bookings, err := svc.s.Store.Bookings().List(ctx, tenantID)
	if err != nil {
		return time.Time{}
	}
	reqClient := map[string]string{}
	var hours [24]int
	found := false
	for _, b := range bookings {
		cid, ok := reqClient[b.RequestID]
		if !ok {
			sr, err := svc.s.Store.Requests().Get(ctx, tenantID, b.RequestID)
			if err != nil {
				continue
			}
			cid = sr.ClientID
			reqClient[b.RequestID] = cid
		}
		if cid == clientID {
			hours[b.CreatedAt.Hour()]++
			found = true
		}
	}
	if !found {
		return time.Time{}
	}
	best, bestN := 10, 0
	for h, n := range hours {
		if n > bestN {
			best, bestN = h, n
		}
	}
	d := time.Date(now.Year(), now.Month(), now.Day(), best, 0, 0, 0, now.Location())
	if !d.After(now.Add(time.Hour)) {
		d = d.Add(24 * time.Hour)
	}
	return d
}

// RunDue delivers scheduled sends whose time has come (Phase 35 sweep).
// Consent and erasure are RE-VALIDATED at delivery time — a client who
// withdrew consent between scheduling and delivery is held, not sent.
// Advisory per send: one failure never aborts the sweep. Returns the
// number of due sends attempted.
func (svc *OutreachService) RunDue(ctx context.Context, tenantID string) (int, error) {
	sends, err := svc.s.Store.Outreach().ListSends(ctx, tenantID, 2000)
	if err != nil {
		return 0, err
	}
	now := svc.s.Now()
	attempted := 0
	for _, o := range sends {
		if o.Status != "scheduled" || o.ScheduledFor == nil || o.ScheduledFor.After(now) {
			continue
		}
		attempted++
		channel := o.Channel
		if channel == "" {
			channel = model.OutreachChannelWhatsApp
		}
		finish := func(status, detail, ref string) {
			if uerr := svc.s.Store.Outreach().UpdateSendResult(ctx, tenantID, o.ID, status, detail, ref); uerr != nil {
				svc.s.Events.Emit(ctx, tenantID, "outreach", o.ID, "outreach.sweep_error",
					map[string]any{"error": uerr.Error()})
			}
		}
		v, err := svc.Verdict(ctx, tenantID, o.ClientID, o.Purpose, channel)
		if err != nil {
			continue // transient store error — leave scheduled, retry next sweep
		}
		if !v.Allowed {
			finish("held", "at delivery: "+v.Reason, "")
			continue
		}
		cl, err := svc.s.Store.Clients().Get(ctx, tenantID, o.ClientID)
		if err != nil {
			finish("failed", "at delivery: client unavailable", "")
			continue
		}
		var recipient, unsubURL string
		if channel == model.OutreachChannelEmail {
			if cl.Email == "" {
				finish("failed", "at delivery: client email unavailable", "")
				continue
			}
			recipient = cl.Email
			if o.Purpose == model.OutreachMarketing {
				unsubURL = svc.s.PublicBase() + "/v1/unsub/" + svc.s.Trips.UnsubTokenFor(tenantID, cl.ID)
			}
		} else {
			if cl.Phone == "" {
				finish("failed", "at delivery: client phone unavailable", "")
				continue
			}
			recipient = cl.Phone
		}
		status, detail, ref := svc.deliver(ctx, tenantID, channel, recipient, SendOutreachInput{
			ClientID: o.ClientID, Purpose: o.Purpose, Body: o.Body, Subject: o.Subject,
			Template: o.Template, Params: o.Params, Channel: channel,
		}, unsubURL)
		finish(status, detail, ref)
		svc.s.Events.Emit(ctx, tenantID, "outreach", o.ID, "outreach.scheduled_delivered",
			map[string]any{"client_id": o.ClientID, "status": status})
		// Phase 40: sweep-delivered sends join the attribution spine too —
		// the deliverability guard's denominator counts every attempted
		// delivery, not just interactive ones (same shape as Send).
		svc.s.Events.Track(ctx, &model.AttributionEvent{
			TenantID: tenantID, Kind: "outreach_sent", Channel: channel,
			Campaign: o.Purpose, ClientID: o.ClientID,
		})
	}
	return attempted, nil
}

// ---------------------------------------------------------- Phase 21: templates

// UpsertTemplate creates or updates a tenant-managed outreach template,
// keyed by (tenant, name) — the console save button is naturally idempotent.
func (svc *OutreachService) UpsertTemplate(ctx context.Context, tenantID, name, purpose, body string) (*model.MessageTemplate, error) {
	name = strings.TrimSpace(name)
	body = strings.TrimSpace(body)
	if len(name) < 2 || len(name) > 64 || body == "" {
		return nil, httpx.ErrValidation
	}
	if purpose == "" {
		purpose = model.TemplatePurposeAny
	}
	if purpose != model.OutreachService && purpose != model.OutreachMarketing && purpose != model.TemplatePurposeAny {
		return nil, httpx.ErrValidation
	}
	now := svc.s.Now()
	t := &model.MessageTemplate{
		ID: NewID(), TenantID: tenantID, Name: name,
		Purpose: purpose, Body: body, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.s.Store.Templates().Upsert(ctx, t); err != nil {
		return nil, err
	}
	svc.s.Audit.Log(ctx, tenantID, "api", "outreach.template_saved", "message_template", t.ID,
		map[string]any{"name": t.Name, "purpose": t.Purpose})
	return t, nil
}

func (svc *OutreachService) ListTemplates(ctx context.Context, tenantID string) ([]model.MessageTemplate, error) {
	return svc.s.Store.Templates().List(ctx, tenantID)
}

func (svc *OutreachService) DeleteTemplate(ctx context.Context, tenantID, id string) error {
	if err := svc.s.Store.Templates().Delete(ctx, tenantID, id); err != nil {
		return err
	}
	svc.s.Audit.Log(ctx, tenantID, "api", "outreach.template_deleted", "message_template", id, nil)
	return nil
}

// renderTemplate resolves a template send: load, purpose-guard, substitute.
func (svc *OutreachService) renderTemplate(ctx context.Context, tenantID string, in SendOutreachInput) (string, error) {
	tmpl, err := svc.s.Store.Templates().Get(ctx, tenantID, in.TemplateID)
	if err != nil {
		return "", err
	}
	purpose := in.Purpose
	if purpose == "" {
		purpose = model.OutreachService
	}
	if tmpl.Purpose != model.TemplatePurposeAny && tmpl.Purpose != purpose {
		// A marketing template sent as "service" would bypass the consent
		// and frequency gates — refuse loudly.
		return "", fmt.Errorf("%w: template purpose %q cannot be sent as %q",
			httpx.ErrValidation, tmpl.Purpose, purpose)
	}
	cl, err := svc.s.Store.Clients().Get(ctx, tenantID, in.ClientID)
	if err != nil {
		return "", err
	}
	t, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return "", err
	}
	return RenderTemplate(tmpl.Body, cl, t), nil
}

// RenderTemplate substitutes the supported variables. Unknown placeholders
// are left as-is — visible in the audit trail, never silently dropped.
func RenderTemplate(body string, cl *model.Client, t *model.Tenant) string {
	name := cl.FullName
	if name == "" {
		name = cl.Phone
	}
	out := strings.ReplaceAll(body, "{{client_name}}", name)
	out = strings.ReplaceAll(out, "{{tenant_name}}", t.Name)
	return out
}
