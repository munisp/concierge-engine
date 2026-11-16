package domain

// Phase 44: consent evidence ledger (GDPR Art. 7 — "the controller shall
// be able to demonstrate that the data subject has consented").
//
// The consent map on the client row is the CURRENT state; the
// consent_events ledger is the PROOF — every actual change, attributed to
// the surface that caused it (registration defaults, console toggle, STOP
// keyword, unsub link, reopt link, lead capture, ESP complaint, DSAR
// erasure). The ledger is append-only and change-only: a replayed write
// with the same value adds no row, so idempotent retries stay clean.
//
// Rows survive erasure deliberately: they carry only the pseudonymous
// client_id plus scope/granted/source — no personal data — and they are
// exactly what a regulator asks for when a data subject claims they never
// consented.

import (
	"context"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// SetConsentSourced is SetConsent plus an evidence row naming the surface
// that changed the scope. SetConsent delegates with source "console" —
// every caller that knows a narrower source should call this directly.
// A no-change write (same value, idempotent replay) records nothing.
func (svc *ClientService) SetConsentSourced(ctx context.Context, tenantID, clientID, scope string, granted bool, source, detail string) (*model.Client, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		return nil, httpx.ErrValidation
	}
	if source == "" {
		source = model.ConsentSourceConsole
	}
	if !model.ConsentSourceValid(source) {
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
	prev, existed := c.Consent[scope].(bool)
	if existed && prev == granted {
		return c, nil // no change — replay adds no evidence row
	}
	c.Consent[scope] = granted
	if err := svc.s.Store.Clients().Upsert(ctx, c); err != nil {
		return nil, err
	}
	if err := svc.s.Store.ConsentEvents().Record(ctx, &model.ConsentEvent{
		ID: NewID(), TenantID: tenantID, ClientID: clientID,
		Scope: scope, Granted: granted, Source: source, Detail: detail,
		CreatedAt: svc.s.Now(),
	}); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "client", clientID, "client.consent_updated",
		map[string]any{"scope": scope, "granted": granted, "source": source})
	svc.s.Audit.Log(ctx, tenantID, "api", "client.consent_updated", "client", clientID,
		map[string]any{"scope": scope, "granted": granted, "source": source})
	return c, nil
}

// ConsentHistory returns a client's evidence ledger newest-first — the
// regulator-facing answer to "prove this person consented".
func (svc *ClientService) ConsentHistory(ctx context.Context, tenantID, clientID string, limit int) ([]model.ConsentEvent, error) {
	if _, err := svc.s.Store.Clients().Get(ctx, tenantID, clientID); err != nil {
		return nil, err // unknown/cross-tenant id — never leak existence
	}
	return svc.s.Store.ConsentEvents().ListForClient(ctx, tenantID, clientID, limit)
}

// recordRegistrationDefaults writes the genesis rows of a client's
// ledger: the jurisdiction-aware defaults ResolveV2 created it with.
// Evidence that the STARTING state was the lawful default for the
// client's region is half the Art. 7 answer.
func (svc *ClientService) recordRegistrationDefaults(ctx context.Context, tenantID, clientID string, consent map[string]any) {
	for scope, v := range consent {
		granted, ok := v.(bool)
		if !ok {
			continue
		}
		// Advisory: evidence must never block registration.
		_ = svc.s.Store.ConsentEvents().Record(ctx, &model.ConsentEvent{
			ID: NewID(), TenantID: tenantID, ClientID: clientID,
			Scope: scope, Granted: granted,
			Source:    model.ConsentSourceRegistrationDefault,
			Detail:    "jurisdiction default at registration",
			CreatedAt: svc.s.Now(),
		})
	}
}

// recordErasureRevocations writes dsar_erasure rows for every scope the
// tombstoning just revoked. Runs inside EraseWithEvidence after the
// consent wipe.
func (svc *ClientService) recordErasureRevocations(ctx context.Context, tenantID, clientID string, before map[string]any) {
	for scope, v := range before {
		granted, ok := v.(bool)
		if !ok || !granted {
			continue // only grants need a revocation row
		}
		_ = svc.s.Store.ConsentEvents().Record(ctx, &model.ConsentEvent{
			ID: NewID(), TenantID: tenantID, ClientID: clientID,
			Scope: scope, Granted: false,
			Source:    model.ConsentSourceDSARErasure,
			Detail:    "revoked by erasure cascade",
			CreatedAt: svc.s.Now(),
		})
	}
}

// ClientExportWithEvidence is the Art. 15 export plus the consent ledger.
// dsar.go's ClientExport is untouched; this wrapper attaches the evidence
// section so the access report can actually demonstrate consent.
type ClientExportWithEvidence struct {
	*ClientExport
	ConsentHistory []model.ConsentEvent `json:"consent_history"`
}

// ExportWithEvidence wraps Export with the consent evidence section.
func (svc *ClientService) ExportWithEvidence(ctx context.Context, tenantID, id string) (*ClientExportWithEvidence, error) {
	exp, err := svc.Export(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	out := &ClientExportWithEvidence{ClientExport: exp}
	if evs, err := svc.s.Store.ConsentEvents().ListForClient(ctx, tenantID, id, 1000); err == nil {
		out.ConsentHistory = evs
	}
	return out, nil
}

// EraseWithEvidence is Erase plus dsar_erasure evidence rows for every
// granted scope the cascade revokes. dsar.go's Erase is untouched; the
// erase endpoint routes through this wrapper so the ledger records the
// revocation even though the row itself becomes a tombstone.
//
// Phase 46: erasure also scrubs the client's conversation message bodies
// immediately — Art. 17 says content goes NOW, not at the next retention
// window. Advisory fail-open: erasure itself has already committed, so a
// scrub failure surfaces as an event rather than an error.
func (svc *ClientService) EraseWithEvidence(ctx context.Context, tenantID, id string) (*model.Client, error) {
	before, err := svc.s.Store.Clients().Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	prevConsent := map[string]any{}
	for k, v := range before.Consent {
		prevConsent[k] = v
	}
	c, err := svc.Erase(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	svc.recordErasureRevocations(ctx, tenantID, id, prevConsent)
	svc.s.Retention.scrubClientMessages(ctx, tenantID, id)
	return c, nil
}
