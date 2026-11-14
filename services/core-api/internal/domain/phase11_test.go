// Phase 16 domain tests: EU/US-first readiness — regional consent defaults
// at registration, GDPR/CCPA export + erasure with write-surface gating,
// per-market Stripe payment rails, and methods discovery for launch markets.
package domain

import (
	"context"
	"errors"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// ------------------------------------------------------------- consent defaults

func TestRegionalConsentDefaults(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	cases := []struct {
		phone     string
		marketing bool
		label     string
	}{
		{"+4915123456789", false, "Germany (GDPR opt-in)"},
		{"+31612345678", false, "Netherlands (GDPR opt-in)"},
		{"+447911123456", false, "UK (UK GDPR opt-in)"},
		{"+12125551234", true, "US (opt-out regime)"},
		{"+14165551234", true, "Canada (opt-out regime)"},
		{"+2348012345678", false, "Nigeria (unknown regime -> conservative opt-in)"},
		{"", false, "no phone (conservative)"},
	}
	for i, tc := range cases {
		rr, err := s.Clients.ResolveV2(ctx, tenantID, ResolveInput{
			Phone: tc.phone, Name: "Client", Locale: "en-US", Timezone: "UTC"})
		if err != nil {
			t.Fatal(err)
		}
		got := rr.Client.Consent["marketing"] == true
		if got != tc.marketing {
			t.Fatalf("case %d %s: marketing=%v, want %v", i, tc.label, got, tc.marketing)
		}
		if rr.Client.Consent["service"] != true || rr.Client.Consent["data_processing"] != true {
			t.Fatalf("case %d %s: service/data_processing must default true", i, tc.label)
		}
		if rr.Client.Locale != "en-US" || rr.Client.Timezone != "UTC" {
			t.Fatalf("case %d: locale/timezone not captured: %+v", i, rr.Client)
		}
	}
}

// ------------------------------------------------------------- DSAR export + erasure

func TestDSARExport(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	rr, _ := s.Clients.Resolve(ctx, tenantID, "+491599900001", "export@example.com", "Export Me")
	id := rr.Client.ID
	if _, err := s.Clients.SavePreference(ctx, tenantID, id, "seat", "window", "service", "", "test"); err != nil {
		t.Fatal(err)
	}

	exp, err := s.Clients.Export(ctx, tenantID, id)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Client.Email != "export@example.com" || len(exp.Preferences) != 1 || exp.ExportedAt == "" {
		t.Fatalf("export incomplete: %+v", exp)
	}
	// Unknown client errors, not a hollow document.
	if _, err := s.Clients.Export(ctx, tenantID, "nope"); err == nil {
		t.Fatal("export of unknown client must error")
	}
}

func TestEraseAnonymizesAndGates(t *testing.T) {
	calls := 0
	s := testServices(t, Deps{WhatsAppURL: waCountingStub(t, &calls).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	rr, _ := s.Clients.Resolve(ctx, tenantID, "+14155550001", "erase@example.com", "Erase Me")
	id := rr.Client.ID
	if _, err := s.Clients.SavePreference(ctx, tenantID, id, "diet", "vegan", "service", "", "test"); err != nil {
		t.Fatal(err)
	}

	erased, err := s.Clients.Erase(ctx, tenantID, id)
	if err != nil {
		t.Fatal(err)
	}
	if !erased.Erased() || erased.FullName != "Erased client" || erased.Phone != "" ||
		erased.Locale != "" || erased.Consent["marketing"] != false {
		t.Fatalf("not anonymized: %+v", erased)
	}
	// Preference memory is gone.
	if prefs, err := s.Store.Clients().Preferences(ctx, tenantID, id); err != nil || len(prefs) != 0 {
		t.Fatalf("preferences survived erasure: %d %v", len(prefs), err)
	}
	// Every write surface refuses further processing.
	name := "Resurrect"
	if _, err := s.Clients.UpdateProfile(ctx, tenantID, id, UpdateProfileInput{FullName: &name}); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("UpdateProfile on erased: %v", err)
	}
	if _, err := s.Clients.SetConsent(ctx, tenantID, id, "marketing", true); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("SetConsent on erased: %v", err)
	}
	if _, err := s.Clients.SavePreference(ctx, tenantID, id, "k", "v", "service", "", "t"); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("SavePreference on erased: %v", err)
	}
	// Outreach: held (auditable refusal), never delivered, any purpose.
	v, err := s.Outreach.Verdict(ctx, tenantID, id, model.OutreachService)
	if err != nil || v.Allowed {
		t.Fatalf("verdict on erased: %+v %v", v, err)
	}
	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: id, Purpose: model.OutreachService, Body: "trip update", IdempotencyKey: "er1"})
	if err != nil || o.Status != "held" {
		t.Fatalf("send to erased: %+v %v", o, err)
	}
	if calls != 0 {
		t.Fatal("adapter called for erased client")
	}
	// Idempotent re-erase.
	if again, err := s.Clients.Erase(ctx, tenantID, id); err != nil || !again.Erased() {
		t.Fatalf("re-erase: %+v %v", again, err)
	}
	// The same human re-registering starts fresh: tombstoned identifiers
	// never match, so resolve creates a NEW client.
	rr2, err := s.Clients.Resolve(ctx, tenantID, "+14155550001", "", "Erase Me")
	if err != nil || !rr2.IsNew || rr2.Client.ID == id {
		t.Fatalf("resolve after erasure: %+v %v", rr2, err)
	}
}

