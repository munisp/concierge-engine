package server_test

// Phase 32 tests: payment-claim honesty (live rails, not flag state),
// the design layer (editorial layout family, section visibility, gallery),
// and the expanded end-to-end onboarding journey.

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
	"concierge-engine/services/core-api/internal/store"
)

// TestSitePaymentHonesty32: with NO PSP keys configured on the platform,
// the public site must not claim secure online payments — even when the
// tenant has enabled every rail flag. This was the Phase 31 bug: the gate
// read flags, not rail availability.
func TestSitePaymentHonesty32(t *testing.T) {
	env := newEnv(t) // Deps{} — no Stripe/Paystack keys
	seedLisbonTenant(t, env)

	_, _, body := getHost(t, env.server.URL, "/site/taylor/", "", "", "")
	if strings.Contains(body, "Secure online payment") || strings.Contains(body, "Pagamento online seguro") {
		t.Fatal("payment trust chip rendered with no live rail (no PSP keys)")
	}
	if strings.Contains(body, "Secure payments") || strings.Contains(body, "Pagamentos seguros") {
		t.Fatal("platform band rendered the payments feature card with no live rail")
	}

	// FAQ must not promise a payment link either.
	var svc model.CatalogService
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "name": "City Transfer",
		"base_minor": 9000, "currency": "EUR", "active": true,
	}, http.StatusCreated, &svc)
	_, _, body = getHost(t, env.server.URL, "/site/taylor/en/book/"+svc.ID, "", "", "")
	if strings.Contains(body, "payment link") {
		t.Fatal("FAQ claims a payment link with no live rail")
	}
	if !strings.Contains(body, "arranges payment with you directly") {
		t.Fatal("FAQ should fall back to concierge-arranged payment with no live rail")
	}

	// A platform WITH a key renders the claims.
	envLive := newEnvDeps(t, domain.Deps{StripeKey: "sk_test_fake_32"})
	seedLisbonTenant(t, envLive)
	_, _, body = getHost(t, envLive.server.URL, "/site/taylor/", "", "", "")
	if !strings.Contains(body, "Pagamento online seguro") {
		t.Fatal("payment chip missing despite live rail (key configured)")
	}
}

// TestSiteDesignLayer32: editorial layout family, section visibility and
// the gallery strip — all tenant-configured, all validated on write.
func TestSiteDesignLayer32(t *testing.T) {
	env := newEnvDeps(t, domain.Deps{StripeKey: "sk_test_fake_32"})
	seedLisbonTenant(t, env)

	// Editorial layout: CSS re-flow of the same DOM.
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{"design": "editorial"},
	}, http.StatusOK, nil)
	_, _, body := getHost(t, env.server.URL, "/site/taylor/", "", "", "")
	for _, want := range []string{`class="design-editorial"`, "body.design-editorial .hero{", "body.design-editorial .cards li{display:flex"} {
		if !strings.Contains(body, want) {
			t.Fatalf("editorial layout missing %q", want)
		}
	}
	// Classic (default) carries no editorial overrides.
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{"design": "classic"},
	}, http.StatusOK, nil)
	_, _, body = getHost(t, env.server.URL, "/site/taylor/", "", "", "")
	if strings.Contains(body, "design-editorial") {
		t.Fatal("editorial CSS leaked into classic layout")
	}

	// Section visibility: hide stats + platform band + steps.
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{"hidden_sections": []string{"stats", "platform", "steps"}},
	}, http.StatusOK, nil)
	_, _, body = getHost(t, env.server.URL, "/site/taylor/", "", "", "")
	for _, gone := range []string{`class="stats"`, `class="band"`, `class="steps"`} {
		if strings.Contains(body, gone) {
			t.Fatalf("hidden section still rendered: %q", gone)
		}
	}
	if !strings.Contains(body, `class="cards"`) { // services still visible
		t.Fatal("services section should still render")
	}

	// Validation: unknown design, unknown section, oversized gallery.
	for _, bad := range []map[string]any{
		{"design": "neon-brutalist"},
		{"hidden_sections": []string{"bogus"}},
		{"gallery_ids": []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}},
	} {
		env.do("PUT", "/v1/tenant", env.tenantID,
			map[string]any{"site_config": bad}, http.StatusUnprocessableEntity, nil)
	}

	// Gallery: uploaded media renders; dangling ids drop out silently.
	var up struct {
		Media model.Media `json:"media"`
	}
	env.do("POST", "/v1/media", env.tenantID, map[string]any{
		"kind": "misc", "content_type": "image/png",
		"data_base64": base64.StdEncoding.EncodeToString(tinyPNG32),
	}, http.StatusCreated, &up)
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{"gallery_ids": []string{up.Media.ID, "00000000-0000-0000-0000-000000000000"}},
	}, http.StatusOK, nil)
	_, _, body = getHost(t, env.server.URL, "/site/taylor/", "", "", "")
	if !strings.Contains(body, `class="gallery"`) {
		t.Fatal("gallery strip missing")
	}
	if strings.Count(body, "<li><img src=") < 1 || strings.Contains(body, "00000000-0000-0000-0000-000000000000") {
		t.Fatal("gallery should render the live asset and skip the dangling id")
	}
}

