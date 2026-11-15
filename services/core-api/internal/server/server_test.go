package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
	"concierge-engine/services/core-api/internal/server"
	"concierge-engine/services/core-api/internal/store"
)

type testEnv struct {
	t        *testing.T
	server   *httptest.Server
	tenantID string
	svc      *domain.Services // Phase 39: token minting in unsub tests
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	return newEnvDeps(t, domain.Deps{})
}

// newEnvDeps (Phase 32) builds an env with explicit platform deps — e.g. a
// Stripe key so payment-rail honesty gates can be tested in both states
// (key present = rail live; no key = sandbox, no payment claims).
func newEnvDeps(t *testing.T, deps domain.Deps) *testEnv {
	t.Helper()
	svc := domain.NewServices(store.NewMemory(), deps)
	srv := httptest.NewServer(server.New(svc))
	t.Cleanup(srv.Close)
	env := &testEnv{t: t, server: srv, svc: svc}

	// platform admin: onboard a tenant (Taylor archetype: flagship)
	var tenant model.Tenant
	env.do("POST", "/v1/platform/tenants", "", model.Tenant{
		Slug: "taylor", Name: "Taylor Travel Management", Archetype: "flagship",
		PricingRules: model.PricingRules{Currency: "EUR", DepositPct: 0.3, QuoteTTLSeconds: 3600, PlatformFeePct: 0.03},
	}, http.StatusCreated, &tenant)
	env.tenantID = tenant.ID
	return env
}

func (e *testEnv) do(method, path, tenantID string, body any, want int, out any) {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, e.server.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if tenantID != "" {
		req.Header.Set("X-Tenant-ID", tenantID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		var env map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&env)
		e.t.Fatalf("%s %s: got %d want %d: %v", method, path, resp.StatusCode, want, env)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			e.t.Fatal(err)
		}
	}
}

func TestTenantIsolationEnforced(t *testing.T) {
	env := newEnv(t)
	// tenant routes without X-Tenant-ID are rejected
	env.do("GET", "/v1/catalog", "", nil, http.StatusForbidden, nil)
}

func TestIdentityResolutionAndPreferences(t *testing.T) {
	env := newEnv(t)

	var res domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"phone": "+31 6 1234 5678", "name": "M. Raap"}, http.StatusOK, &res)
	if !res.IsNew {
		t.Fatal("expected new client")
	}

	// same phone, reformatted -> same client (phone is the identity anchor)
	var res2 domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"phone": "+31612345678"}, http.StatusOK, &res2)
	if res2.IsNew || res2.Client.ID != res.Client.ID {
		t.Fatalf("identity resolution failed: %+v", res2)
	}

	// consent-scoped preference memory
	env.do("PUT", "/v1/clients/"+res.Client.ID+"/preferences/vehicle_class", env.tenantID,
		map[string]string{"value": "first", "consent_scope": "service", "recorded_by": "ai"}, http.StatusOK, nil)
	var prefs []model.Preference
	env.do("GET", "/v1/clients/"+res.Client.ID+"/preferences", env.tenantID, nil, http.StatusOK, &prefs)
	if len(prefs) != 1 || prefs[0].Value != "first" {
		t.Fatalf("preferences: %+v", prefs)
	}
	// DSAR cascade
	env.do("DELETE", "/v1/clients/"+res.Client.ID+"/preferences", env.tenantID, nil, http.StatusNoContent, nil)
	env.do("GET", "/v1/clients/"+res.Client.ID+"/preferences", env.tenantID, nil, http.StatusOK, &prefs)
	if len(prefs) != 0 {
		t.Fatal("DSAR cascade failed")
	}
}

