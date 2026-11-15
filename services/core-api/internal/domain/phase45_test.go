package domain

// Phase 45: client self-service consent center — token discipline,
// view/set/receipt flows, erasure interaction, and the footer link.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestConsentCenterToken45(t *testing.T) {
	s := testServices(t, Deps{TripSecret: "test-secret"})

	tok := s.Trips.ConsentTokenFor("t1", "c1")
	tenantID, clientID, err := s.Trips.ParseConsentToken(tok)
	if err != nil || tenantID != "t1" || clientID != "c1" {
		t.Fatalf("round trip: %q %q %v", tenantID, clientID, err)
	}
	// Cross-family tokens never open the consent center.
	if _, _, err := s.Trips.ParseConsentToken(s.Trips.UnsubTokenFor("t1", "c1")); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("unsub token: %v", err)
	}
	if _, _, err := s.Trips.ParseConsentToken(s.Trips.ReoptTokenFor("t1", "c1")); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("reopt token: %v", err)
	}
	// And the consent token opens nothing else.
	if _, _, err := s.Trips.ParseReoptToken(tok); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("consent token as reopt: %v", err)
	}
	if _, _, err := s.Trips.ParseConsentToken("garbage"); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("garbage: %v", err)
	}
}

func TestConsentCenterViewSet45(t *testing.T) {
	s := testServices(t, Deps{TripSecret: "test-secret"})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	rr, err := s.Clients.ResolveV2(ctx, tenantID, ResolveInput{
		Phone: "+971500000777", Name: "Center Buyer"})
	if err != nil {
		t.Fatal(err)
	}
	cl := rr.Client
	tok := s.Trips.ConsentTokenFor(tenantID, cl.ID)

	cc, err := s.Clients.ConsentCenterByToken(ctx, tok)
	if err != nil {
		t.Fatal(err)
	}
	if cc.TenantName == "" || cc.Erased {
		t.Fatalf("view: %+v", cc)
	}
	if len(cc.Scopes) != 3 {
		t.Fatalf("scopes: %+v", cc.Scopes)
	}
	if !cc.Scopes[0].Locked || cc.Scopes[0].Scope != "service" {
		t.Fatalf("locked service scope first: %+v", cc.Scopes[0])
	}
	for _, st := range cc.Scopes {
		if st.Scope == "marketing" && (st.Granted || st.Locked) {
			t.Fatalf("marketing should be off + toggleable: %+v", st)
		}
		if st.Label == "" {
			t.Fatalf("scope missing label: %+v", st)
		}
	}
	if len(cc.History) != 3 { // genesis rows render on the page
		t.Fatalf("genesis rows in view: %d", len(cc.History))
	}

	// One-tap grant — evidence row with the self_service source.
	if _, err := s.Clients.ConsentSetByToken(ctx, tok, "marketing", true); err != nil {
		t.Fatal(err)
	}
	hist, _ := s.Clients.ConsentHistory(ctx, tenantID, cl.ID, 50)
	if len(hist) != 4 || hist[0].Source != model.ConsentSourceSelfService || !hist[0].Granted {
		t.Fatalf("self-service evidence: %+v", hist[0])
	}

	// Double-click replay adds no row (change-only evidence).
	if _, err := s.Clients.ConsentSetByToken(ctx, tok, "marketing", true); err != nil {
		t.Fatal(err)
	}
	if hist, _ = s.Clients.ConsentHistory(ctx, tenantID, cl.ID, 50); len(hist) != 4 {
		t.Fatalf("replay added rows: %d", len(hist))
	}

	// Locked scope refused — booking notifications are the contract.
	if _, err := s.Clients.ConsentSetByToken(ctx, tok, "service", false); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("locked scope: %v", err)
	}
	// Unknown scope refused — the token must not pollute the consent map.
	if _, err := s.Clients.ConsentSetByToken(ctx, tok, "telemetry", true); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("unknown scope: %v", err)
	}
	// Wrong token family refused.
	if _, err := s.Clients.ConsentSetByToken(ctx, s.Trips.UnsubTokenFor(tenantID, cl.ID),
		"marketing", false); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("unsub token on set: %v", err)
	}
}

