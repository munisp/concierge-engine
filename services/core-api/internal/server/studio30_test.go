package server_test

// Phase 30 tests: Site Studio API (presets, theme, generate/apply, static
// export, CMS integrations) + the tenant analytics overview endpoint.
//
// Deployment shapes under test: same-host (theme tokens land on the live
// SSR site), split-host static export (zip renders through the real site
// dispatcher, internal links relativized, booking CTAs stay absolute to
// the platform), and CMS embed (iframe snippet + WordPress shortcode
// plugin source).

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/model"
)

func TestStudioPresetsAndTheme30(t *testing.T) {
	env := newEnv(t)

	var presets struct {
		Presets []struct {
			Name    string `json:"name"`
			Accent  string `json:"accent"`
			Primary string `json:"primary"`
		} `json:"presets"`
	}
	env.do("GET", "/v1/studio/presets", env.tenantID, nil, http.StatusOK, &presets)
	if len(presets.Presets) != 5 || presets.Presets[0].Name != "noir-gold" {
		t.Fatalf("presets: %+v", presets.Presets)
	}

	// Apply the azure-modern preset — the live site must re-render with the
	// new design tokens on the very next request (same-host shape).
	var res struct {
		Resolved struct {
			Accent  string `json:"accent"`
			Primary string `json:"primary"`
		} `json:"resolved"`
	}
	env.do("PUT", "/v1/studio/theme", env.tenantID, map[string]any{
		"theme": map[string]string{"preset": "azure-modern"},
	}, http.StatusOK, &res)
	if res.Resolved.Accent != "#19a7ce" || res.Resolved.Primary != "#0b2447" {
		t.Fatalf("resolved: %+v", res.Resolved)
	}

	resp, err := http.Get(env.server.URL + "/site/taylor/")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	if !strings.Contains(body, "--accent:#19a7ce") || !strings.Contains(body, "--ink:#0b2447") {
		t.Fatal("landing did not pick up the azure-modern theme tokens")
	}

	// Custom preset with explicit colors + sharp corners.
	env.do("PUT", "/v1/studio/theme", env.tenantID, map[string]any{
		"theme": map[string]string{
			"preset": "custom", "primary": "#101010", "accent": "#FF0055", "corners": "sharp",
		},
	}, http.StatusOK, nil)
	resp, _ = http.Get(env.server.URL + "/site/taylor/")
	body = readAll(t, resp)
	if !strings.Contains(body, "--accent:#ff0055") || !strings.Contains(body, "--radius:0") {
		t.Fatal("custom theme tokens not rendered")
	}

	// Validation: bad hex and unknown preset are rejected (422), never stored.
	env.do("PUT", "/v1/studio/theme", env.tenantID, map[string]any{
		"theme": map[string]string{"preset": "custom", "accent": "red"},
	}, http.StatusUnprocessableEntity, nil)
	env.do("PUT", "/v1/studio/theme", env.tenantID, map[string]any{
		"theme": map[string]string{"preset": "holographic-brutalist"},
	}, http.StatusUnprocessableEntity, nil)

	// The rejected theme must not have stuck: site still renders #FF0055.
	resp, _ = http.Get(env.server.URL + "/site/taylor/")
	body = readAll(t, resp)
	if !strings.Contains(body, "--accent:#ff0055") {
		t.Fatal("rejected theme overwrote stored state")
	}

	// Theme rides the selective tenant patch: an unrelated config update
	// must not wipe it (the Phase 24 wipe regression class).
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"operator_config": map[string]any{"alert_phones": []string{"+971500000096"}},
	}, http.StatusOK, nil)
	resp, _ = http.Get(env.server.URL + "/site/taylor/")
	body = readAll(t, resp)
	if !strings.Contains(body, "--accent:#ff0055") {
		t.Fatal("theme wiped by unrelated tenant patch")
	}
}

