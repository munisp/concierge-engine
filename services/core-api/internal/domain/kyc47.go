package domain

// Phase 47: KYC / identity verification lane.
//
// Client.KYCStatus existed from Phase 1 as a write-only field — set to
// "none" at registration, never checked, never advanced. This service
// makes it real:
//
//   - a verification CASE per attempt (open → submit → decide), one open
//     case per (client, level), terminal states absorbing;
//   - the client status ADVANCES on approval only, never regresses;
//   - BookingService.Confirm gates high-value quotes on the tenant's
//     KycConfig thresholds — the field finally has a reader;
//   - stale undecided cases expire (documents go stale; a case that was
//     never finished must not be decidible forever);
//   - erasure scrubs document references immediately while the case row
//     survives as the AML accountability record (same exempt class as
//     financials).
//
// Provider honesty: the only provider today is "manual" — operator review.
// No third-party KYC vendor is wired and nothing here pretends otherwise;
// external providers (Smile Identity, Onfido) attach behind the same case
// shape when a tenant contracts one.

import (
	"context"
	"errors"
	"fmt"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// Platform KYC defaults.
const (
	// KycDefaultStandardAboveMinor: €10k — the AML-adjacent due-diligence
	// line for high-value concierge services.
	KycDefaultStandardAboveMinor int64 = 1_000_000
	// KycDefaultEnhancedAboveMinor: €100k — jets, yachts, multi-week
	// engagements.
	KycDefaultEnhancedAboveMinor int64 = 10_000_000
	// KycDefaultCaseExpiryDays: document freshness window for an
	// undecided case.
	KycDefaultCaseExpiryDays = 30
)

// KycProviderManual is the operator-review lane — the only verification
// rail until a tenant contracts an external KYC vendor.
const KycProviderManual = "manual"

// KycDocumentTypes is the closed vocabulary of document types the platform
// records. The document itself NEVER enters the platform — only the fact
// that one was sighted and the operator's reference to where it is filed.
var KycDocumentTypes = map[string]bool{
	"passport":         true,
	"national_id":      true,
	"driving_licence":  true,
	"company_registry": true, // ENHANCED corporate bookings
	"proof_of_address": true,
	"source_of_wealth": true, // ENHANCED
}

type KycService struct{ s *Services }

// RequiredLevel resolves the verification level a booking of amountMinor
// demands under the tenant's config. "" = no requirement.
func RequiredKycLevel(cfg model.KycConfig, amountMinor int64) string {
	if cfg.Disabled {
		return ""
	}
	enhanced := cfg.EnhancedAboveMinor
	if enhanced == 0 {
		enhanced = KycDefaultEnhancedAboveMinor
	}
	if enhanced > 0 && amountMinor >= enhanced {
		return model.KycLevelEnhanced
	}
	standard := cfg.StandardAboveMinor
	if standard == 0 {
		standard = KycDefaultStandardAboveMinor
	}
	if standard > 0 && amountMinor >= standard {
		return model.KycLevelStandard
	}
	return ""
}

// clientStatusFor maps a case level to the client-facing status string.
func clientStatusFor(level string) string {
	switch level {
	case model.KycLevelEnhanced:
		return "enhanced"
	default:
		return "standard"
	}
}

// Open registers a verification case for the client at the level.
// Idempotent on the caller key: a replay returns the original case. A
// second open at the same level while one is in flight is 409 — the
// caller reloads the open case via ListForClient.
func (svc *KycService) Open(ctx context.Context, tenantID, clientID, level, idem string) (*model.KycCase, error) {
	if !model.KycLevelValid(level) || idem == "" {
		return nil, httpx.ErrValidation
	}
	c, err := svc.s.Store.Clients().Get(ctx, tenantID, clientID)
	if err != nil {
		return nil, err
	}
	if c.Erased() {
		return nil, fmt.Errorf("%w: erased client", httpx.ErrConflict)
	}
	if model.KycLevelRank(c.KYCStatus) >= model.KycLevelRank(clientStatusFor(level)) {
		return nil, fmt.Errorf("%w: client already verified at this level", httpx.ErrConflict)
	}
	kc := &model.KycCase{
		ID: NewID(), TenantID: tenantID, ClientID: clientID, Level: level,
		Status: model.KycStatusPending, Provider: KycProviderManual,
		IdempotencyKey: idem, CreatedAt: svc.s.Now(), UpdatedAt: svc.s.Now(),
	}
	if err := svc.s.Store.KycCases().Create(ctx, kc); err != nil {
		if errors.Is(err, httpx.ErrConflict) {
			// Idempotent replay: same key returns the original case.
			if orig, gerr := svc.s.Store.KycCases().GetByIdem(ctx, tenantID, idem); gerr == nil {
				return orig, nil
			}
		}
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "kyc", kc.ID, "kyc.case_opened",
		map[string]any{"client_id": clientID, "level": level})
	svc.s.Audit.Log(ctx, tenantID, "system:kyc", "kyc.open", "kyc_case", kc.ID,
		map[string]any{"client_id": clientID, "level": level})
	return kc, nil
}

// Submit records the document metadata and moves the case to IN_REVIEW.
// The document itself never enters the platform — document_ref is the
// operator's pointer into the tenant's secure channel.
func (svc *KycService) Submit(ctx context.Context, tenantID, caseID, documentType, documentRef string) (*model.KycCase, error) {
	if !KycDocumentTypes[documentType] || documentRef == "" || len(documentRef) > 200 {
		return nil, httpx.ErrValidation
	}
	kc, err := svc.s.Store.KycCases().Get(ctx, tenantID, caseID)
	if err != nil {
		return nil, err
	}
	if kc.Status == model.KycStatusInReview {
		return kc, nil // replay: same submission re-arrived
	}
	if kc.Status != model.KycStatusPending {
		return nil, httpx.ErrConflict // terminal states are absorbing
	}
	kc.Status = model.KycStatusInReview
	kc.DocumentType = documentType
	kc.DocumentRef = documentRef
	kc.SubmittedAt = svc.s.Now()
	kc.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.KycCases().Update(ctx, kc); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "kyc", kc.ID, "kyc.case_submitted",
		map[string]any{"client_id": kc.ClientID, "level": kc.Level, "document_type": documentType})
	return kc, nil
}

