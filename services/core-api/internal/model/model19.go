package model

import "time"

// Phase 65: partner rate cards — negotiated per-channel pricing. One card
// per (tenant, partner, vertical): a hotel's guests pay the fixed airport
// transfer they contracted, a travel agency gets 10% off chauffeur hourly,
// a transport company farm-in carries a markup. Cards apply only to demand
// the partner SOURCED (SourcePartnerID) and every adjustment lands on the
// quote as an explicit, partner-named line — repricing is never silent.

const (
	RateCardPctDiscount = "pct_discount" // Value = percent off, 1–50
	RateCardPctMarkup   = "pct_markup"   // Value = percent on, 1–50
	RateCardFixedTotal  = "fixed_total"  // Value = fixed item subtotal, minor units; Currency required
)

// ValidRateCardKind guards the closed vocabulary.
func ValidRateCardKind(k string) bool {
	switch k {
	case RateCardPctDiscount, RateCardPctMarkup, RateCardFixedTotal:
		return true
	}
	return false
}

// MaxRateCardPct caps percentage adjustments — a deeper "discount" is a
// pricing decision for the tenant's own catalog, not a rate card.
const MaxRateCardPct = 50

type RateCard struct {
	TenantID  string    `json:"tenant_id"`
	PartnerID string    `json:"partner_id"`
	Vertical  string    `json:"vertical"`
	Kind      string    `json:"kind"`
	Value     int64     `json:"value"` // percent (1–50) or minor units (fixed_total)
	Currency  string    `json:"currency,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