func TestInstantQuoteAndBookingFlow(t *testing.T) {
	env := newEnv(t)

	// catalog: business sedan, per-km pricing
	env.do("POST", "/v1/catalog", env.tenantID, model.CatalogService{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Business Sedan|business",
		BaseMinor: 9500, Currency: "EUR", PerKMMinor: 350, AutoQuotable: true,
	}, http.StatusCreated, nil)

	var res domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"email": "ea@family-office.example", "name": "EA Booker"}, http.StatusOK, &res)

	// create request (idempotent) — AMS airport -> Amsterdam centre (~15km)
	var sr model.ServiceRequest
	env.do("POST", "/v1/requests", env.tenantID, map[string]any{
		"client_id": res.Client.ID, "idempotency_key": "req-1",
		"items": []model.ServiceItem{{
			Vertical: "CHAUFFEUR_POINT_TO_POINT", VehicleClass: "business",
			PickupAddress: "AMS Schiphol", PickupLat: 52.3105, PickupLng: 4.7683,
			DropoffAddress: "Herengracht 1, Amsterdam", DropoffLat: 52.3676, DropoffLng: 4.9041,
			PickupAt: time.Now().Add(24 * time.Hour).Unix(), Passengers: 2,
		}},
	}, http.StatusCreated, &sr)

	// idempotent replay returns the same request
	var sr2 model.ServiceRequest
	env.do("POST", "/v1/requests", env.tenantID, map[string]any{
		"client_id": res.Client.ID, "idempotency_key": "req-1",
		"items": []model.ServiceItem{{
			Vertical: "CHAUFFEUR_POINT_TO_POINT", VehicleClass: "business",
			PickupAddress: "AMS Schiphol", PickupLat: 52.3105, PickupLng: 4.7683,
			DropoffAddress: "Herengracht 1, Amsterdam", DropoffLat: 52.3676, DropoffLng: 4.9041,
			PickupAt: time.Now().Add(24 * time.Hour).Unix(), Passengers: 2,
		}},
	}, http.StatusCreated, &sr2)
	if sr2.ID != sr.ID {
		t.Fatal("idempotency broken")
	}

	// instant quote
	var q model.Quote
	env.do("POST", "/v1/requests/"+sr.ID+"/quote", env.tenantID, nil, http.StatusOK, &q)
	if q.Origin != "INSTANT" || q.TotalMinor <= 9500 || q.Currency != "EUR" {
		t.Fatalf("quote: %+v", q)
	}
	if q.DepositMinor != int64(float64(q.TotalMinor)*0.3+0.5) && q.DepositMinor == 0 {
		t.Fatalf("deposit missing: %+v", q)
	}

	// payment link then PSP capture -> booking + split settlement
	var intent model.PaymentIntent
	env.do("POST", "/v1/payments/links", env.tenantID, map[string]any{
		"quote_id": q.ID, "mode": "FULL", "idempotency_key": "pay-1",
	}, http.StatusCreated, &intent)
	if intent.AmountMinor != q.TotalMinor {
		t.Fatalf("intent amount: %+v", intent)
	}

	var booking model.Booking
	env.do("POST", "/v1/quotes/"+q.ID+"/confirm", env.tenantID, nil, http.StatusCreated, &booking)

	env.do("POST", "/v1/payments/webhooks/stripe", env.tenantID, map[string]string{
		"intent_id": intent.ID, "event": "payment.captured", "psp_ref": "ch_123",
	}, http.StatusOK, nil)

	var postings []model.LedgerPosting
	env.do("GET", "/v1/payments/postings", env.tenantID, nil, http.StatusOK, &postings)
	if len(postings) != 2 { // net revenue + platform fee
		t.Fatalf("expected 2 split postings, got %d", len(postings))
	}
	var sum int64
	for _, p := range postings {
		sum += p.AmountMinor
	}
	if sum != intent.AmountMinor {
		t.Fatalf("split mismatch: %d != %d", sum, intent.AmountMinor)
	}
}

func TestStateMachineRejectsIllegalTransition(t *testing.T) {
	env := newEnv(t)
	var res domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"name": "X"}, http.StatusOK, &res)
	var sr model.ServiceRequest
	env.do("POST", "/v1/requests", env.tenantID, map[string]any{
		"client_id": res.Client.ID, "idempotency_key": "req-illegal",
		"items": []model.ServiceItem{{
			Vertical: "CHAUFFEUR_POINT_TO_POINT", PickupAddress: "A", PickupAt: time.Now().Unix(),
		}},
	}, http.StatusCreated, &sr)
	// NEW->CAPTURING happens at create; CAPTURING->CONFIRMED is illegal
	env.do("POST", "/v1/requests/"+sr.ID+"/transition", env.tenantID,
		map[string]string{"to": "CONFIRMED"}, http.StatusConflict, nil)
}