func TestStudioGenerateApply30(t *testing.T) {
	env := newEnv(t)
	var svc model.CatalogService
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "PRIVATE_TOUR", "name": "Sintra Private Tour",
		"base_minor": 35000, "currency": "EUR", "active": true,
	}, http.StatusCreated, &svc)

	// No concierge-agent in tests → deterministic template fallback, and the
	// provenance label must say so honestly.
	var draft struct {
		Locale      string            `json:"locale"`
		Tagline     string            `json:"tagline"`
		Description string            `json:"description"`
		ServiceDesc map[string]string `json:"service_descs"`
		Engine      string            `json:"engine"`
	}
	env.do("POST", "/v1/studio/generate", env.tenantID, map[string]any{
		"business_case": "Luxury private tours around Lisbon for families and executives",
		"locale":        "en",
	}, http.StatusOK, &draft)
	if draft.Engine != "template" || draft.Tagline == "" || draft.Description == "" {
		t.Fatalf("draft: %+v", draft)
	}
	if draft.ServiceDesc[svc.ID] == "" {
		t.Fatalf("no description generated for seeded service: %+v", draft.ServiceDesc)
	}

	// Empty business case is a validation error, not a silent generic draft.
	env.do("POST", "/v1/studio/generate", env.tenantID, map[string]any{
		"business_case": "   ",
	}, http.StatusUnprocessableEntity, nil)

	// Apply: tagline lands in brand_i18n, service desc in desc_i18n, and the
	// public site renders both.
	env.do("POST", "/v1/studio/apply", env.tenantID, draft, http.StatusOK, nil)
	resp, err := http.Get(env.server.URL + "/site/taylor/")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	if !strings.Contains(body, draft.Tagline) {
		t.Fatalf("applied tagline %q not on landing", draft.Tagline)
	}

	// All-or-nothing: a draft referencing another tenant's service is
	// rejected outright (422 from ErrValidation on foreign id, or 404).
	env.do("POST", "/v1/studio/apply", env.tenantID, map[string]any{
		"locale": "en", "tagline": "x",
		"service_descs": map[string]string{"svc-foreign": "stolen"},
	}, http.StatusUnprocessableEntity, nil)
}

