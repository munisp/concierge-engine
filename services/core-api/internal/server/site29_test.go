package server_test

// Phase 29 tests: multi-locale site engine + premium redesign + chat CTAs.
//
// Locale strategy under test: site_config.locales (first = default, served
// unprefixed); alternate locales live at /site/{slug}/{locale}/...; every
// page carries a full hreflang cluster + x-default; unconfigured locale
// prefixes 404; per-resource i18n maps override names/descriptions;
// de/fr/es render localized chrome with English guide prose (honest
// degrade).

import (
	"net/http"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/model"
)

// seedLisbonTenant configures the shared newEnv tenant ("taylor") as a
// bilingual Lisbon operator: pt-PT default + en, localized catalog/areas,
// WhatsApp number on brand.
func seedLisbonTenant(t *testing.T, env *testEnv) model.CatalogService {
	t.Helper()
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{
			"locales": []string{"pt-PT", "en"},
			"brand_i18n": map[string]map[string]string{
				"pt-PT": {"tagline": "Transporte de luxo e tours privados em Lisboa."},
				"en":    {"tagline": "Luxury transport and private tours in Lisbon."},
			},
		},
		"brand": map[string]any{"whatsapp_number": "+351912345678"},
	}, http.StatusOK, nil)

	var svc model.CatalogService
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "PRIVATE_TOUR", "name": "Sintra Private Tour",
		"base_minor": 35000, "currency": "EUR", "active": true,
		"name_i18n": map[string]string{"pt-PT": "Tour Privado de Sintra"},
		"desc_i18n": map[string]string{
			"pt-PT": "Palácios, serra e costa num tour privado de dia inteiro.",
			"en":    "Palaces, mountains and coast on a private full-day tour.",
		},
	}, http.StatusCreated, &svc)
	env.do("POST", "/v1/geo/areas", env.tenantID, map[string]any{
		"name":      "Lisbon",
		"polygon":   [][]float64{{38.69, -9.23}, {38.80, -9.23}, {38.80, -9.09}, {38.69, -9.09}},
		"name_i18n": map[string]string{"pt-PT": "Lisboa"},
	}, http.StatusCreated, nil)
	return svc
}

func TestLocaleTreesAndHreflang29(t *testing.T) {
	env := newEnv(t)
	svc := seedLisbonTenant(t, env)

	// Default locale (pt-PT) at the canonical unprefixed URL.
	_, _, body := getHost(t, env.server.URL, "/site/taylor/", "", "", "")
	for _, want := range []string{
		`<html lang="pt-PT">`,
		"Tour Privado de Sintra",                                    // localized catalog name
		"Reserve o seu concierge",                                   // pt-PT chrome
		"Transporte de luxo e tours privados",                       // brand_i18n tagline
		`rel="canonical" href="`,                                    // canonical present
		`hreflang="pt-PT"`, `hreflang="en"`, `hreflang="x-default"`, // full cluster
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("pt-PT landing missing %q", want)
		}
	}
	// Default-locale canonical must be unprefixed.
	if !strings.Contains(body, `/site/taylor/"`) {
		t.Fatal("default-locale canonical should be the unprefixed URL")
	}

	// Alternate locale at /en/ — localized chrome + English names.
	_, _, body = getHost(t, env.server.URL, "/site/taylor/en/", "", "", "")
	for _, want := range []string{
		`<html lang="en">`,
		"Sintra Private Tour",
		"Book your concierge",
		"Luxury transport and private tours",
		`hreflang="pt-PT"`, `hreflang="en"`, `hreflang="x-default"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("en landing missing %q", want)
		}
	}
	// Per-locale canonical: the en page canonicalizes to its own URL.
	if !strings.Contains(body, `/site/taylor/en/"`) {
		t.Fatal("en canonical should point at the /en/ URL")
	}

	// Service pages localize too, in both trees.
	_, _, body = getHost(t, env.server.URL, "/site/taylor/book/"+svc.ID, "", "", "")
	if !strings.Contains(body, "Tour Privado de Sintra") || !strings.Contains(body, "Perguntas frequentes") {
		t.Fatal("pt-PT service page not localized")
	}
	_, _, body = getHost(t, env.server.URL, "/site/taylor/en/book/"+svc.ID, "", "", "")
	if !strings.Contains(body, "Sintra Private Tour") || !strings.Contains(body, "Common questions") {
		t.Fatal("en service page not localized")
	}

	// Area page: localized area name.
	_, _, body = getHost(t, env.server.URL, "/site/taylor/", "", "", "")
	if !strings.Contains(body, "Lisboa") {
		t.Fatal("pt-PT area name missing from landing")
	}

	// Unconfigured locale prefix must 404 — no duplicate-content pages.
	status, _, _ := getHost(t, env.server.URL, "/site/taylor/fr/", "", "", "")
	if status != http.StatusNotFound {
		t.Fatalf("unconfigured locale: want 404, got %d", status)
	}

	// Sitemap lists every configured locale tree.
	_, _, sm := getHost(t, env.server.URL, "/site/taylor/sitemap.xml", "", "", "")
	for _, want := range []string{"/site/taylor/book/", "/site/taylor/en/book/", "/site/taylor/en/areas/"} {
		if !strings.Contains(sm, want) {
			t.Fatalf("sitemap missing per-locale URL %q", want)
		}
	}
}