func TestHandoffQueue(t *testing.T) {
	env := newEnv(t)
	var conv model.Conversation
	env.do("POST", "/v1/conversations", env.tenantID, map[string]string{
		"channel": "WHATSAPP",
	}, http.StatusCreated, &conv)

	env.do("POST", "/v1/conversations/"+conv.ID+"/messages", env.tenantID, model.Message{
		Author: "CLIENT", Body: "I need the jet to Geneva tomorrow, money no object.",
	}, http.StatusCreated, nil)

	var handoff map[string]any
	env.do("POST", "/v1/handoffs", env.tenantID, map[string]any{
		"conversation_id": conv.ID, "trigger": "VALUE",
		"reason": "estimated value above tenant threshold",
	}, http.StatusCreated, &handoff)
	if handoff["id"] == nil {
		t.Fatal("no handoff id")
	}

	var open []map[string]any
	env.do("GET", "/v1/handoffs", env.tenantID, nil, http.StatusOK, &open)
	if len(open) != 1 {
		t.Fatalf("expected 1 open handoff, got %d", len(open))
	}

	env.do("POST", fmt.Sprintf("/v1/handoffs/%v/resolve", handoff["id"]), env.tenantID,
		map[string]string{"resolution": "RESOLVED", "owner": "staff:mathijs"}, http.StatusOK, nil)
	env.do("GET", "/v1/handoffs", env.tenantID, nil, http.StatusOK, &open)
	if len(open) != 0 {
		t.Fatal("handoff not resolved")
	}
}

func TestHumanQuoteFallbackAboveTier2Limit(t *testing.T) {
	env := newEnv(t)
	// default TIER-2 limit is 50000 minor; price a request above it
	env.do("POST", "/v1/catalog", env.tenantID, model.CatalogService{
		Vertical: "CHAUFFEUR_HOURLY", Name: "First Class|first",
		BaseMinor: 60000, Currency: "EUR", PerHourMinor: 15000, AutoQuotable: true,
	}, http.StatusCreated, nil)
	var res domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID, map[string]string{"name": "VIP"}, http.StatusOK, &res)
	var sr model.ServiceRequest
	env.do("POST", "/v1/requests", env.tenantID, map[string]any{
		"client_id": res.Client.ID, "idempotency_key": "req-big",
		"items": []model.ServiceItem{{
			Vertical: "CHAUFFEUR_HOURLY", VehicleClass: "first", Hours: 4,
			PickupAddress: "Amsterdam", PickupAt: time.Now().Unix(),
		}},
	}, http.StatusCreated, &sr)
	// 60000 + 4*? base alone exceeds 50000 -> human fallback (202)
	var out map[string]any
	env.do("POST", "/v1/requests/"+sr.ID+"/quote", env.tenantID, nil, http.StatusAccepted, &out)
	if out["origin"] != "HUMAN" {
		t.Fatalf("expected human fallback: %v", out)
	}
}