func TestStudioExport30(t *testing.T) {
	env := newEnv(t)
	var svc model.CatalogService
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "name": "Airport Transfer",
		"base_minor": 12000, "currency": "EUR", "active": true,
	}, http.StatusCreated, &svc)
	env.do("POST", "/v1/geo/areas", env.tenantID, map[string]any{
		"name":    "Amsterdam Centrum",
		"polygon": [][]float64{{52.36, 4.88}, {52.38, 4.88}, {52.38, 4.91}, {52.36, 4.91}},
	}, http.StatusCreated, nil)
	var areas []model.ServiceArea
	env.do("GET", "/v1/geo/areas", env.tenantID, nil, http.StatusOK, &areas)
	if len(areas) != 1 {
		t.Fatalf("areas: %d", len(areas))
	}

	req, _ := http.NewRequest("GET", env.server.URL+"/v1/studio/export", nil)
	req.Header.Set("X-Tenant-ID", env.tenantID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	blob, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content-type: %q", ct)
	}
	if !strings.HasPrefix(string(blob), "PK\x03\x04") {
		t.Fatal("not a zip")
	}

	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(b)
	}
	for _, want := range []string{
		"index.html", "book-" + svc.ID + ".html", "area-" + areas[0].ID + ".html",
		"links.html", "partners.html", "README.txt",
	} {
		if _, ok := files[want]; !ok {
			t.Fatalf("export missing %s (has %v)", want, keysOf(files))
		}
	}

	// Split-host contract: internal links are relativized to the flat
	// layout, booking CTAs stay absolute to the platform (capture funnel
	// with attribution), and the rendered HTML is the real site (tenant
	// name + service present).
	index := files["index.html"]
	if !strings.Contains(index, "book-"+svc.ID+".html") {
		t.Fatal("index links not relativized to flat layout")
	}
	if strings.Contains(index, `href="`+env.server.URL+`/site/taylor/book/`) {
		t.Fatal("absolute internal link leaked into export")
	}
	if !strings.Contains(index, "/capture?tenant="+env.tenantID) {
		t.Fatal("booking CTA lost its absolute platform URL")
	}
	if !strings.Contains(index, "Taylor Travel Management") || !strings.Contains(index, "Airport Transfer") {
		t.Fatal("export is not the real rendered site")
	}
	if !strings.Contains(files["book-"+svc.ID+".html"], `"@type":"Offer"`) {
		t.Fatal("service page lost its structured data in export")
	}
	if !strings.Contains(files["README.txt"], "static site export") {
		t.Fatal("README missing")
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestStudioIntegrations30(t *testing.T) {
	env := newEnv(t)
	var out struct {
		Shapes map[string]map[string]string `json:"shapes"`
	}
	env.do("GET", "/v1/studio/integrations", env.tenantID, nil, http.StatusOK, &out)
	for _, shape := range []string{"hosted", "custom_domain", "static_export", "embed", "wordpress"} {
		if _, ok := out.Shapes[shape]; !ok {
			t.Fatalf("missing deployment shape %q", shape)
		}
	}
	ifr := out.Shapes["embed"]["iframe"]
	if !strings.Contains(ifr, "/embed/taylor") || !strings.HasPrefix(ifr, "<iframe ") {
		t.Fatalf("iframe snippet: %q", ifr)
	}
	wp := out.Shapes["wordpress"]["plugin_php"]
	if !strings.Contains(wp, "add_shortcode('concierge_book'") || !strings.Contains(wp, "/embed/taylor") {
		t.Fatalf("wordpress plugin: %q", wp)
	}
	if !strings.Contains(out.Shapes["hosted"]["url"], "/site/taylor/") {
		t.Fatalf("hosted shape: %+v", out.Shapes["hosted"])
	}
}

func TestAnalyticsOverviewEndpoint30(t *testing.T) {
	env := newEnv(t)

	// Golden path: catalog → request → quote → payment → confirmed booking.
	env.do("POST", "/v1/catalog", env.tenantID, model.CatalogService{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Business Sedan|business",
		BaseMinor: 9500, Currency: "EUR", PerKMMinor: 350, AutoQuotable: true, Active: true,
	}, http.StatusCreated, nil)
	var res struct {
		Client struct {
			ID string `json:"id"`
		} `json:"client"`
	}
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"name": "KPI Client"}, http.StatusOK, &res)
	var sr model.ServiceRequest
	env.do("POST", "/v1/requests", env.tenantID, map[string]any{
		"client_id": res.Client.ID, "idempotency_key": "kpi-req",
		"items": []model.ServiceItem{{
			Vertical: "CHAUFFEUR_POINT_TO_POINT", VehicleClass: "business",
			PickupAddress: "AMS Schiphol", PickupLat: 52.3105, PickupLng: 4.7683,
			DropoffAddress: "Herengracht 1, Amsterdam", DropoffLat: 52.3676, DropoffLng: 4.9041,
			PickupAt: 1893456000, Passengers: 2,
		}},
	}, http.StatusCreated, &sr)
	var q model.Quote
	env.do("POST", "/v1/requests/"+sr.ID+"/quote", env.tenantID, nil, http.StatusOK, &q)
	var intent model.PaymentIntent
	env.do("POST", "/v1/payments/links", env.tenantID, map[string]any{
		"quote_id": q.ID, "mode": "FULL", "idempotency_key": "kpi-pay",
	}, http.StatusCreated, &intent)
	env.do("POST", "/v1/quotes/"+q.ID+"/confirm", env.tenantID, nil, http.StatusCreated, nil)
	env.do("POST", "/v1/payments/webhooks/stripe", env.tenantID, map[string]string{
		"intent_id": intent.ID, "event": "payment.captured", "psp_ref": "ch_kpi",
	}, http.StatusOK, nil)

	var o struct {
		Days   int `json:"days"`
		Totals struct {
			BookingsCreated   int    `json:"bookings_created"`
			BookingsConfirmed int    `json:"bookings_confirmed"`
			GMVMinor          int64  `json:"gmv_minor"`
			CollectedMinor    int64  `json:"collected_minor"`
			Currency          string `json:"currency"`
			NewClients        int    `json:"new_clients"`
		} `json:"totals"`
		ByService []struct {
			Name string `json:"name"`
		} `json:"by_service"`
		Notes []string `json:"notes"`
	}
	env.do("GET", "/v1/analytics/overview?days=30", env.tenantID, nil, http.StatusOK, &o)
	if o.Days != 30 || o.Totals.BookingsCreated != 1 || o.Totals.BookingsConfirmed != 1 {
		t.Fatalf("totals: %+v", o.Totals)
	}
	if o.Totals.GMVMinor != q.TotalMinor || o.Totals.CollectedMinor <= 0 || o.Totals.Currency != "EUR" {
		t.Fatalf("revenue: %+v (quote total %d)", o.Totals, q.TotalMinor)
	}
	if o.Totals.NewClients != 1 {
		t.Fatalf("new clients: %+v", o.Totals)
	}
	if len(o.ByService) != 1 {
		t.Fatalf("service mix: %+v", o.ByService)
	}
	if len(o.Notes) == 0 {
		t.Fatal("honesty notes missing")
	}

	// Window clamps: nonsense days values fall back to the 30-day default,
	// and an empty window is a clean zeroed overview — never a 500.
	env.do("GET", "/v1/analytics/overview?days=bogus", env.tenantID, nil, http.StatusOK, &o)
	if o.Days != 30 {
		t.Fatalf("days clamp: %d", o.Days)
	}

	// Tenant isolation: a second tenant sees none of taylor's business.
	var t2 model.Tenant
	env.do("POST", "/v1/platform/tenants", "", model.Tenant{
		Slug: "other", Name: "Other Co", Archetype: "flagship",
	}, http.StatusCreated, &t2)
	env.do("GET", "/v1/analytics/overview", t2.ID, nil, http.StatusOK, &o)
	if o.Totals.BookingsCreated != 0 || o.Totals.GMVMinor != 0 {
		t.Fatalf("cross-tenant leak: %+v", o.Totals)
	}
}
