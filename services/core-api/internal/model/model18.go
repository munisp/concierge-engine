// Phase 63 domain models: demand channels — the source of every request
// (direct client, hotel concierge desk, travel agency, transport company
// farm-in, tour operator) is first-class on requests and bookings so
// quoting, dispatch, commission and margin analytics all attribute demand
// to the channel that brought it.
package model

// Partner kinds (partners.kind). A partner is simultaneously a supplier
// (fulfilment, Phase 2/56) and a demand channel (sourcing, Phase 63); the
// kind drives channel workflows and margin reporting. Empty on legacy rows
// is normalized to PartnerKindTourOperator (the Phase 56 default).
const (
	PartnerKindHotel            = "hotel"
	PartnerKindTravelAgency     = "travel_agency"
	PartnerKindTransportCompany = "transport_company"
	PartnerKindTourOperator     = "tour_operator"
	PartnerKindOther            = "other"
)

// ValidPartnerKind reports whether k is a recognized partner kind.
func ValidPartnerKind(k string) bool {
	switch k {
	case PartnerKindHotel, PartnerKindTravelAgency, PartnerKindTransportCompany,
		PartnerKindTourOperator, PartnerKindOther:
		return true
	}
	return false
}

// Source kinds on requests/bookings. SourceDirect is the zero-commission
// channel (site, WhatsApp, console, repeat client) — the most lucrative
// demand the platform has, and the report says so explicitly. The other
// kinds mirror partner kinds: sourced demand carries the sourcing
// partner's commission.
const (
	SourceDirect           = "direct"
	SourceHotel            = PartnerKindHotel
	SourceTravelAgency     = PartnerKindTravelAgency
	SourceTransportCompany = PartnerKindTransportCompany
	SourceTourOperator     = PartnerKindTourOperator
	SourceOther            = PartnerKindOther
)

// ValidSourceKind reports whether k is a recognized request source.
func ValidSourceKind(k string) bool {
	return k == SourceDirect || ValidPartnerKind(k)
}

// SourceReportRow is one channel's economics over a reporting window.
// Gross is captured booking value (quote totals of bookings created in the
// window); Commission is accrued sourcing+fulfilment payouts; Net =
// Gross − Commission. Ratios are withheld on mixed currency (Phase 40
// honesty pattern) — a blended number would be a lie.
type SourceReportRow struct {
	SourceKind      string `json:"source_kind"`
	Bookings        int    `json:"bookings"`
	GrossMinor      int64  `json:"gross_minor"`
	CommissionMinor int64  `json:"commission_minor"`
	NetMinor        int64  `json:"net_minor"`
	Currency        string `json:"currency"`
	MixedCurrency   bool   `json:"mixed_currency,omitempty"`
}

// SourceReport is the tenant's demand-channel margin view, sorted by gross
// descending — direct demand normally tops it, which is the point: the
// report makes the commission-free channel visible.
type SourceReport struct {
	Days int               `json:"days"`
	Rows []SourceReportRow `json:"rows"`
	Note string            `json:"note"`
}