// Decide closes an IN_REVIEW case. Approval advances the client status
// (never regresses it — an ENHANCED client approving a STANDARD case stays
// ENHANCED). Rejection requires a reason; the client status is unchanged
// so a fresh case can be opened. Terminal replay with the SAME decision is
// a no-op; a conflicting re-decision is 409.
func (svc *KycService) Decide(ctx context.Context, tenantID, caseID string, approve bool, reason, decidedBy string) (*model.KycCase, error) {
	if !approve && reason == "" {
		return nil, httpx.ErrValidation
	}
	if decidedBy == "" {
		decidedBy = "operator"
	}
	kc, err := svc.s.Store.KycCases().Get(ctx, tenantID, caseID)
	if err != nil {
		return nil, err
	}
	if kc.KycTerminal() {
		same := (kc.Status == model.KycStatusApproved) == approve
		if same {
			return kc, nil // absorbing replay
		}
		return nil, httpx.ErrConflict
	}
	if kc.Status != model.KycStatusInReview {
		return nil, fmt.Errorf("%w: case not submitted", httpx.ErrConflict)
	}
	if approve {
		kc.Status = model.KycStatusApproved
	} else {
		kc.Status = model.KycStatusRejected
		kc.DecisionReason = reason
	}
	kc.DecidedBy = decidedBy
	kc.DecidedAt = svc.s.Now()
	kc.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.KycCases().Update(ctx, kc); err != nil {
		return nil, err
	}
	if approve {
		svc.syncClientStatus(ctx, tenantID, kc)
	}
	svc.s.Events.Emit(ctx, tenantID, "kyc", kc.ID, "kyc.decided",
		map[string]any{"client_id": kc.ClientID, "level": kc.Level,
			"approved": approve, "decided_by": decidedBy})
	svc.s.Audit.Log(ctx, tenantID, decidedBy, "kyc.decide", "kyc_case", kc.ID,
		map[string]any{"client_id": kc.ClientID, "level": kc.Level, "approved": approve, "reason": reason})
	return kc, nil
}

