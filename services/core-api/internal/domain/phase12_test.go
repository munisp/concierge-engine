// Phase 17 domain tests: tax engine (flat / stripe-tax / none, inclusive vs
// exclusive, deposit on grand total, engine provenance on the quote) and
// per-tenant payment configuration (PSP disable in routing and discovery).
package domain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"concierge-engine/services/core-api/internal/model"
)

// quoteWithTax seeds a tenant + catalog + request and returns the quote.
func quoteWithTax(t *testing.T, s *Services, tenantID string) *model.Quote {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Catalog.Add(ctx, &model.CatalogService{
		TenantID: tenantID, Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Sedan|business",
		BaseMinor: 10000, Currency: "EUR", AutoQuotable: true, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	rr, err := s.Clients.Resolve(ctx, tenantID, "+49159"+NewID()[:8], "", "Tax Client")
	if err != nil {
		t.Fatal(err)
	}
	sr, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
		TenantID: tenantID, ClientID: rr.Client.ID, IdempotencyKey: "tax-" + NewID()[:8],
		Items: []model.ServiceItem{{
			Vertical: "CHAUFFEUR_POINT_TO_POINT", PickupAddress: "A", DropoffAddress: "B",
			PickupAt: 1733500000, Passengers: 1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	q, instant, err := s.Booking.QuoteRequest(ctx, tenantID, sr.ID)
	if err != nil || !instant {
		t.Fatalf("quote: %v instant=%v", err, instant)
	}
	return q
}

func setTenantTax(t *testing.T, s *Services, tenantID string, cfg model.TaxConfig) {
	t.Helper()
	ctx := context.Background()
	tenant, err := s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	tenant.TaxConfig = cfg
	if err := s.Store.Tenants().UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}
}

func TestTaxNoneByDefault(t *testing.T) {
	s := testServices(t, Deps{})
	tenantID := seedTenant(t, s)
	q := quoteWithTax(t, s, tenantID)
	if q.TaxMinor != 0 || q.TaxEngine != "" || q.TotalMinor != 10000 || len(q.Lines) != 1 {
		t.Fatalf("no-config quote must carry no tax: %+v", q)
	}
}

func TestTaxFlatExclusive(t *testing.T) {
	s := testServices(t, Deps{})
	tenantID := seedTenant(t, s)
	setTenantTax(t, s, tenantID, model.TaxConfig{Engine: "flat", Country: "DE", VATRateBP: 1900})

	q := quoteWithTax(t, s, tenantID)
	if q.TaxMinor != 1900 || q.TaxEngine != "flat" || q.TotalMinor != 11900 {
		t.Fatalf("exclusive VAT: %+v", q)
	}
	last := q.Lines[len(q.Lines)-1]
	if last.Description != "VAT 19.0% (DE)" || last.AmountMinor != 1900 {
		t.Fatalf("tax line: %+v", last)
	}
	// Store roundtrip keeps the provenance.
	got, err := s.Store.Quotes().Get(context.Background(), tenantID, q.ID)
	if err != nil || got.TaxMinor != 1900 || got.TaxEngine != "flat" {
		t.Fatalf("store roundtrip: %+v %v", got, err)
	}
}

func TestTaxFlatInclusive(t *testing.T) {
	s := testServices(t, Deps{})
	tenantID := seedTenant(t, s)
	setTenantTax(t, s, tenantID, model.TaxConfig{
		Engine: "flat", Country: "DE", VATRateBP: 1900, PricesIncludeTax: true})

	q := quoteWithTax(t, s, tenantID)
	// tax inside 11900-style pricing: subtotal 10000 incl. -> 1597 tax
	if q.TaxEngine != "flat" || q.TaxMinor != 1597 {
		t.Fatalf("inclusive VAT: %+v", q)
	}
}

func TestTaxStripeEngineWithFallback(t *testing.T) {
	// Fake Stripe Tax: 20% on everything.
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tax/calculations" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"tax_amount_exclusive": 2000})
	}))
	defer fake.Close()

	s := testServices(t, Deps{})
	s.Stripe = NewStripeClient("sk_test", fake.URL, "", nil)
	tenantID := seedTenant(t, s)
	setTenantTax(t, s, tenantID, model.TaxConfig{Engine: "stripe-tax", Country: "US"})

	q := quoteWithTax(t, s, tenantID)
	if q.TaxMinor != 2000 || q.TaxEngine != "stripe-tax" || q.TotalMinor != 12000 {
		t.Fatalf("stripe-tax: %+v", q)
	}

	// Stripe down -> honest fallback to the tenant's flat rate.
	s2 := testServices(t, Deps{})
	s2.Stripe = NewStripeClient("sk_test", "http://127.0.0.1:1", "", nil)
	tenantID2 := seedTenant(t, s2)
	setTenantTax(t, s2, tenantID2, model.TaxConfig{Engine: "stripe-tax", VATRateBP: 1900})
	q2 := quoteWithTax(t, s2, tenantID2)
	if q2.TaxMinor != 1900 || q2.TaxEngine != "flat" {
		t.Fatalf("fallback must price flat and say so: %+v", q2)
	}
}