// TestOnboardingEndToEnd32: the journey now covers the full business
// setup — fleet, drivers, pricing, tax, dispatch, design, imagery,
// socials, templates, lifecycle — every step derived from real resources.
func TestOnboardingEndToEnd32(t *testing.T) {
	env := newEnv(t) // no PSP keys → payments step honestly pending

	type step struct {
		ID     string `json:"id"`
		Group  string `json:"group"`
		Status string `json:"status"`
	}
	get := func() map[string]step {
		var rep struct {
			Steps []step `json:"steps"`
		}
		env.do("GET", "/v1/onboarding", env.tenantID, nil, http.StatusOK, &rep)
		out := map[string]step{}
		for _, s := range rep.Steps {
			out[s.ID] = s
		}
		return out
	}

	// Fresh tenant (seed has currency+deposit): ops and presence steps pend.
	byID := get()
	wantPending := []string{"fleet", "drivers", "tax", "dispatch_cfg", "site_design",
		"imagery", "socials", "custom_domain", "templates", "lifecycle", "payments"}
	for _, id := range wantPending {
		if byID[id].Status != "pending" {
			t.Fatalf("step %q should pend on a fresh tenant, got %q", id, byID[id].Status)
		}
	}
	if byID["pricing"].Status != "done" { // seed sets deposit_pct 0.3
		t.Fatalf("pricing should be done (seed deposit), got %q", byID["pricing"].Status)
	}
	// Every step carries a group.
	for id, s := range byID {
		if s.Group == "" {
			t.Fatalf("step %q missing group", id)
		}
	}

	// Complete the operations core: fleet + driver + tax + dispatch tuning.
	env.do("POST", "/v1/dispatch/fleet", env.tenantID, map[string]any{
		"kind": "sedan", "label": "Mercedes S-Class", "active": true,
	}, http.StatusCreated, nil)
	env.do("POST", "/v1/dispatch/crew", env.tenantID, map[string]any{
		"full_name": "Joao Silva", "certificates": []string{"Defensive Driving"}, "active": true,
	}, http.StatusCreated, nil)
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"tax_config":      map[string]any{"engine": "flat", "country": "PT", "vat_rate_bp": 2300, "prices_include_tax": true},
		"dispatch_config": map[string]any{"default_duration_minutes": 240, "turnaround_minutes": 45},
		"site_config": map[string]any{
			"hero_image_id": "some-hero", "social_links": []string{"https://instagram.com/taylor"},
		},
		"lifecycle_config": map[string]any{"post_trip_hours": 24, "win_back_days": 90},
	}, http.StatusOK, nil)
	env.do("PUT", "/v1/studio/theme", env.tenantID, map[string]any{"theme": map[string]any{"preset": "noir-gold"}}, http.StatusOK, nil)
	env.do("POST", "/v1/outreach/templates", env.tenantID, map[string]any{
		"name": "booking-confirm", "purpose": "service", "body": "Obrigado {{client_name}}!",
	}, http.StatusOK, nil)

	byID = get()
	wantDone := []string{"fleet", "drivers", "tax", "dispatch_cfg", "site_design",
		"imagery", "socials", "templates", "lifecycle"}
	for _, id := range wantDone {
		if byID[id].Status != "done" {
			t.Fatalf("step %q should be done after setup, got %q", id, byID[id].Status)
		}
	}
	// Payments still honestly pending (no keys on this instance).
	if byID["payments"].Status != "pending" {
		t.Fatalf("payments should pend without live rails, got %q", byID["payments"].Status)
	}

	// Skip/unskip works on the new ids.
	env.do("PUT", "/v1/onboarding", env.tenantID,
		map[string]any{"skip": []string{"custom_domain"}}, http.StatusOK, nil)
	if s := get()["custom_domain"]; s.Status != "skipped" {
		t.Fatalf("custom_domain should be skipped, got %q", s.Status)
	}
	// Unknown step ids are rejected.
	env.do("PUT", "/v1/onboarding", env.tenantID,
		map[string]any{"skip": []string{"bogus_step"}}, http.StatusUnprocessableEntity, nil)
}

