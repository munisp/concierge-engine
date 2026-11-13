// Quote engine v0 (Master Spec FR-B2): tenant pricing rules -> instant quote
// for auto-quotable shapes; anything else falls back to a human quote with an
// SLA timer (Taylor's 1-hour promise is the default SLA).
package domain

import (
	"math"
	"strconv"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// haversine returns distance in kilometres between two coordinates.
func haversine(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadiusKM = 6371.0
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusKM * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// PriceItem prices one service item against a catalog service.
func PriceItem(cat model.CatalogService, item model.ServiceItem, rules model.PricingRules) []model.QuoteLine {
	lines := []model.QuoteLine{}
	base := model.QuoteLine{Description: cat.Name + " — base", AmountMinor: cat.BaseMinor, ItemID: item.ID}
	lines = append(lines, base)

	switch item.Vertical {
	case "CHAUFFEUR_POINT_TO_POINT", "AIRPORT_MEET_GREET", "HELICOPTER":
		if cat.PerKMMinor > 0 && item.DropoffLat != 0 {
			km := haversine(item.PickupLat, item.PickupLng, item.DropoffLat, item.DropoffLng)
			// road-distance approximation: haversine x 1.3 circuity factor
			km = km * 1.3
			if rules.PerKMAdjustmentPct != 0 {
				km = km * (1 + rules.PerKMAdjustmentPct)
			}
			amount := int64(math.Round(km * float64(cat.PerKMMinor)))
			lines = append(lines, model.QuoteLine{
				Description: cat.Name + " — distance (" + formatKM(km) + ")",
				AmountMinor: amount, ItemID: item.ID,
			})
		}
	case "CHAUFFEUR_HOURLY":
		if cat.PerHourMinor > 0 && item.Hours > 0 {
			lines = append(lines, model.QuoteLine{
				Description: cat.Name + " — hourly",
				AmountMinor: int64(item.Hours) * cat.PerHourMinor, ItemID: item.ID,
			})
		}
	}
	return lines
}

func formatKM(km float64) string {
	return strconv.FormatFloat(km, 'f', 1, 64) + " km"
}

// BuildQuote assembles a full quote for a request from catalog + tenant rules.
// Returns (quote, instant) — instant=false signals human-quote fallback.
func BuildQuote(tenant model.Tenant, catalog []model.CatalogService, r *model.ServiceRequest, now time.Time) (lines []model.QuoteLine, total, deposit int64, currency string, instant bool) {
	rules := tenant.PricingRules
	currency = rules.Currency
	if currency == "" {
		currency = "EUR"
	}
	instant = true
	for _, item := range r.Items {
		var cat *model.CatalogService
		for i := range catalog {
			c := catalog[i]
			if c.Vertical == item.Vertical && (item.VehicleClass == "" || classOf(c.Name) == item.VehicleClass) {
				cat = &catalog[i]
				break
			}
		}
		if cat == nil || !cat.AutoQuotable {
			instant = false // human-quote fallback with SLA timer
			continue
		}
		for _, l := range PriceItem(*cat, item, rules) {
			lines = append(lines, l)
			total += l.AmountMinor
		}
	}
	if rules.DepositPct > 0 {
		deposit = int64(math.Round(float64(total) * rules.DepositPct))
	}
	return lines, total, deposit, currency, instant
}

func classOf(name string) string {
	// Catalog names encode the class as suffix, e.g. "Business Sedan|business".
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '|' {
			return name[i+1:]
		}
	}
	return ""
}

// QuoteExpiry computes the expiry timestamp from tenant rules.
func QuoteExpiry(rules model.PricingRules, now time.Time) time.Time {
	ttl := rules.QuoteTTLSeconds
	if ttl <= 0 {
		ttl = 3600
	}
	return now.Add(time.Duration(ttl) * time.Second)
}

// PlatformFeeMinor computes the platform take rate (2–5% per vertical/deal).
func PlatformFeeMinor(rules model.PricingRules, total int64) int64 {
	pct := rules.PlatformFeePct
	if pct <= 0 {
		pct = 0.03
	}
	return int64(math.Round(float64(total) * pct))
}