func TestTenantConfigSelectivePatch(t *testing.T) {
	env := newEnv(t)

	// Seed two config objects.
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"tax_config":      map[string]any{"engine": "flat", "country": "NL", "vat_rate_bp": 2100},
		"dispatch_config": map[string]any{"turnaround_minutes": 45, "default_duration_minutes": 90},
	}, http.StatusOK, nil)

	// Patch ONLY the operator roster: tax and dispatch must survive intact.
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"operator_config": map[string]any{"alert_phones": []string{"+971500000099"}},
	}, http.StatusOK, nil)

	var got model.Tenant
	env.do("GET", "/v1/tenant", env.tenantID, nil, http.StatusOK, &got)
	if got.TaxConfig.Engine != "flat" || got.TaxConfig.VATRateBP != 2100 {
		t.Fatalf("tax config wiped by unrelated patch: %+v", got.TaxConfig)
	}
	if got.DispatchConfig.TurnaroundMinutes != 45 || got.DispatchConfig.DefaultDurationMinutes != 90 {
		t.Fatalf("dispatch config wiped by unrelated patch: %+v", got.DispatchConfig)
	}
	if len(got.OperatorConfig.AlertPhones) != 1 || got.OperatorConfig.AlertPhones[0] != "+971500000099" {
		t.Fatalf("operator roster not applied: %+v", got.OperatorConfig)
	}

	// Empty object patch is a no-op, not a wipe.
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{}, http.StatusOK, nil)
	env.do("GET", "/v1/tenant", env.tenantID, nil, http.StatusOK, &got)
	if got.TaxConfig.Engine != "flat" || len(got.OperatorConfig.AlertPhones) != 1 {
		t.Fatalf("empty patch mutated config: %+v %+v", got.TaxConfig, got.OperatorConfig)
	}
}

// ------------------------------------------------------------- Phase 25: tracking + lifecycle

func TestTrackEndpoint(t *testing.T) {
	env := newEnv(t)

	// Garbage token: forbidden, never a panic or a 500.
	env.do("GET", "/v1/track/not-a-token", "", nil, http.StatusForbidden, nil)

	// Mint a real track link for an assignment that doesn't exist yet: the
	// token verifies (auth passed) and the missing trip is a clean 404.
	var link struct {
		Token string `json:"token"`
		URL   string `json:"url"`
	}
	env.do("POST", "/v1/dispatch/assignments/asg-nope/track-link", env.tenantID, nil,
		http.StatusOK, &link)
	if link.Token == "" || link.URL == "" {
		t.Fatalf("track link empty: %+v", link)
	}
	env.do("GET", "/v1/track/"+link.Token, "", nil, http.StatusNotFound, nil)

	// The same endpoint rejects the DRIVER token family (wrong credential).
	// (The driver mint is exercised end-to-end in the domain suite; here we
	// just prove the track route never accepts a foreign token shape.)
	env.do("GET", "/v1/track/"+link.Token[:len(link.Token)-2]+"zz", "", nil,
		http.StatusForbidden, nil)
}

func TestFollowUpsRunEndpoint(t *testing.T) {
	env := newEnv(t)

	// Fresh tenant: nothing to do, but the run is real and reported.
	var res struct {
		PostTripSent   int `json:"post_trip_sent"`
		WinBackSent    int `json:"win_back_sent"`
		WinBackHeld    int `json:"win_back_held"`
		ClientsScanned int `json:"clients_scanned"`
	}
	env.do("POST", "/v1/followups/run", env.tenantID, nil, http.StatusOK, &res)
	if res.PostTripSent != 0 || res.WinBackSent != 0 || res.WinBackHeld != 0 || res.ClientsScanned != 0 {
		t.Fatalf("empty tenant produced engagement: %+v", res)
	}

	// Lifecycle config rides the selective patch: set it, then prove an
	// unrelated patch leaves it intact (Phase 24 wipe regression).
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"lifecycle_config": map[string]any{"disabled": true, "post_trip_hours": 12, "win_back_days": 45},
	}, http.StatusOK, nil)
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"operator_config": map[string]any{"alert_phones": []string{"+971500000098"}},
	}, http.StatusOK, nil)
	var got model.Tenant
	env.do("GET", "/v1/tenant", env.tenantID, nil, http.StatusOK, &got)
	if !got.LifecycleConfig.Disabled || got.LifecycleConfig.PostTripHours != 12 ||
		got.LifecycleConfig.WinBackDays != 45 {
		t.Fatalf("lifecycle config not persisted: %+v", got.LifecycleConfig)
	}
	if len(got.OperatorConfig.AlertPhones) != 1 {
		t.Fatalf("unrelated patch not applied: %+v", got.OperatorConfig)
	}

	// Disabled tenant: the run is a no-op even when triggered manually.
	env.do("POST", "/v1/followups/run", env.tenantID, nil, http.StatusOK, &res)
	if res.PostTripSent != 0 || res.WinBackSent != 0 {
		t.Fatalf("disabled tenant engaged clients: %+v", res)
	}
}

