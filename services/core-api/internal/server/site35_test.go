package server_test

// Phase 35 — customer acquisition & outreach: server-surface tests for the
// public acquisition pages (A1 airport, A2 routes, A3 estimate, A4 quote
// share, A6 links, A7 referer attribution, A9 corporate capture, A12
// campaign windows, A14 gift storefront, A15 sitemap) and the outreach
// campaign endpoints (O1 broadcast, O2 segment preview, O15 funnel).
//
// Honesty gates under test: pages 404 unless the tenant's real coverage /
// catalog / voucher stock backs them; tampered quote tokens are rejected;
// no coverage is ever invented.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
)

// coverSchiphol registers a service area covering the Randstad: Schiphol
// (52.3105, 4.7683), Amsterdam, Rotterdam and The Hague.
func coverSchiphol(t *testing.T, env *testEnv) {
	t.Helper()
	env.do("POST", "/v1/geo/areas", env.tenantID, map[string]any{
		"name": "Randstad",
		"polygon": [][2]float64{
			{51.8, 4.3}, {52.6, 4.3}, {52.6, 5.1}, {51.8, 5.1},
		},
	}, http.StatusCreated, nil)
}

// addPerKMService registers an auto-quotable per-km chauffeur service.
func addPerKMService(t *testing.T, env *testEnv) model.CatalogService {
	t.Helper()
	var svc model.CatalogService
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "name": "Business Sedan|business",
		"base_minor": 9500, "currency": "EUR", "per_km_minor": 350, "auto_quotable": true,
	}, http.StatusCreated, &svc)
	return svc
}

// ------------------------------------------------------------- A1: airport pages