func TestRedesignAndChatCTA29(t *testing.T) {
	env := newEnv(t)
	seedLisbonTenant(t, env)

	_, _, body := getHost(t, env.server.URL, "/site/taylor/", "", "", "")
	for _, want := range []string{
		"btn-primary",               // hero primary CTA
		"btn-ghost",                 // WhatsApp ghost CTA
		"wa.me/351912345678",        // chat link built from brand number, + stripped
		"card-top",                  // tour card grid with monogram band
		"Fale connosco no WhatsApp", // localized chat CTA label
		"class=\"trust\"",           // trust bar
		"Confirmação por WhatsApp",  // WhatsApp trust chip (number configured)
		"Pagamento online seguro",   // payment chip (default env has a rail enabled)
		"class=\"sticky\"",          // sticky mobile CTA bar
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("redesigned landing missing %q", want)
		}
	}

	// Honest trust bar: disable both rails → the secure-payment claim
	// disappears rather than lying.
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"payment_config": map[string]any{"disable_stripe": true, "disable_paystack": true},
	}, http.StatusOK, nil)
	_, _, body = getHost(t, env.server.URL, "/site/taylor/", "", "", "")
	if strings.Contains(body, "Pagamento online seguro") || strings.Contains(body, "Secure online payment") {
		t.Fatal("payment trust chip rendered with all rails disabled")
	}
	// FAQ payment answer must follow the same honesty rule.
	var svc model.CatalogService
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "name": "Airport Transfer",
		"base_minor": 12000, "currency": "EUR", "active": true,
	}, http.StatusCreated, &svc)
	_, _, body = getHost(t, env.server.URL, "/site/taylor/en/book/"+svc.ID, "", "", "")
	if strings.Contains(body, "payment link") {
		t.Fatal("FAQ claims a payment link with all rails disabled")
	}
	if !strings.Contains(body, "arranges payment with you directly") {
		t.Fatal("FAQ missing the no-rail payment answer")
	}
}

func TestDegradeChromeOnly29(t *testing.T) {
	env := newEnv(t)
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{"locales": []string{"en", "de"}},
	}, http.StatusOK, nil)
	env.do("POST", "/v1/catalog", env.tenantID, model.CatalogService{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Airport Transfer",
		BaseMinor: 12000, Currency: "EUR", Active: true,
	}, http.StatusCreated, nil)

	_, _, body := getHost(t, env.server.URL, "/site/taylor/de/", "", "", "")
	// German chrome…
	if !strings.Contains(body, "Jetzt buchen") || !strings.Contains(body, `<html lang="de">`) {
		t.Fatal("de chrome missing")
	}
	// …with English body prose (honest degrade — no machine-translated copy).
	if !strings.Contains(body, "arranged end to end") && !strings.Contains(body, "VIP concierge") {
		t.Fatal("de page should fall back to English guide prose")
	}
}

func TestOGAsciiFold29(t *testing.T) {
	env := newEnv(t)
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "PRIVATE_TOUR", "name": "Fátima Sanctuary Tour",
		"base_minor": 28000, "currency": "EUR", "active": true,
	}, http.StatusCreated, nil)

	// The card renderer is ASCII-only; a diacritic-laden name must render a
	// valid PNG (folded), never panic or corrupt the image.
	status, hdr, body := getHost(t, env.server.URL, "/site/taylor/og.png", "", "", "")
	if status != http.StatusOK || !strings.HasPrefix(body, "\x89PNG") {
		t.Fatalf("landing og: %d", status)
	}
	if hdr.Get("Content-Type") != "image/png" {
		t.Fatal("og content-type")
	}
}