// readAll drains a response body for content assertions (public site pages).
func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ------------------------------------------------------------- Phase 26: acquisition monetization + SEO site

func TestAcquisitionEndpoints(t *testing.T) {
	env := newEnv(t)

	// Defaults ride through: 100 clients / 90 days / 10% fee channels.
	var cohort struct {
		GoalClients int  `json:"goal_clients"`
		GoalDays    int  `json:"goal_days"`
		NewClients  int  `json:"new_clients"`
		OnTrack     bool `json:"on_track"`
	}
	env.do("GET", "/v1/acquisition/cohort", env.tenantID, nil, http.StatusOK, &cohort)
	if cohort.GoalClients != 100 || cohort.GoalDays != 90 || cohort.NewClients != 0 {
		t.Fatalf("cohort defaults: %+v", cohort)
	}

	var inv struct {
		Lines       []any `json:"lines"`
		NetFeeMinor int64 `json:"net_fee_minor"`
	}
	env.do("GET", "/v1/billing/acquisition-fees", env.tenantID, nil, http.StatusOK, &inv)
	if inv.NetFeeMinor != 0 || len(inv.Lines) != 0 {
		t.Fatalf("empty tenant invoiced: %+v", inv)
	}

	// acquisition_config rides the selective patch and survives unrelated edits.
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"acquisition_config": map[string]any{"fee_pct": 0.12, "goal_clients": 50, "channels": []string{"seo"}},
	}, http.StatusOK, nil)
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"operator_config": map[string]any{"alert_phones": []string{"+971500000097"}},
	}, http.StatusOK, nil)
	var got model.Tenant
	env.do("GET", "/v1/tenant", env.tenantID, nil, http.StatusOK, &got)
	if got.AcquisitionConfig.FeePct != 0.12 || got.AcquisitionConfig.GoalClients != 50 ||
		len(got.AcquisitionConfig.Channels) != 1 || got.AcquisitionConfig.Channels[0] != "seo" {
		t.Fatalf("acquisition config not persisted: %+v", got.AcquisitionConfig)
	}
	if len(got.OperatorConfig.AlertPhones) != 1 {
		t.Fatalf("unrelated patch not applied: %+v", got.OperatorConfig)
	}
}

func TestSitePages(t *testing.T) {
	env := newEnv(t) // slug "taylor"

	var svc model.CatalogService
	env.do("POST", "/v1/catalog", env.tenantID, model.CatalogService{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Airport Transfer",
		BaseMinor: 12000, Currency: "EUR", AutoQuotable: true, Active: true,
	}, http.StatusCreated, &svc)
	env.do("POST", "/v1/geo/areas", env.tenantID, map[string]any{
		"name":    "Amsterdam Centrum",
		"polygon": [][]float64{{52.36, 4.88}, {52.38, 4.88}, {52.38, 4.91}, {52.36, 4.91}},
	}, http.StatusCreated, nil)

	// Landing page: SSR HTML with the tenant name, service link, JSON-LD.
	resp, err := http.Get(env.server.URL + "/site/taylor/")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	for _, want := range []string{
		"Taylor Travel Management", "Airport Transfer", "application/ld+json",
		"LocalBusiness", `rel="canonical"`, "channel=seo", "Amsterdam Centrum",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("landing missing %q", want)
		}
	}

	// Service page carries an Offer with price; the CTA attributes to seo.
	resp, err = http.Get(env.server.URL + "/site/taylor/book/" + svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	body = readAll(t, resp)
	for _, want := range []string{"Airport Transfer", `"@type":"Offer"`, "120.00", "svc-"} {
		if !strings.Contains(body, want) {
			t.Fatalf("service page missing %q", want)
		}
	}

	// Area page renders the programmatic service × location content.
	areaID := ""
	{
		var areas []model.ServiceArea
		env.do("GET", "/v1/geo/areas", env.tenantID, nil, http.StatusOK, &areas)
		if len(areas) != 1 {
			t.Fatalf("areas: %d", len(areas))
		}
		areaID = areas[0].ID
	}
	resp, err = http.Get(env.server.URL + "/site/taylor/areas/" + areaID)
	if err != nil {
		t.Fatal(err)
	}
	body = readAll(t, resp)
	if !strings.Contains(body, "Amsterdam Centrum") || !strings.Contains(body, "areaServed") {
		t.Fatal("area page incomplete")
	}

	// Sitemap + robots.
	resp, _ = http.Get(env.server.URL + "/site/taylor/sitemap.xml")
	body = readAll(t, resp)
	if !strings.Contains(body, "/book/"+svc.ID) || !strings.Contains(body, "/areas/"+areaID) {
		t.Fatal("sitemap incomplete")
	}
	resp, _ = http.Get(env.server.URL + "/site/taylor/robots.txt")
	if !strings.Contains(readAll(t, resp), "Sitemap:") {
		t.Fatal("robots.txt missing sitemap")
	}

	// Unknown slug / unknown service are clean 404s, never 500s.
	resp, _ = http.Get(env.server.URL + "/site/nope/")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown slug: %d", resp.StatusCode)
	}
	resp, _ = http.Get(env.server.URL + "/site/taylor/book/nope")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown service: %d", resp.StatusCode)
	}
}

