package domain

// Phase 45: client self-service consent center (GDPR Art. 7(3) — "the
// data subject shall have the right to withdraw his or her consent at any
// time ... It shall be as easy to withdraw as to give consent").
//
// Phase 44's ledger proves consent to a regulator; this phase hands the
// same ledger to the client. A consent/ HMAC capability token (same
// discipline as track/, unsub/, reopt/ — the token IS the credential, no
// tenant header, cross-family tokens rejected) opens a public page that
// shows the current scopes WITH their evidence timeline, flips any
// self-serviceable scope one-tap (every flip lands in the ledger with
// source "self_service"), and downloads a consent receipt. Marketing
// email footers carry the link next to the mandated unsubscribe.
//
// The "service" scope is deliberately not self-serviceable: booking
// confirmations and driver updates are the contract the client entered,
// not a preference. The page shows it locked with an explanation rather
// than offering a toggle that would silently break the service
// relationship. A client who wants everything gone has the erasure path
// (Phase 16/44), which the page links in copy.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

const consentTokenPrefix = "consent/"

// ConsentTokenFor mints the self-service consent-center token for a
// client. Same HMAC discipline as the unsub/reopt families.
func (svc *TripService) ConsentTokenFor(tenantID, clientID string) string {
	payload := consentTokenPrefix + tenantID + "/" + clientID
	mac := hmac.New(sha256.New, svc.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac.Sum(nil))
}

// ParseConsentToken verifies and extracts (tenantID, clientID). Tokens
// from other families are rejected — an unsubscribe or tracking link can
// never open the consent center.
func (svc *TripService) ParseConsentToken(token string) (string, string, error) {
	_, _, payload, err := svc.parseAny(token)
	if err != nil {
		return "", "", err
	}
	rest, ok := strings.CutPrefix(payload, consentTokenPrefix)
	if !ok {
		return "", "", httpx.ErrForbidden
	}
	return splitPayload(rest)
}

// consentScopeLocked reports whether a scope is visible but not
// self-serviceable. "service" covers booking-critical notifications —
// withdrawing it is an erasure/contract question, not a toggle.
func consentScopeLocked(scope string) bool { return scope == "service" }

// ConsentScopeState is one scope as the consent center renders it.
type ConsentScopeState struct {
	Scope   string `json:"scope"`
	Granted bool   `json:"granted"`
	Locked  bool   `json:"locked"`
	Label   string `json:"label"`
	Blurb   string `json:"blurb"`
}

// ConsentCenter is the view model for the public page: who the controller
// is, what each scope means, its current state, and the evidence trail
// behind it.
type ConsentCenter struct {
	TenantName string               `json:"tenant_name"`
	Erased     bool                 `json:"erased"`
	Scopes     []ConsentScopeState  `json:"scopes"`
	History    []model.ConsentEvent `json:"history"`
}

// consentScopeMeta gives the client-facing label + plain-language
// explanation for the platform's standard scopes. Unknown scopes render
// under their raw name — the map is presentation, not policy.
var consentScopeMeta = map[string]struct{ Label, Blurb string }{
	"service":         {"Service updates", "Booking confirmations, driver updates and trip changes. Required to deliver your bookings — this is not marketing."},
	"data_processing": {"Data processing", "Storing your profile and trip history so the concierge team can serve you."},
	"marketing":       {"Marketing", "Offers, news and win-back messages. Off means no marketing, ever — service messages are unaffected."},
}

// ConsentCenterByToken builds the consent-center view for the token's
// client. The page shows no PII beyond what the link holder already has
// (the tenant's name and their own consent state); the history rows are
// pseudonymous by design (Phase 44).
func (svc *ClientService) ConsentCenterByToken(ctx context.Context, token string) (*ConsentCenter, error) {
	tenantID, clientID, err := svc.s.Trips.ParseConsentToken(token)
	if err != nil {
		return nil, err
	}
	c, err := svc.s.Store.Clients().Get(ctx, tenantID, clientID)
	if err != nil {
		return nil, err
	}
	t, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	cc := &ConsentCenter{TenantName: t.Name, Erased: c.Erased()}
	keys := make([]string, 0, len(c.Consent))
	for k := range c.Consent {
		if k == "erased" {
			continue // tombstone marker, not a scope
		}
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { // locked scopes first, then alpha
		li, lj := consentScopeLocked(keys[i]), consentScopeLocked(keys[j])
		if li != lj {
			return li
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		granted, _ := c.Consent[k].(bool)
		meta := consentScopeMeta[k]
		st := ConsentScopeState{
			Scope: k, Granted: granted, Locked: consentScopeLocked(k),
			Label: meta.Label, Blurb: meta.Blurb,
		}
		if st.Label == "" {
			st.Label = k
		}
		cc.Scopes = append(cc.Scopes, st)
	}
	// Evidence timeline is advisory to the page: a store hiccup must not
	// take down the client's ability to SEE and CHANGE their consent.
	if evs, err := svc.s.Store.ConsentEvents().ListForClient(ctx, tenantID, clientID, 50); err == nil {
		cc.History = evs
	}
	return cc, nil
}

// ConsentSetByToken flips one scope from the consent center. Locked
// scopes and scopes the client does not have are refused; everything else
// flows through SetConsentSourced, so the flip is evidence-recorded with
// the self_service source and inherits change-only replay semantics (a
// double-click adds no row).
func (svc *ClientService) ConsentSetByToken(ctx context.Context, token, scope string, granted bool) (*model.Client, error) {
	tenantID, clientID, err := svc.s.Trips.ParseConsentToken(token)
	if err != nil {
		return nil, err
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	if consentScopeLocked(scope) {
		return nil, fmt.Errorf("%w: %q covers service messages required to deliver your bookings and cannot be switched off here", httpx.ErrValidation, scope)
	}
	c, err := svc.s.Store.Clients().Get(ctx, tenantID, clientID)
	if err != nil {
		return nil, err
	}
	if _, ok := c.Consent[scope]; !ok {
		return nil, fmt.Errorf("%w: unknown consent scope %q", httpx.ErrValidation, scope)
	}
	return svc.SetConsentSourced(ctx, tenantID, clientID, scope, granted,
		model.ConsentSourceSelfService, "self-service consent page")
}

// ConsentReceipt is the downloadable proof-of-consent artifact: who the
// controller is, the pseudonymous subject, current scopes, and the full
// evidence trail. JSON so the client (or their regulator) can machine-
// check it; the consent page offers it as a download.
type ConsentReceipt struct {
	ReceiptID  string               `json:"receipt_id"`
	IssuedAt   time.Time            `json:"issued_at"`
	Controller string               `json:"controller"` // tenant (data controller) name
	Subject    string               `json:"subject"`    // pseudonymous client id
	Scopes     []ConsentScopeState  `json:"scopes"`
	History    []model.ConsentEvent `json:"history"`
}

// ConsentReceiptByToken issues a fresh receipt for the token's client.
func (svc *ClientService) ConsentReceiptByToken(ctx context.Context, token string) (*ConsentReceipt, error) {
	_, clientID, err := svc.s.Trips.ParseConsentToken(token)
	if err != nil {
		return nil, err
	}
	cc, err := svc.ConsentCenterByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	return &ConsentReceipt{
		ReceiptID: NewID(), IssuedAt: svc.s.Now(),
		Controller: cc.TenantName, Subject: clientID,
		Scopes: cc.Scopes, History: cc.History,
	}, nil
}