func TestAirportPage35(t *testing.T) {
	env := newEnv(t)
	addPerKMService(t, env)

	// Unknown IATA code: clean 404, never a invented page.
	if status, _, _ := getHost(t, env.server.URL, "/site/taylor/airport/ZZZ", "", "", ""); status != http.StatusNotFound {
		t.Fatalf("unknown IATA should 404, got %d", status)
	}
	// Known airport but no service-area coverage: honest 404.
	if status, _, _ := getHost(t, env.server.URL, "/site/taylor/airport/AMS", "", "", ""); status != http.StatusNotFound {
		t.Fatalf("uncovered airport should 404, got %d", status)
	}

	coverSchiphol(t, env)
	status, _, body := getHost(t, env.server.URL, "/site/taylor/airport/AMS", "", "", "")
	if status != http.StatusOK {
		t.Fatalf("covered airport page: %d", status)
	}
	for _, want := range []string{"AMS", "Chauffeur transfers and VIP meet", `"@type":"Airport"`, `"iataCode":"AMS"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("airport page missing %q", want)
		}
	}
	// An airport outside the coverage polygon still 404s.
	if status, _, _ := getHost(t, env.server.URL, "/site/taylor/airport/DXB", "", "", ""); status != http.StatusNotFound {
		t.Fatalf("out-of-area airport should 404, got %d", status)
	}
}

// ------------------------------------------------------------- A2: route pages

func TestRoutePage35(t *testing.T) {
	env := newEnv(t)

	// No per-km auto-quotable service yet: honest 404, no invented price.
	if status, _, _ := getHost(t, env.server.URL, "/site/taylor/routes/amsterdam-to-rotterdam", "", "", ""); status != http.StatusNotFound {
		t.Fatalf("route without per-km service should 404, got %d", status)
	}

	addPerKMService(t, env)
	status, _, body := getHost(t, env.server.URL, "/site/taylor/routes/amsterdam-to-rotterdam", "", "", "")
	if status != http.StatusOK {
		t.Fatalf("route page: %d", status)
	}
	for _, want := range []string{"Amsterdam to Rotterdam", "km, from EUR", "indicative"} {
		if !strings.Contains(body, want) {
			t.Fatalf("route page missing %q", want)
		}
	}
	// Unknown city, and same-city pairs: 404.
	if status, _, _ := getHost(t, env.server.URL, "/site/taylor/routes/amsterdam-to-atlantis", "", "", ""); status != http.StatusNotFound {
		t.Fatalf("unknown city should 404, got %d", status)
	}
	if status, _, _ := getHost(t, env.server.URL, "/site/taylor/routes/amsterdam-to-amsterdam", "", "", ""); status != http.StatusNotFound {
		t.Fatalf("same-city pair should 404, got %d", status)
	}
}

// ------------------------------------------------------------- A3: anonymous estimate

func TestSiteEstimate35(t *testing.T) {
	env := newEnv(t)
	svc := addPerKMService(t, env)

	// Priced from the live catalog: AMS -> Amsterdam centre.
	var out struct {
		TotalMinor int64  `json:"total_minor"`
		Currency   string `json:"currency"`
		Indicative bool   `json:"indicative"`
	}
	env.do("POST", "/site/taylor/estimate", "", map[string]any{
		"service_id": svc.ID,
		"pickup_lat": 52.3105, "pickup_lng": 4.7683,
		"dropoff_lat": 52.3676, "dropoff_lng": 4.9041,
	}, http.StatusOK, &out)
	if out.TotalMinor <= 9500 || out.Currency != "EUR" || !out.Indicative {
		t.Fatalf("estimate: %+v", out)
	}

	// Unknown service id: 404.
	env.do("POST", "/site/taylor/estimate", "", map[string]any{
		"service_id": "svc-nope",
		"pickup_lat": 52.3105, "pickup_lng": 4.7683,
		"dropoff_lat": 52.3676, "dropoff_lng": 4.9041,
	}, http.StatusNotFound, nil)
	// Unknown tenant slug: 404.
	env.do("POST", "/site/nope/estimate", "", map[string]any{
		"service_id": svc.ID,
	}, http.StatusNotFound, nil)
}

// ------------------------------------------------------------- A4: quote share

func TestQuoteShare35(t *testing.T) {
	env := newEnv(t)
	addPerKMService(t, env)

	var res domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"phone": "+31600000035", "name": "Share Tester"}, http.StatusOK, &res)
	var sr model.ServiceRequest
	env.do("POST", "/v1/requests", env.tenantID, map[string]any{
		"client_id": res.Client.ID, "idempotency_key": "share-req-1",
		"items": []model.ServiceItem{{
			Vertical: "CHAUFFEUR_POINT_TO_POINT", VehicleClass: "business",
			PickupAddress: "AMS Schiphol", PickupLat: 52.3105, PickupLng: 4.7683,
			DropoffAddress: "Herengracht 1, Amsterdam", DropoffLat: 52.3676, DropoffLng: 4.9041,
			PickupAt: time.Now().Add(24 * time.Hour).Unix(), Passengers: 1,
		}},
	}, http.StatusCreated, &sr)
	var q model.Quote
	env.do("POST", "/v1/requests/"+sr.ID+"/quote", env.tenantID, nil, http.StatusOK, &q)

	var share model.QuoteShare
	env.do("POST", "/v1/quotes/"+q.ID+"/share", env.tenantID, nil, http.StatusCreated, &share)
	if share.Token == "" || share.QuoteID != q.ID || !strings.HasSuffix(share.URL, "/quote/"+share.Token) {
		t.Fatalf("share: %+v", share)
	}

	// Public page renders the priced offer (no tenant header needed).
	status, hdr, body := getHost(t, env.server.URL, "/quote/"+share.Token, "", "", "")
	if status != http.StatusOK {
		t.Fatalf("quote share page: %d", status)
	}
	if cc := hdr.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("priced offer must never be cached: %q", cc)
	}
	for _, want := range []string{"Taylor Travel Management", "AMS Schiphol", "Herengracht"} {
		if !strings.Contains(body, want) {
			t.Fatalf("quote page missing %q", want)
		}
	}

	// Tampered token: rejected, never a rendered quote.
	tampered := share.Token[:len(share.Token)-2] + "xx"
	if status, _, _ := getHost(t, env.server.URL, "/quote/"+tampered, "", "", ""); status != http.StatusForbidden {
		t.Fatalf("tampered token should 403, got %d", status)
	}
	// Unknown quote id behind the API: clean error, not a panic.
	env.do("POST", "/v1/quotes/q-nope/share", env.tenantID, nil, http.StatusNotFound, nil)
}

// ------------------------------------------------------------- A5: leaderboard

func TestReferralLeaderboard35(t *testing.T) {
	env := newEnv(t)
	var out struct {
		Rows []domain.ReferralRank `json:"rows"`
	}
	env.do("GET", "/v1/growth/referral-leaderboard?limit=10", env.tenantID, nil, http.StatusOK, &out)
	if len(out.Rows) != 0 {
		t.Fatalf("fresh tenant should have an empty leaderboard: %+v", out.Rows)
	}
}

// ------------------------------------------------------------- A6/A12: links + windows

func TestMarketingLinks35(t *testing.T) {
	env := newEnv(t)

	type linksResp struct {
		CaptureLinks   map[string]string `json:"capture_links"`
		ActiveCampaign string            `json:"active_campaign"`
		CapturePage    string            `json:"capture_page"`
	}
	var out linksResp
	env.do("GET", "/v1/marketing/links", env.tenantID, nil, http.StatusOK, &out)
	for _, ch := range []string{"seo", "qr", "widget", "social", "email", "ads", "partner"} {
		u := out.CaptureLinks[ch]
		if !strings.Contains(u, "channel="+ch) || !strings.Contains(u, "tenant="+env.tenantID) {
			t.Fatalf("channel %s link malformed: %q", ch, u)
		}
	}
	if out.ActiveCampaign != "" {
		t.Fatalf("no window configured: active campaign should be empty, got %q", out.ActiveCampaign)
	}

	// A12: configure a window covering now — links adopt utm_campaign.
	now := time.Now().UTC()
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"acquisition_config": map[string]any{
			"campaign_windows": []map[string]string{{
				"name":  "winter-launch",
				"start": now.Add(-time.Hour).Format(time.RFC3339),
				"end":   now.Add(time.Hour).Format(time.RFC3339),
			}},
		},
	}, http.StatusOK, nil)
	env.do("GET", "/v1/marketing/links", env.tenantID, nil, http.StatusOK, &out)
	if out.ActiveCampaign != "winter-launch" {
		t.Fatalf("active campaign: %q", out.ActiveCampaign)
	}
	if u := out.CaptureLinks["social"]; !strings.Contains(u, "utm_campaign=winter-launch") {
		t.Fatalf("window link should carry utm_campaign: %q", u)
	}
}

// ------------------------------------------------------------- A7: referer attribution

func TestRefererAttribution35(t *testing.T) {
	env := newEnv(t)

	// No channel supplied: the Referer classifies the event.
	req, _ := http.NewRequest("POST", env.server.URL+"/v1/attribution",
		strings.NewReader(`{"kind":"page_view"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", env.tenantID)
	req.Header.Set("Referer", "https://www.google.com/search?q=chauffeur+amsterdam")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("track: %d", resp.StatusCode)
	}

	var rows []map[string]any
	env.do("GET", "/v1/attribution/funnel", env.tenantID, nil, http.StatusOK, &rows)
	found := false
	for _, row := range rows {
		if row["channel"] == "seo" && fmt.Sprint(row["campaign"]) == "organic:google" {
			found = true
		}
	}
	if !found {
		t.Fatalf("referer should attribute as seo/organic:google: %+v", rows)
	}

	// An explicit channel always wins over the Referer.
	req, _ = http.NewRequest("POST", env.server.URL+"/v1/attribution",
		strings.NewReader(`{"kind":"page_view","channel":"qr"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", env.tenantID)
	req.Header.Set("Referer", "https://www.google.com/search?q=x")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	env.do("GET", "/v1/attribution/funnel", env.tenantID, nil, http.StatusOK, &rows)
	found = false
	for _, row := range rows {
		if row["channel"] == "qr" {
			found = true
		}
	}
	if !found {
		t.Fatalf("explicit channel must win: %+v", rows)
	}
}

// ------------------------------------------------------------- A9: corporate self-serve

func TestCorporateCapture35(t *testing.T) {
	env := newEnv(t)

	var out struct {
		Account      model.CorporateAccount `json:"account"`
		Contact      model.Client           `json:"contact"`
		ContactIsNew bool                   `json:"contact_is_new"`
	}
	env.do("POST", "/v1/capture/corporate", env.tenantID, map[string]any{
		"company_name": "Verhoeven Logistics", "billing_email": "ap@verhoeven.example",
		"contact_name": "Sanne Verhoeven", "contact_phone": "+31600000036",
	}, http.StatusCreated, &out)
	if out.Account.ID == "" || out.Account.Currency != "EUR" {
		t.Fatalf("account should inherit the tenant currency: %+v", out.Account)
	}
	if !out.ContactIsNew || out.Contact.OrgID != out.Account.ID {
		t.Fatalf("contact should be new and linked to the account: %+v", out)
	}

	// Re-submitting with the same phone resolves the same client — the
	// signup form is safe to retry.
	var again struct {
		Contact      model.Client `json:"contact"`
		ContactIsNew bool         `json:"contact_is_new"`
	}
	env.do("POST", "/v1/capture/corporate", env.tenantID, map[string]any{
		"company_name": "Verhoeven Logistics", "contact_phone": "+31600000036",
	}, http.StatusCreated, &again)
	if again.ContactIsNew || again.Contact.ID != out.Contact.ID {
		t.Fatalf("re-capture should resolve the same contact: %+v", again)
	}

	// Validation: no company name, or no reachable contact channel.
	env.do("POST", "/v1/capture/corporate", env.tenantID, map[string]any{
		"contact_phone": "+31600000037",
	}, http.StatusUnprocessableEntity, nil)
	env.do("POST", "/v1/capture/corporate", env.tenantID, map[string]any{
		"company_name": "No Contact BV",
	}, http.StatusUnprocessableEntity, nil)
}

// ------------------------------------------------------------- A14: gift storefront

func TestGiftStorefront35(t *testing.T) {
	env := newEnv(t)

	// No live gift vouchers: honest 404, not an empty storefront.
	if status, _, _ := getHost(t, env.server.URL, "/site/taylor/gift", "", "", ""); status != http.StatusNotFound {
		t.Fatalf("no vouchers should 404, got %d", status)
	}

	env.do("POST", "/v1/promos", env.tenantID, map[string]any{
		"code": "GIFT-250", "credit_minor": 25000, "currency": "EUR", "gift": true,
	}, http.StatusCreated, nil)
	status, _, body := getHost(t, env.server.URL, "/site/taylor/gift", "", "", "")
	if status != http.StatusOK {
		t.Fatalf("gift storefront: %d", status)
	}
	if !strings.Contains(body, "GIFT-250") {
		t.Fatalf("storefront should list the voucher code")
	}
}

// ------------------------------------------------------------- A15: sitemap coverage

func TestSitemapCoverage35(t *testing.T) {
	env := newEnv(t)

	// Bare tenant: none of the Phase 35 URLs appear.
	status, _, body := getHost(t, env.server.URL, "/site/taylor/sitemap.xml", "", "", "")
	if status != http.StatusOK {
		t.Fatalf("sitemap: %d", status)
	}
	for _, absent := range []string{"/airport/", "/routes/", "/gift"} {
		if strings.Contains(body, absent) {
			t.Fatalf("bare sitemap should not list %q", absent)
		}
	}

	// Coverage + per-km service + a live gift voucher: all three appear.
	coverSchiphol(t, env)
	addPerKMService(t, env)
	env.do("POST", "/v1/promos", env.tenantID, map[string]any{
		"code": "GIFT-500", "credit_minor": 50000, "currency": "EUR", "gift": true,
	}, http.StatusCreated, nil)
	_, _, body = getHost(t, env.server.URL, "/site/taylor/sitemap.xml", "", "", "")
	for _, want := range []string{"/airport/AMS", "/routes/", "/gift"} {
		if !strings.Contains(body, want) {
			t.Fatalf("sitemap should list %q", want)
		}
	}
	// Only covered airports are listed — DXB has no coverage.
	if strings.Contains(body, "/airport/DXB") {
		t.Fatalf("sitemap must not list uncovered airports")
	}
}

// ------------------------------------------- O1/O2/O15: broadcast endpoints

func TestBroadcastEndpoints35(t *testing.T) {
	env := newEnv(t)

	// Two clients: one consented to marketing, one not.
	var consented, silent domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"phone": "+31600000038", "name": "Consented Client"}, http.StatusOK, &consented)
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"phone": "+31600000039", "name": "Silent Client"}, http.StatusOK, &silent)
	env.do("PUT", "/v1/clients/"+consented.Client.ID+"/consent", env.tenantID,
		map[string]any{"scope": "marketing", "granted": true}, http.StatusOK, nil)

	// O2: preview counts the audience before anything sends.
	var preview struct {
		Count  int `json:"count"`
		Sample []struct {
			ID string `json:"id"`
		} `json:"sample"`
	}
	env.do("POST", "/v1/outreach/segments/preview", env.tenantID,
		map[string]any{}, http.StatusOK, &preview)
	if preview.Count != 2 || len(preview.Sample) != 2 {
		t.Fatalf("segment preview: %+v", preview)
	}
	// Consent-only rules narrow it to one.
	env.do("POST", "/v1/outreach/segments/preview", env.tenantID,
		map[string]any{"marketing_consent": true}, http.StatusOK, &preview)
	if preview.Count != 1 {
		t.Fatalf("consent-only preview: %+v", preview)
	}

	// O1: broadcast hits the whole segment; the non-consented client is
	// held, never sent (no WhatsApp adapter here, so sends land failed —
	// the record is what matters).
	var bres domain.BroadcastResult
	env.do("POST", "/v1/outreach/broadcast", env.tenantID, map[string]any{
		"campaign": "spring-launch", "body": "Spring fleet is live — reply to book.",
	}, http.StatusOK, &bres)
	if bres.Matched != 2 || bres.Held != 1 || bres.Sent+bres.Failed+bres.Scheduled != 1 {
		t.Fatalf("broadcast: %+v", bres)
	}
	// Idempotent replay: the second broadcast re-tallies the same stored
	// disposition — nothing new is delivered and the ledger gains no rows.
	first := bres
	env.do("POST", "/v1/outreach/broadcast", env.tenantID, map[string]any{
		"campaign": "spring-launch", "body": "Spring fleet is live — reply to book.",
	}, http.StatusOK, &bres)
	if bres != first {
		t.Fatalf("replay should re-tally the stored disposition: %+v vs %+v", bres, first)
	}

	// O15: the funnel reflects the campaign's real ledger rows — exactly
	// two sends exist, so a replay provably recorded nothing new.
	var funnel domain.CampaignFunnel
	env.do("GET", "/v1/outreach/campaigns/spring-launch/funnel", env.tenantID, nil, http.StatusOK, &funnel)
	if funnel.Campaign != "spring-launch" || funnel.Held != 1 || funnel.Sent+funnel.Failed+funnel.Scheduled != 1 {
		t.Fatalf("funnel: %+v", funnel)
	}

	// Campaign name validation: ':' and '#' are reserved key separators.
	env.do("POST", "/v1/outreach/broadcast", env.tenantID, map[string]any{
		"campaign": "bad:name", "body": "x",
	}, http.StatusUnprocessableEntity, nil)
}