// ------------------------------------------------------------- Phase 27

func TestOnboardingEndpoints(t *testing.T) {
	env := newEnv(t)

	var rep struct {
		Track string `json:"track"`
		Steps []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"steps"`
		Total    int    `json:"total"`
		NextStep string `json:"next_step"`
	}
	env.do("GET", "/v1/onboarding", env.tenantID, nil, http.StatusOK, &rep)
	if rep.Track != "existing" { // flagship archetype default
		t.Fatalf("track: %q", rep.Track)
	}
	byID := map[string]string{}
	for _, s := range rep.Steps {
		byID[s.ID] = s.Status
	}
	if byID["website"] != "done" || byID["catalog"] != "pending" {
		t.Fatalf("derived statuses wrong: %+v", byID)
	}

	// Track + skip via PUT; skip survives in the next read.
	env.do("PUT", "/v1/onboarding", env.tenantID,
		map[string]any{"track": "new", "skip": []string{"team"}}, http.StatusOK, nil)
	env.do("GET", "/v1/onboarding", env.tenantID, nil, http.StatusOK, &rep)
	if rep.Track != "new" {
		t.Fatalf("track not persisted: %q", rep.Track)
	}
	for _, s := range rep.Steps {
		if s.ID == "team" && s.Status != "skipped" {
			t.Fatalf("team should be skipped: %+v", s)
		}
	}
	// onboarding_config also survives an unrelated selective patch.
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"operator_config": map[string]any{"alert_phones": []string{"+971500000098"}},
	}, http.StatusOK, nil)
	var got model.Tenant
	env.do("GET", "/v1/tenant", env.tenantID, nil, http.StatusOK, &got)
	if got.OnboardingConfig.Track != "new" || len(got.OnboardingConfig.Skipped) != 1 {
		t.Fatalf("onboarding config wiped by unrelated patch: %+v", got.OnboardingConfig)
	}
}

func TestImportEndpoints(t *testing.T) {
	env := newEnv(t)

	var catRes struct {
		Created int      `json:"created"`
		Skipped int      `json:"skipped"`
		Errors  []string `json:"errors"`
	}
	env.do("POST", "/v1/import/catalog", env.tenantID, map[string]any{
		"items": []map[string]any{
			{"vertical": "ground", "name": "Airport Transfer", "base_minor": 9500},
			{"vertical": "ground", "name": "Airport Transfer", "base_minor": 9500},
		},
	}, http.StatusOK, &catRes)
	if catRes.Created != 1 || catRes.Skipped != 1 {
		t.Fatalf("catalog import: %+v", catRes)
	}

	var cliRes struct {
		Created int `json:"created"`
		Skipped int `json:"skipped"`
	}
	env.do("POST", "/v1/import/clients", env.tenantID, map[string]any{
		"items": []map[string]any{
			{"full_name": "Ada", "phone": "+447700900010"},
			{"full_name": "Dup", "phone": "+447700900010"},
		},
	}, http.StatusOK, &cliRes)
	if cliRes.Created != 1 || cliRes.Skipped != 1 {
		t.Fatalf("client import: %+v", cliRes)
	}
}

func TestMarketingSurfaces(t *testing.T) {
	env := newEnv(t)

	// QR: PNG bytes for a printable landing code.
	req, _ := http.NewRequest("GET", env.server.URL+"/v1/marketing/qr?target=landing", nil)
	req.Header.Set("X-Tenant-ID", env.tenantID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), "\x89PNG") {
		t.Fatalf("qr: status %d, %d bytes", resp.StatusCode, len(body))
	}
	// Unknown target shape → 422 (httpx validation).
	env.do("GET", "/v1/marketing/qr?target=bogus", env.tenantID, nil, http.StatusUnprocessableEntity, nil)

	// WhatsApp links: unconfigured until brand.whatsapp_number is set.
	var wa struct {
		Configured bool `json:"configured"`
		Links      []struct {
			URL string `json:"url"`
		} `json:"links"`
	}
	env.do("GET", "/v1/marketing/wa-links", env.tenantID, nil, http.StatusOK, &wa)
	if wa.Configured {
		t.Fatal("wa links should be unconfigured without a number")
	}
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"brand": map[string]any{"whatsapp_number": "+971501112233", "tagline": "Travel, arranged."},
	}, http.StatusOK, nil)
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "ground", "name": "Airport Transfer", "base_minor": 9500, "currency": "EUR",
	}, http.StatusCreated, nil)
	env.do("GET", "/v1/marketing/wa-links", env.tenantID, nil, http.StatusOK, &wa)
	if !wa.Configured || len(wa.Links) != 1 || !strings.Contains(wa.Links[0].URL, "wa.me/971501112233") {
		t.Fatalf("wa links: %+v", wa)
	}

	// Partner co-marketing: link + zero conversions.
	var partner struct {
		ID string `json:"id"`
	}
	env.do("POST", "/v1/partners", env.tenantID, map[string]any{
		"name": "Hotel Grande", "verticals": []string{"ground"},
	}, http.StatusCreated, &partner)
	var pa struct {
		Link     string `json:"link"`
		Bookings int    `json:"bookings"`
	}
	env.do("GET", "/v1/partners/"+partner.ID+"/acquisition", env.tenantID, nil, http.StatusOK, &pa)
	if !strings.Contains(pa.Link, "channel=partner") || pa.Bookings != 0 {
		t.Fatalf("partner acquisition: %+v", pa)
	}
}

func TestTrackReviewAndPublicPages(t *testing.T) {
	env := newEnv(t)

	// Garbage token → 403; valid token for a missing assignment → 404.
	env.do("POST", "/v1/track/not-a-token/review", "", map[string]any{"rating": 5},
		http.StatusForbidden, nil)
	var link struct {
		Token string `json:"token"`
	}
	env.do("POST", "/v1/dispatch/assignments/asg-nope/track-link", env.tenantID, nil,
		http.StatusOK, &link)
	env.do("POST", "/v1/track/"+link.Token+"/review", "", map[string]any{"rating": 5},
		http.StatusNotFound, nil)

	// Link-in-bio page + embed widget are public and carry the tenant.
	req, _ := http.NewRequest("GET", env.server.URL+"/site/taylor/links", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page := string(readAll(t, resp))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "Taylor Travel Management") ||
		!strings.Contains(page, "channel=social") {
		t.Fatalf("links page: status %d", resp.StatusCode)
	}
	req, _ = http.NewRequest("GET", env.server.URL+"/embed/taylor", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page = string(readAll(t, resp))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "channel=widget") {
		t.Fatalf("embed widget: status %d", resp.StatusCode)
	}
	// Sitemap lists the links page.
	req, _ = http.NewRequest("GET", env.server.URL+"/site/taylor/sitemap.xml", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page = string(readAll(t, resp))
	resp.Body.Close()
	if !strings.Contains(page, "/links") {
		t.Fatal("sitemap missing /links")
	}
}