// TestLiveRails32: the domain gate itself — keys ∧ flags.
func TestLiveRails32(t *testing.T) {
	svc := domain.NewServices(store.NewMemory(), domain.Deps{StripeKey: "sk", PaystackKey: "pk"})
	rails := svc.Payments.LiveRails(model.PaymentConfig{})
	if len(rails) != 2 {
		t.Fatalf("both rails live: %v", rails)
	}
	rails = svc.Payments.LiveRails(model.PaymentConfig{DisableStripe: true})
	if len(rails) != 1 || rails[0] != "paystack" {
		t.Fatalf("stripe disabled: %v", rails)
	}
	svcNone := domain.NewServices(store.NewMemory(), domain.Deps{})
	if r := svcNone.Payments.LiveRails(model.PaymentConfig{}); len(r) != 0 {
		t.Fatalf("no keys = no live rails: %v", r)
	}
}

// tinyPNG32 is a 1x1 transparent PNG (same fixture as the Phase 31 suite;
// duplicated to keep this file self-contained).
var tinyPNG32 = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x62, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

// TestCatalogConsole32: the console Catalog page's write surface — the
// PATCH-shaped catalog update covers price/rates/active/name (not just
// image_id), with validation, and the tenant-scoped raw media endpoint
// serves bytes for console thumbnails.
func TestCatalogConsole32(t *testing.T) {
	env := newEnv(t)

	var svc model.CatalogService
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "ground", "name": "Airport Transfer",
		"base_minor": 9500, "currency": "EUR",
	}, http.StatusCreated, &svc)

	// Full mutable-field update.
	var updated model.CatalogService
	env.do("PUT", "/v1/catalog/"+svc.ID, env.tenantID, map[string]any{
		"name": "Airport Transfer (Meet & Greet)", "base_minor": 12000,
		"per_km_minor": 210, "per_hour_minor": 4500,
		"auto_quotable": true, "active": true,
	}, http.StatusOK, &updated)
	if updated.Name != "Airport Transfer (Meet & Greet)" || updated.BaseMinor != 12000 ||
		updated.PerKMMinor != 210 || updated.PerHourMinor != 4500 || !updated.AutoQuotable {
		t.Fatalf("update not applied: %+v", updated)
	}

	// Deactivate.
	env.do("PUT", "/v1/catalog/"+svc.ID, env.tenantID,
		map[string]any{"active": false}, http.StatusOK, &updated)
	if updated.Active {
		t.Fatal("active=false not applied")
	}

	// Validation: explicit empty name / negative price rejected.
	env.do("PUT", "/v1/catalog/"+svc.ID, env.tenantID,
		map[string]any{"name": ""}, http.StatusUnprocessableEntity, nil)
	env.do("PUT", "/v1/catalog/"+svc.ID, env.tenantID,
		map[string]any{"base_minor": -1}, http.StatusUnprocessableEntity, nil)

	// Raw media read for console thumbnails: bytes + content type,
	// tenant-scoped (a second tenant cannot read it).
	var up struct {
		Media model.Media `json:"media"`
	}
	env.do("POST", "/v1/media", env.tenantID, map[string]any{
		"kind": "misc", "content_type": "image/png",
		"data_base64": base64.StdEncoding.EncodeToString(tinyPNG32),
	}, http.StatusCreated, &up)

	req, err := http.NewRequest("GET", env.server.URL+"/v1/media/"+up.Media.ID+"/raw", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Tenant-ID", env.tenantID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("raw media: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("raw media content type: %q", ct)
	}

	req2, _ := http.NewRequest("GET", env.server.URL+"/v1/media/"+up.Media.ID+"/raw", nil)
	req2.Header.Set("X-Tenant-ID", "00000000-0000-0000-0000-000000000000")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode == http.StatusOK {
		t.Fatal("raw media readable across tenants")
	}
}