func TestMergeIntoErasedRefused(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	winner, _ := s.Clients.Resolve(ctx, tenantID, "+14155550010", "", "Winner")
	loser, _ := s.Clients.Resolve(ctx, tenantID, "+14155550011", "", "Loser")
	if _, err := s.Clients.Erase(ctx, tenantID, winner.Client.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Clients.Merge(ctx, tenantID, winner.Client.ID, loser.Client.ID); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("merge into erased winner: %v", err)
	}
	// Erasing the LOSER is fine — merge still works with a live winner.
	if _, err := s.Clients.Erase(ctx, tenantID, loser.Client.ID); err != nil {
		t.Fatal(err)
	}
	winner2, _ := s.Clients.Resolve(ctx, tenantID, "+14155550012", "", "Live Winner")
	if err := s.Clients.Merge(ctx, tenantID, winner2.Client.ID, loser.Client.ID); err != nil {
		t.Fatalf("merge of erased loser into live winner: %v", err)
	}
}

// ------------------------------------------------------------- EU/US payment rails

func TestStripeMarketRails(t *testing.T) {
	eur := stripeChannelsFor("EUR")
	joined := ""
	for _, m := range eur {
		joined += m + ","
	}
	for _, want := range []string{"card", "sepa_debit", "ideal", "bancontact", "giropay", "sofort", "klarna"} {
		if !strings.Contains(joined, want+",") {
			t.Fatalf("EUR rails missing %s: %v", want, eur)
		}
	}
	usd := stripeChannelsFor("USD")
	found := false
	for _, m := range usd {
		if m == "us_bank_account" {
			found = true
		}
	}
	if !found {
		t.Fatalf("USD rails missing ACH: %v", usd)
	}
	if card := stripeChannelsFor("AED"); len(card) != 1 || card[0] != "card" {
		t.Fatalf("unknown currency must fall back to card: %v", card)
	}
}

func TestPaymentMethodsLaunchMarkets(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	for _, cur := range []string{"EUR", "USD", "GBP"} {
		methods, err := s.Payments.PaymentMethods(ctx, tenantID, cur)
		if err != nil {
			t.Fatal(err)
		}
		if len(methods) != 1 || methods[0].PSP != "stripe" {
			t.Fatalf("%s must route to Stripe only pre-Africa-wave: %+v", cur, methods)
		}
		if len(methods[0].Channels) < 2 {
			t.Fatalf("%s should expose market rails, not card-only: %+v", cur, methods[0])
		}
	}
	// African currencies still offer Paystack first.
	methods, err := s.Payments.PaymentMethods(ctx, tenantID, "NGN")
	if err != nil {
		t.Fatal(err)
	}
	if len(methods) != 2 || methods[0].PSP != "paystack" || methods[1].PSP != "stripe" {
		t.Fatalf("NGN rails: %+v", methods)
	}
}
