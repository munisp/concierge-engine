package domain

// Phase 44: consent evidence ledger — change-only recording, per-surface
// attribution, genesis rows at registration, erasure revocations, and the
// DSAR export section.

import (
	"context"
	"errors"
	"testing"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestConsentEvidence44(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	// Registration writes genesis rows (jurisdiction defaults).
	rr, err := s.Clients.ResolveV2(ctx, tenantID, ResolveInput{
		Phone: "+971500000555", Name: "Evidence Buyer"})
	if err != nil {
		t.Fatal(err)
	}
	cl := rr.Client
	hist, err := s.Clients.ConsentHistory(ctx, tenantID, cl.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 3 { // service + data_processing + marketing defaults
		t.Fatalf("genesis rows: %d", len(hist))
	}
	for _, e := range hist {
		if e.Source != model.ConsentSourceRegistrationDefault {
			t.Fatalf("genesis source: %s", e.Source)
		}
	}
	// +971 is an opt-in jurisdiction: marketing starts false.
	for _, e := range hist {
		if e.Scope == "marketing" && e.Granted {
			t.Fatal("opt-in jurisdiction must default marketing=false")
		}
	}

	// Console grant records one evidence row.
	if _, err := s.Clients.SetConsentSourced(ctx, tenantID, cl.ID, "marketing", true,
		model.ConsentSourceConsole, "operator toggle"); err != nil {
		t.Fatal(err)
	}
	hist, _ = s.Clients.ConsentHistory(ctx, tenantID, cl.ID, 50)
	if len(hist) != 4 || hist[0].Source != model.ConsentSourceConsole || !hist[0].Granted {
		t.Fatalf("after grant: %+v", hist[0])
	}

	// Replayed write with the same value adds NO row (idempotent evidence).
	if _, err := s.Clients.SetConsentSourced(ctx, tenantID, cl.ID, "marketing", true,
		model.ConsentSourceConsole, ""); err != nil {
		t.Fatal(err)
	}
	if hist, _ = s.Clients.ConsentHistory(ctx, tenantID, cl.ID, 50); len(hist) != 4 {
		t.Fatalf("replay must not add evidence: %d rows", len(hist))
	}

	// Unknown source is refused — the ledger stays queryable.
	if _, err := s.Clients.SetConsentSourced(ctx, tenantID, cl.ID, "marketing", false,
		"carrier_pigeon", ""); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("unknown source: %v", err)
	}

	// Unsub path attributes to the unsub link.
	if _, err := s.Outreach.UnsubscribeByToken(ctx, s.Trips.UnsubTokenFor(tenantID, cl.ID)); err != nil {
		t.Fatal(err)
	}
	hist, _ = s.Clients.ConsentHistory(ctx, tenantID, cl.ID, 50)
	if len(hist) != 5 || hist[0].Source != model.ConsentSourceUnsub || hist[0].Granted {
		t.Fatalf("unsub evidence: %+v", hist[0])
	}
}

func TestConsentKeywordEvidence44(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	rr, err := s.Clients.ResolveV2(ctx, tenantID, ResolveInput{Phone: "+15550000444", Name: "KW"})
	if err != nil {
		t.Fatal(err)
	}
	cl := rr.Client // +1 = opt-out regime → marketing true by default

	// STOP flips off with keyword evidence.
	handled, err := s.Cases.maybeConsentKeyword(ctx, tenantID, cl.ID, "STOP")
	if err != nil || !handled {
		t.Fatalf("stop: %v %v", handled, err)
	}
	hist, _ := s.Clients.ConsentHistory(ctx, tenantID, cl.ID, 50)
	var found bool
	for _, e := range hist {
		if e.Source == model.ConsentSourceKeyword && !e.Granted && e.Detail == "client texted 'stop'" {
			found = true
		}
	}
	if !found {
		t.Fatalf("keyword evidence missing: %+v", hist)
	}
	// Idempotent: second STOP adds no row.
	n := len(hist)
	if _, err := s.Cases.maybeConsentKeyword(ctx, tenantID, cl.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	if hist, _ = s.Clients.ConsentHistory(ctx, tenantID, cl.ID, 50); len(hist) != n {
		t.Fatalf("repeated STOP added evidence: %d -> %d", n, len(hist))
	}
	// START flips back on.
	if _, err := s.Cases.maybeConsentKeyword(ctx, tenantID, cl.ID, "START"); err != nil {
		t.Fatal(err)
	}
	hist, _ = s.Clients.ConsentHistory(ctx, tenantID, cl.ID, 50)
	if hist[0].Source != model.ConsentSourceKeyword || !hist[0].Granted {
		t.Fatalf("start evidence: %+v", hist[0])
	}
}

func TestConsentErasureEvidence44(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	rr, err := s.Clients.ResolveV2(ctx, tenantID, ResolveInput{Phone: "+15550000777", Name: "Gone"})
	if err != nil {
		t.Fatal(err)
	}
	cl := rr.Client

	if _, err := s.Clients.EraseWithEvidence(ctx, tenantID, cl.ID); err != nil {
		t.Fatal(err)
	}
	// Every previously-granted scope got a dsar_erasure revocation row.
	hist, err := s.Clients.ConsentHistory(ctx, tenantID, cl.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	revoked := map[string]bool{}
	for _, e := range hist {
		if e.Source == model.ConsentSourceDSARErasure {
			if e.Granted {
				t.Fatal("erasure row must record granted=false")
			}
			revoked[e.Scope] = true
		}
	}
	for _, scope := range []string{"service", "data_processing", "marketing"} {
		if !revoked[scope] { // +1 defaults granted all three
			t.Fatalf("no erasure revocation for %s: %+v", scope, hist)
		}
	}
	// Post-erasure writes stay refused, evidence or not.
	if _, err := s.Clients.SetConsentSourced(ctx, tenantID, cl.ID, "marketing", true,
		model.ConsentSourceConsole, ""); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("erased client consent write: %v", err)
	}
}

func TestConsentEvidenceInExport44(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	rr, err := s.Clients.ResolveV2(ctx, tenantID, ResolveInput{Phone: "+15550000999", Name: "Export"})
	if err != nil {
		t.Fatal(err)
	}
	exp, err := s.Clients.ExportWithEvidence(ctx, tenantID, rr.Client.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(exp.ConsentHistory) != 3 {
		t.Fatalf("export consent history: %d rows", len(exp.ConsentHistory))
	}
}

func TestConsentHistoryUnknownClient44(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	if _, err := s.Clients.ConsentHistory(ctx, tenantID, NewID(), 10); !errors.Is(err, httpx.ErrNotFound) {
		t.Fatalf("unknown client: %v", err)
	}
}