func TestTaxDepositOnGrandTotal(t *testing.T) {
	s := testServices(t, Deps{})
	tenantID := seedTenant(t, s)
	ctx := context.Background()
	tenant, _ := s.Store.Tenants().Get(ctx, tenantID)
	tenant.PricingRules.DepositPct = 0.30
	if err := s.Store.Tenants().UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	setTenantTax(t, s, tenantID, model.TaxConfig{Engine: "flat", VATRateBP: 2000})

	q := quoteWithTax(t, s, tenantID)
	// subtotal 10000 + 20% = 12000; deposit 30% of grand total = 3600
	if q.TotalMinor != 12000 || q.DepositMinor != 3600 {
		t.Fatalf("deposit must be on grand total: %+v", q)
	}
}

func TestCountryFromLocale(t *testing.T) {
	if got := countryFromLocale("de-DE"); got != "DE" {
		t.Fatalf("de-DE -> %q", got)
	}
	if got := countryFromLocale("en_US"); got != "US" {
		t.Fatalf("en_US -> %q", got)
	}
	if got := countryFromLocale("en"); got != "" {
		t.Fatalf("regionless locale -> %q", got)
	}
}

// ------------------------------------------------------------- tenant payment config

func TestTenantPaymentConfigRouting(t *testing.T) {
	s := testServices(t, Deps{})
	s.Stripe = NewStripeClient("sk_test", "http://unused", "", nil)
	s.Paystack = NewPaystackClient("sk_test", "http://unused")

	// EU tenant without a Stripe entity: EUR falls to paystack (configured),
	// and disabling both leaves sandbox.
	pc := model.PaymentConfig{DisableStripe: true}
	if got := s.Payments.choosePSP("EUR", "", pc); got != "paystack" {
		t.Fatalf("stripe disabled: %q", got)
	}
	both := model.PaymentConfig{DisableStripe: true, DisablePaystack: true}
	if got := s.Payments.choosePSP("NGN", "", both); got != "sandbox" {
		t.Fatalf("both disabled: %q", got)
	}
	// Explicit request for a disabled rail is rejected at link creation.
	if got := s.Payments.choosePSP("EUR", "paystack", model.PaymentConfig{DisablePaystack: true}); got != "paystack" {
		t.Fatalf("explicit request must win (rejected upstream): %q", got)
	}
}

func TestPaymentMethodsRespectsTenantConfig(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	tenant, _ := s.Store.Tenants().Get(ctx, tenantID)
	tenant.PaymentConfig = model.PaymentConfig{DisablePaystack: true}
	if err := s.Store.Tenants().UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	methods, err := s.Payments.PaymentMethods(ctx, tenantID, "NGN")
	if err != nil {
		t.Fatal(err)
	}
	if len(methods) != 1 || methods[0].PSP != "stripe" {
		t.Fatalf("paystack must be filtered for this tenant: %+v", methods)
	}
}