func TestConsentCenterErased45(t *testing.T) {
	s := testServices(t, Deps{TripSecret: "test-secret"})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	rr, err := s.Clients.ResolveV2(ctx, tenantID, ResolveInput{
		Phone: "+15550007777", Name: "Gone Buyer"})
	if err != nil {
		t.Fatal(err)
	}
	cl := rr.Client
	tok := s.Trips.ConsentTokenFor(tenantID, cl.ID)

	if _, err := s.Clients.EraseWithEvidence(ctx, tenantID, cl.ID); err != nil {
		t.Fatal(err)
	}
	// The page still renders: tombstone state + surviving evidence trail.
	cc, err := s.Clients.ConsentCenterByToken(ctx, tok)
	if err != nil {
		t.Fatal(err)
	}
	if !cc.Erased {
		t.Fatal("erased view must flag erased")
	}
	for _, st := range cc.Scopes {
		if st.Scope == "erased" {
			t.Fatal("tombstone marker must not render as a scope")
		}
	}
	if len(cc.History) == 0 {
		t.Fatal("evidence survives erasure")
	}
	// But no further changes — the erasure gate still holds.
	if _, err := s.Clients.ConsentSetByToken(ctx, tok, "marketing", true); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("set on erased: %v", err)
	}
}

func TestConsentReceipt45(t *testing.T) {
	s := testServices(t, Deps{TripSecret: "test-secret"})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	rr, err := s.Clients.ResolveV2(ctx, tenantID, ResolveInput{
		Phone: "+15550008888", Name: "Receipt Buyer"})
	if err != nil {
		t.Fatal(err)
	}
	cl := rr.Client
	tok := s.Trips.ConsentTokenFor(tenantID, cl.ID)

	if _, err := s.Clients.ConsentSetByToken(ctx, tok, "marketing", false); err != nil {
		t.Fatal(err)
	}
	rec, err := s.Clients.ConsentReceiptByToken(ctx, tok)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ReceiptID == "" || rec.IssuedAt.IsZero() || rec.Controller == "" {
		t.Fatalf("receipt envelope: %+v", rec)
	}
	if rec.Subject != cl.ID {
		t.Fatalf("subject: %q", rec.Subject)
	}
	// US number: marketing defaults on; the self-service revocation just
	// turned it off — receipt shows current state AND both rows.
	for _, st := range rec.Scopes {
		if st.Scope == "marketing" && st.Granted {
			t.Fatalf("receipt stale state: %+v", st)
		}
	}
	if len(rec.History) != 4 || rec.History[0].Source != model.ConsentSourceSelfService {
		t.Fatalf("receipt history: %+v", rec.History[0])
	}
}

func TestConsentFooterLink45(t *testing.T) {
	var calls int
	var payload map[string]any
	s := testServices(t, Deps{
		EmailURL:   emailStub(t, &calls, &payload).URL,
		TripSecret: "test-secret",
		PublicBase: "https://core.example",
	})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := emailClient(t, s, tenantID, true)

	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachMarketing, Channel: "email",
		Body: "Race-weekend transfers are open.", Subject: "F1 transfers",
		IdempotencyKey: "e-mkt-consent-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The recorded body carries BOTH links — unsubscribe and the consent
	// center (withdrawal as easy as giving, Art. 7(3)).
	if !strings.Contains(o.Body, "Unsubscribe from these messages: https://core.example/v1/unsub/") {
		t.Fatalf("missing unsub footer: %q", o.Body)
	}
	if !strings.Contains(o.Body, "Manage your preferences: https://core.example/v1/consent/") {
		t.Fatalf("missing consent-center link: %q", o.Body)
	}
}
