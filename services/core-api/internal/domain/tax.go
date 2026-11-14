package domain

// Phase 17: tax engine — VAT/GST/sales-tax on quotes for the EU/US launch.
//
// Three engines, selected per tenant (Tenant.TaxConfig.Engine):
//   - ""          none: B2B/exempt or unregistered — totals carry no tax.
//   - "flat"      tenant-configured rate (basis points) on the service
//                 subtotal. Deterministic, works offline, correct for a
//                 single-registration domestic launch.
//   - "stripe-tax" live Stripe Tax calculation per quote. Falls back to the
//                 flat rate when Stripe is unreachable or unconfigured —
//                 a degraded quote beats no quote, and the engine label on
//                 the quote records which one actually priced it.
//
// Prices-included (EU B2C convention) and prices-excluded (US convention)
// are both supported. The tax line is appended to the quote lines so every
// downstream surface (console, capture page, receipts) renders it; Quote.
// TaxMinor/TaxEngine carry the machine-readable provenance.
//
// Honest boundary: this computes the tax component of OUR quote. Filing,
// remittance, and multi-registration nexus determination belong to the
// tenant's Stripe Tax dashboard / accountant — not to this engine.

import (
	"context"
	"fmt"
	"math"
	"strings"

	"concierge-engine/services/core-api/internal/model"
)

// countryFromLocale extracts the ISO country subtag from a BCP-47 locale
// ("de-DE" -> "DE"). Best-effort signal for Stripe Tax's customer country.
func countryFromLocale(locale string) string {
	parts := strings.Split(strings.ReplaceAll(locale, "_", "-"), "-")
	for _, p := range parts[1:] {
		if len(p) == 2 {
			return strings.ToUpper(p)
		}
	}
	return ""
}

// applyTax computes the tax component for a quote and returns the display
// line plus the machine fields. (0, "", nil) = no tax configured.
func (svc *BookingService) applyTax(ctx context.Context, tenant *model.Tenant,
	subtotal int64, currency, clientLocale string) (taxLine *model.QuoteLine, taxMinor int64, engine string) {
	cfg := tenant.TaxConfig
	if subtotal <= 0 {
		return nil, 0, ""
	}
	rateBP := 0
	switch cfg.Engine {
	case "stripe-tax":
		if svc.s.Stripe.Enabled() {
			country := cfg.Country
			if country == "" {
				country = countryFromLocale(clientLocale)
			}
			amount, err := svc.s.Stripe.CalculateTax(ctx, currency, subtotal, country)
			if err == nil && amount >= 0 {
				taxMinor, engine = amount, "stripe-tax"
			}
			// fall through to flat on failure — engine label stays honest
		}
		if taxMinor == 0 && cfg.VATRateBP > 0 {
			rateBP = cfg.VATRateBP
			engine = "flat"
		}
	case "flat":
		if cfg.VATRateBP > 0 {
			rateBP = cfg.VATRateBP
			engine = "flat"
		}
	}
	if engine == "" {
		return nil, 0, ""
	}
	if rateBP > 0 {
		if cfg.PricesIncludeTax {
			// tax = subtotal - subtotal/(1+rate) — the tax inside the price
			taxMinor = subtotal - int64(math.Round(float64(subtotal)/(1+float64(rateBP)/10000)))
		} else {
			taxMinor = int64(math.Round(float64(subtotal) * float64(rateBP) / 10000))
		}
	}
	if taxMinor <= 0 {
		return nil, 0, ""
	}
	label := "VAT"
	if rateBP > 0 {
		label = fmt.Sprintf("VAT %.1f%%", float64(rateBP)/100)
	}
	if cfg.Country != "" {
		label += " (" + cfg.Country + ")"
	}
	return &model.QuoteLine{Description: label, AmountMinor: taxMinor}, taxMinor, engine
}