// syncClientStatus advances Client.KYCStatus on approval. Advisory
// fail-open with an explicit event: the case is already terminal, so a
// sync failure must page, not silently drop — the next Decide replay
// (absorbing) will NOT retry the sync.
func (svc *KycService) syncClientStatus(ctx context.Context, tenantID string, kc *model.KycCase) {
	c, err := svc.s.Store.Clients().Get(ctx, tenantID, kc.ClientID)
	if err != nil {
		svc.s.Events.Emit(ctx, tenantID, "kyc", kc.ID, "kyc.status_sync_failed",
			map[string]any{"client_id": kc.ClientID, "error": err.Error()})
		return
	}
	next := clientStatusFor(kc.Level)
	if model.KycLevelRank(c.KYCStatus) >= model.KycLevelRank(next) {
		return // never regress
	}
	c.KYCStatus = next
	if err := svc.s.Store.Clients().Upsert(ctx, c); err != nil {
		svc.s.Events.Emit(ctx, tenantID, "kyc", kc.ID, "kyc.status_sync_failed",
			map[string]any{"client_id": kc.ClientID, "error": err.Error()})
	}
}

// GateCheck answers the booking gate: does this client satisfy the
// verification level a quote of amountMinor demands? Returns the required
// level ("" = no requirement) and whether the client satisfies it.
func (svc *KycService) GateCheck(ctx context.Context, tenantID, clientID string, amountMinor int64) (string, bool, error) {
	t, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return "", false, err
	}
	required := RequiredKycLevel(t.KycConfig, amountMinor)
	if required == "" {
		return "", true, nil
	}
	c, err := svc.s.Store.Clients().Get(ctx, tenantID, clientID)
	if err != nil {
		return "", false, err
	}
	return required, model.KycLevelRank(c.KYCStatus) >= model.KycLevelRank(clientStatusFor(required)), nil
}

// ExpireStale flips undecided cases older than the tenant's case-expiry
// window to EXPIRED. Runs hourly next to the retention sweep and on demand
// via the operator endpoint. Idempotent by state transition.
func (svc *KycService) ExpireStale(ctx context.Context, tenantID string) (int, error) {
	t, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	if t.KycConfig.Disabled {
		return 0, nil
	}
	days := t.KycConfig.CaseExpiryDays
	if days == 0 {
		days = KycDefaultCaseExpiryDays
	}
	if days < 0 {
		return 0, nil // never expire
	}
	n, err := svc.s.Store.KycCases().ExpireBefore(ctx, tenantID, svc.s.Now().AddDate(0, 0, -days))
	if err != nil {
		return 0, err
	}
	if n > 0 {
		svc.s.Audit.Log(ctx, tenantID, "system:kyc", "kyc.expire", "tenant", tenantID,
			map[string]any{"expired": n})
	}
	return n, nil
}

// scrubDocumentRefs is the Art. 17 erasure companion: document pointers go
// NOW; the case row (level, decision, timestamps) survives as the AML
// record. Advisory fail-open — erasure itself has already committed.
func (svc *KycService) scrubDocumentRefs(ctx context.Context, tenantID, clientID string) {
	if _, err := svc.s.Store.KycCases().ScrubDocumentRefsForClient(ctx, tenantID, clientID); err != nil {
		svc.s.Events.Emit(ctx, tenantID, "kyc", clientID, "kyc.scrub_failed",
			map[string]any{"client_id": clientID})
	}
}

func (svc *KycService) Get(ctx context.Context, tenantID, id string) (*model.KycCase, error) {
	return svc.s.Store.KycCases().Get(ctx, tenantID, id)
}

func (svc *KycService) List(ctx context.Context, tenantID, status string, limit int) ([]model.KycCase, error) {
	if status != "" && status != model.KycStatusPending && status != model.KycStatusInReview &&
		status != model.KycStatusApproved && status != model.KycStatusRejected && status != model.KycStatusExpired {
		return nil, httpx.ErrValidation
	}
	return svc.s.Store.KycCases().List(ctx, tenantID, status, limit)
}

func (svc *KycService) ListForClient(ctx context.Context, tenantID, clientID string) ([]model.KycCase, error) {
	return svc.s.Store.KycCases().ListForClient(ctx, tenantID, clientID)
}
