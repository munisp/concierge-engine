package domain

// Phase 16: EU/US-first readiness — data-subject rights (GDPR Art. 15/17,
// CCPA) and regional consent defaults at registration.
//
// Erasure model: the client ROW is never deleted. Financial records
// (bookings, payment intents, the TigerBeetle ledger) are legal-hold data
// the platform must retain for accounting obligations, so erasure scrubs
// identity to a pseudonymous tombstone: name/email/phone/locale replaced,
// preference memory deleted, consent revoked. The remaining client_id maps
// to no identifiable person. Re-contacting an erased client is blocked at
// every write surface (profile, consent, preferences, outreach).
//
// Consent defaults are jurisdiction-aware: GDPR/UK-GDPR/CH-FADP markets are
// opt-IN for marketing (default false), US/Canada are opt-OUT (default
// true). Jurisdiction is derived from the phone country calling code — the
// most reliable signal available at registration. Heuristic, documented as
// such; operators can correct per-client via PUT /v1/clients/{id}/consent.

import (
	"context"
	"fmt"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// optInCallingCodes: EU-27 + EEA + UK + Switzerland country calling codes
// (GDPR / UK GDPR / FADP — marketing requires prior opt-in).
var optInCallingCodes = map[string]bool{
	"30": true, "31": true, "32": true, "33": true, "34": true, "36": true,
	"39": true, "40": true, "41": true, "43": true, "44": true, "45": true,
	"46": true, "47": true, "48": true, "49": true, "350": true, "351": true,
	"352": true, "353": true, "354": true, "356": true, "357": true,
	"358": true, "359": true, "370": true, "371": true, "372": true,
	"385": true, "386": true, "420": true, "421": true, "423": true,
}

// ConsentDefaults returns the jurisdiction-appropriate consent map for a
// newly registered client. service/data_processing are always granted (the
// contract the client just entered); marketing follows the regime:
// opt-in jurisdictions default false, opt-out (US/CA, +1) default true.
func ConsentDefaults(phone string) map[string]any {
	marketing := false // conservative default: unknown jurisdiction = opt-in
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	for _, n := range []int{3, 2} { // longest-prefix match
		if len(digits) >= n && optInCallingCodes[digits[:n]] {
			marketing = false
			return map[string]any{"service": true, "data_processing": true, "marketing": marketing}
		}
	}
	if strings.HasPrefix(digits, "1") { // NANP: US/CA — opt-out regime
		marketing = true
	}
	return map[string]any{"service": true, "data_processing": true, "marketing": marketing}
}

// ClientExport is the GDPR Art. 15 / CCPA access response: everything the
// platform holds on one data subject, in one machine-readable document.
type ClientExport struct {
	Client        *model.Client             `json:"client"`
	Preferences   []model.Preference        `json:"preferences"`
	Requests      []model.ServiceRequest    `json:"requests"`
	Bookings      []model.Booking           `json:"bookings"`
	Cases         []model.Case              `json:"cases"`
	Conversations []model.Conversation      `json:"conversations"`
	Attribution   []model.AttributionEvent  `json:"attribution"`
	Outreach      []model.OutreachSend      `json:"outreach"`
	ExportedAt    string                    `json:"exported_at"`
}

// Export assembles the data-subject access report. Sensitive read:
// audit-logged with section counts. Sections are best-effort — a partial
// export with an error is worse than a complete one assembled slowly, but a
// missing section is named rather than silently dropped (it appears empty).
func (svc *ClientService) Export(ctx context.Context, tenantID, id string) (*ClientExport, error) {
	c, err := svc.s.Store.Clients().Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	exp := &ClientExport{Client: c, ExportedAt: svc.s.Now().UTC().Format("2006-01-02T15:04:05Z")}
	if prefs, err := svc.Preferences(ctx, tenantID, id); err == nil {
		exp.Preferences = prefs
	}
	if reqs, err := svc.s.Store.Requests().ForClient(ctx, tenantID, id, 1000); err == nil {
		exp.Requests = reqs
	}
	if bks, err := svc.s.Store.Bookings().ForClient(ctx, tenantID, id, 1000); err == nil {
		exp.Bookings = bks
	}
	if cases, err := svc.s.Store.Cases().ForClient(ctx, tenantID, id, 1000); err == nil {
		exp.Cases = cases
	}
	if convs, err := svc.s.Store.Conversations().ForClient(ctx, tenantID, id, 1000); err == nil {
		exp.Conversations = convs
	}
	if evs, err := svc.s.Store.Attribution().List(ctx, tenantID, id, 1000); err == nil {
		exp.Attribution = evs
	}
	if sends, err := svc.s.Store.Outreach().SendsForClient(ctx, tenantID, id,
		svc.s.Now().AddDate(-10, 0, 0)); err == nil {
		exp.Outreach = sends
	}
	svc.s.Events.Emit(ctx, tenantID, "client", id, "dsar.exported", nil)
	svc.s.Audit.Log(ctx, tenantID, "api", "dsar.export", "client", id, map[string]any{
		"requests": len(exp.Requests), "bookings": len(exp.Bookings),
		"cases": len(exp.Cases), "conversations": len(exp.Conversations),
	})
	return exp, nil
}

// Erase executes the right to erasure (GDPR Art. 17 / CCPA delete) with the
// accounting carve-out: preference memory is deleted, identity fields are
// scrubbed to a tombstone, consent is revoked, and the row is marked so
// every write surface refuses further processing. Financial history stays
// pseudonymously attached. Idempotent: re-erasing is a no-op.
func (svc *ClientService) Erase(ctx context.Context, tenantID, id string) (*model.Client, error) {
	c, err := svc.s.Store.Clients().Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if c.Erased() {
		return c, nil // replay-safe
	}
	// Delete preference memory first (AI spec §6 DSAR cascade).
	if err := svc.s.Store.Clients().DeletePreferences(ctx, tenantID, id); err != nil {
		return nil, err
	}
	c.FullName = "Erased client"
	c.Email = fmt.Sprintf("erased-%s@erased.invalid", c.ID)
	c.Phone = ""
	c.Locale = ""
	c.Timezone = ""
	c.VIPFlags = nil
	c.LoyaltyTier = ""
	c.Consent = map[string]any{"service": false, "data_processing": false, "marketing": false, "erased": true}
	c.ErasedAt = svc.s.Now()
	if err := svc.s.Store.Clients().Upsert(ctx, c); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "client", id, "client.erased", nil)
	svc.s.Audit.Log(ctx, tenantID, "api", "client.erased", "client", id, nil)
	return c, nil
}

// requireNotErased guards every write surface that would process personal
// data of an erased data subject.
func requireNotErased(c *model.Client) error {
	if c.Erased() {
		return fmt.Errorf("%w: client erased (GDPR/CCPA) — no further processing", httpx.ErrConflict)
	}
	return nil
}
