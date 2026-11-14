package domain

// Phase 15: currency hygiene for international payments. ISO-4217 validation
// and the zero-decimal table — amount_minor arithmetic is only correct when
// the exponent is known (¥100 is 100 minor units, not 10 000).

import (
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
)

// zeroDecimalCurrencies have no minor unit (ISO-4217 exponent 0): the minor
// amount equals the major amount. Everything else we serve is exponent 2.
var zeroDecimalCurrencies = map[string]bool{
	"BIF": true, "CLP": true, "DJF": true, "GNF": true, "JPY": true,
	"KMF": true, "KRW": true, "MGA": true, "PYG": true, "RWF": true,
	"UGX": true, "VND": true, "VUV": true, "XAF": true, "XOF": true,
}

// MinorExponent returns the ISO-4217 exponent we support (0 or 2).
func MinorExponent(currency string) int {
	if zeroDecimalCurrencies[strings.ToUpper(currency)] {
		return 0
	}
	return 2
}

// validateCurrency enforces ISO-4217 shape (3 uppercase letters). The set of
// codes a tenant actually accepts is a tenant pricing decision, not a code
// constant — this only rejects malformed input.
func validateCurrency(currency string) error {
	c := strings.ToUpper(strings.TrimSpace(currency))
	if len(c) != 3 {
		return httpx.ErrValidation
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return httpx.ErrValidation
		}
	}
	return nil
}

// PaymentMethod describes one PSP rail available for a currency — the
// answer to "how can this customer actually pay?" per market.
type PaymentMethod struct {
	PSP      string   `json:"psp"`
	Currency string   `json:"currency"`
	Channels []string `json:"channels"` // card | bank_transfer | ussd | mobile_money | ...
	Live     bool     `json:"live"`     // false = sandbox (no PSP key configured)
}

// paystackMarkets maps currencies where Paystack is the default rail
// (Nigeria first — card, bank transfer, USSD; Ghana mobile money).
var paystackCurrencies = map[string][]string{
	"NGN": {"card", "bank_transfer", "ussd"},
	"GHS": {"card", "mobile_money"},
	"ZAR": {"card"},
	"KES": {"card", "mobile_money"},
}

// stripeChannels is what a Stripe Checkout Session offers out of the box
// (dashboard-configured local methods appear automatically in live mode).
var stripeChannels = []string{"card", "wallet", "local_methods"}
