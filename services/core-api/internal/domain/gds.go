// GDS supply port (Phase 3, Master Spec §14 — "GDS/hotels/events
// integrations; NDC seed with Amadeus"). Live-first with a deterministic mock
// degrade so dev/CI never need credentials; the attach event feeds the
// attach-rate metric that the Amadeus pilot is judged on.
package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// GDSPort is the travel-content supply interface — Amadeus NDC first, but
// hotels/events providers plug into the same shape.
type GDSPort interface {
	SearchFlights(ctx context.Context, origin, dest, date string, adults int) ([]model.FlightOffer, error)
	SearchHotels(ctx context.Context, cityCode, checkIn, checkOut string, adults int) ([]model.HotelOffer, error)
	// FlightStatus (Phase 61): live status for one flight on one date.
	// Providers without a status feed return a SCHEDULED report with
	// Source "scheduled" — never a fabricated ETA.
	FlightStatus(ctx context.Context, flightNumber, date string) (*model.FlightStatusReport, error)
	Name() string
}

// ------------------------------------------------------------- Amadeus

type AmadeusAdapter struct {
	base, clientID, clientSecret string
	hc                           *http.Client

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

func NewAmadeusAdapter(base, clientID, clientSecret string) *AmadeusAdapter {
	if base == "" {
		base = "https://test.api.amadeus.com" // sandbox; prod is api.amadeus.com
	}
	return &AmadeusAdapter{
		base:     strings.TrimSuffix(base, "/"),
		clientID: clientID, clientSecret: clientSecret,
		hc: &http.Client{Timeout: 15 * time.Second},
	}
}

func (a *AmadeusAdapter) Name() string { return "amadeus" }

// Live reports whether real credentials are configured.
func (a *AmadeusAdapter) Live() bool { return a.clientID != "" && a.clientSecret != "" }

// token fetches (and caches) a client-credentials token.
func (a *AmadeusAdapter) tokenFor(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && time.Now().Before(a.tokenExpiry.Add(-30*time.Second)) {
		return a.token, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {a.clientID},
		"client_secret": {a.clientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.base+"/v1/security/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("amadeus auth %d", resp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("amadeus auth: malformed token response")
	}
	a.token = tok.AccessToken
	a.tokenExpiry = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return a.token, nil
}

func (a *AmadeusAdapter) get(ctx context.Context, path string, q url.Values, out any) error {
	token, err := a.tokenFor(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("amadeus %s: %d", path, resp.StatusCode)
	}
	return json.Unmarshal(body, out)
}

func (a *AmadeusAdapter) SearchFlights(ctx context.Context, origin, dest, date string, adults int) ([]model.FlightOffer, error) {
	var res struct {
		Data []struct {
			ID    string `json:"id"`
			Price struct {
				Total    string `json:"total"`
				Currency string `json:"currency"`
			} `json:"price"`
			Itineraries []struct {
				Segments []struct {
					Departure struct {
						IataCode string `json:"iataCode"`
						At       string `json:"at"`
					} `json:"departure"`
					Arrival struct {
						IataCode string `json:"iataCode"`
						At       string `json:"at"`
					} `json:"arrival"`
					CarrierCode string `json:"carrierCode"`
					Number      string `json:"number"`
				} `json:"segments"`
			} `json:"itineraries"`
		} `json:"data"`
	}
	err := a.get(ctx, "/v2/shopping/flight-offers", url.Values{
		"originLocationCode":      {origin},
		"destinationLocationCode": {dest},
		"departureDate":           {date},
		"adults":                  {strconv.Itoa(adults)},
		"max":                     {"5"},
		"currencyCode":            {"EUR"},
	}, &res)
	if err != nil {
		return nil, err
	}
	var out []model.FlightOffer
	for _, d := range res.Data {
		if len(d.Itineraries) == 0 || len(d.Itineraries[0].Segments) == 0 {
			continue
		}
		first := d.Itineraries[0].Segments[0]
		last := d.Itineraries[0].Segments[len(d.Itineraries[0].Segments)-1]
		totalF, _ := strconv.ParseFloat(d.Price.Total, 64)
		out = append(out, model.FlightOffer{
			ID: "fl-" + d.ID, Source: "amadeus",
			Origin: first.Departure.IataCode, Destination: last.Arrival.IataCode,
			DepartureAt: first.Departure.At, ArrivalAt: last.Arrival.At,
			Carrier: first.CarrierCode, FlightNo: first.CarrierCode + first.Number,
			Cabin:      "BUSINESS", // premium platform default
			TotalMinor: int64(totalF*100 + 0.5), Currency: d.Price.Currency,
			OfferRef: d.ID,
		})
	}
	return out, nil
}

func (a *AmadeusAdapter) SearchHotels(ctx context.Context, cityCode, checkIn, checkOut string, adults int) ([]model.HotelOffer, error) {
	// Two-step: city -> hotel ids, then offers for those ids.
	var list struct {
		Data []struct {
			HotelID string `json:"hotelId"`
			Name    string `json:"name"`
		} `json:"data"`
	}
	if err := a.get(ctx, "/v1/reference-data/locations/hotels/by-city", url.Values{
		"cityCode": {cityCode}, "radius": {"10"}, "radiusUnit": {"KM"},
	}, &list); err != nil {
		return nil, err
	}
	ids := make([]string, 0, 5)
	names := map[string]string{}
	for _, h := range list.Data {
		if len(ids) < 5 {
			ids = append(ids, h.HotelID)
			names[h.HotelID] = h.Name
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var offers struct {
		Data []struct {
			Hotel struct {
				HotelID string `json:"hotelId"`
				Name    string `json:"name"`
			} `json:"hotel"`
			Offers []struct {
				ID       string `json:"id"`
				CheckIn  string `json:"checkInDate"`
				CheckOut string `json:"checkOutDate"`
				Room     struct {
					TypeEstimated struct {
						Category string `json:"category"`
					} `json:"typeEstimated"`
				} `json:"room"`
				Price struct {
					Total    string `json:"total"`
					Currency string `json:"currency"`
				} `json:"price"`
			} `json:"offers"`
		} `json:"data"`
	}
	if err := a.get(ctx, "/v3/shopping/hotel-offers", url.Values{
		"hotelIds": {strings.Join(ids, ",")}, "checkInDate": {checkIn},
		"checkOutDate": {checkOut}, "adults": {strconv.Itoa(adults)},
	}, &offers); err != nil {
		return nil, err
	}
	var out []model.HotelOffer
	for _, d := range offers.Data {
		if len(d.Offers) == 0 {
			continue
		}
		o := d.Offers[0]
		totalF, _ := strconv.ParseFloat(o.Price.Total, 64)
		out = append(out, model.HotelOffer{
			ID: "ho-" + o.ID, Source: "amadeus",
			HotelName: d.Hotel.Name, City: cityCode,
			CheckIn: o.CheckIn, CheckOut: o.CheckOut,
			RoomType:   o.Room.TypeEstimated.Category,
			TotalMinor: int64(totalF*100 + 0.5), Currency: o.Price.Currency,
			OfferRef: o.ID,
		})
	}
	return out, nil
}

// FlightStatus queries the Amadeus schedule/status feed. Without live
// credentials the adapter reports timetable-only (SCHEDULED, source
// "scheduled") — honest degrade, no invented delays or ETAs.
func (a *AmadeusAdapter) FlightStatus(ctx context.Context, flightNumber, date string) (*model.FlightStatusReport, error) {
	if !a.Live() {
		return &model.FlightStatusReport{
			FlightNumber: flightNumber, Date: date,
			Status: model.LegScheduled, Source: "scheduled",
		}, nil
	}
	// Amadeus On-Demand Flight Status: GET /v2/schedule/flights
	// (carrierCode + flightNumber + scheduledDepartureDate).
	carrier, number := flightNumber[:2], flightNumber[2:]
	q := url.Values{}
	q.Set("carrierCode", carrier)
	q.Set("flightNumber", number)
	q.Set("scheduledDepartureDate", date)
	var out struct {
		Data []struct {
			FlightPoints []struct {
				Departure *struct {
					Timings []struct {
						Value string `json:"value"`
						Delay string `json:"delay"`
					} `json:"timings"`
					Terminal string `json:"terminal"`
				} `json:"departure"`
				Arrival *struct {
					Timings []struct {
						Value string `json:"value"`
						Delay string `json:"delay"`
					} `json:"timings"`
					Terminal string `json:"terminal"`
				} `json:"arrival"`
			} `json:"flightPoints"`
		} `json:"data"`
	}
	if err := a.get(ctx, "/v2/schedule/flights", q, &out); err != nil {
		return nil, err
	}
	rep := &model.FlightStatusReport{
		FlightNumber: flightNumber, Date: date,
		Status: model.LegScheduled, Source: "live",
	}
	if len(out.Data) > 0 && len(out.Data[0].FlightPoints) > 0 {
		fp := out.Data[0].FlightPoints
		if arr := fp[len(fp)-1].Arrival; arr != nil {
			rep.Terminal = arr.Terminal
			for _, tm := range arr.Timings {
				if tm.Value != "" {
					rep.ETALocal = tm.Value
				}
				if tm.Delay != "" {
					rep.DelayMinutes = parseISO8601Minutes(tm.Delay)
					if rep.DelayMinutes > 0 {
						rep.Status = model.LegDelayed
					}
				}
			}
		}
	}
	return rep, nil
}

// parseISO8601Minutes converts "PT1H40M"/"PT40M" delays to minutes.
func parseISO8601Minutes(d string) int {
	d = strings.TrimPrefix(d, "PT")
	total := 0
	if i := strings.Index(d, "H"); i >= 0 {
		var h int
		fmt.Sscanf(d[:i], "%d", &h)
		total += h * 60
		d = d[i+1:]
	}
	if i := strings.Index(d, "M"); i >= 0 {
		var m int
		fmt.Sscanf(d[:i], "%d", &m)
		total += m
	}
	return total
}

// ------------------------------------------------------------- mock (dev/CI)

type MockGDS struct{}

func (m *MockGDS) Name() string { return "mock" }

func (m *MockGDS) SearchFlights(_ context.Context, origin, dest, date string, adults int) ([]model.FlightOffer, error) {
	return []model.FlightOffer{
		{ID: "fl-mock-1", Source: "mock", Origin: origin, Destination: dest,
			DepartureAt: date + "T08:15:00", ArrivalAt: date + "T10:05:00",
			Carrier: "KL", FlightNo: "KL1234", Cabin: "BUSINESS",
			TotalMinor: int64(48900 * adults), Currency: "EUR", OfferRef: "mock-offer-1"},
		{ID: "fl-mock-2", Source: "mock", Origin: origin, Destination: dest,
			DepartureAt: date + "T13:40:00", ArrivalAt: date + "T15:30:00",
			Carrier: "BA", FlightNo: "BA441", Cabin: "BUSINESS",
			TotalMinor: int64(54200 * adults), Currency: "EUR", OfferRef: "mock-offer-2"},
	}, nil
}

// FlightStatus — mock is always on time, clearly labeled scheduled.
func (m *MockGDS) FlightStatus(_ context.Context, flightNumber, date string) (*model.FlightStatusReport, error) {
	return &model.FlightStatusReport{
		FlightNumber: flightNumber, Date: date,
		Status: model.LegScheduled, Source: "scheduled",
	}, nil
}

func (m *MockGDS) SearchHotels(_ context.Context, cityCode, checkIn, checkOut string, adults int) ([]model.HotelOffer, error) {
	return []model.HotelOffer{
		{ID: "ho-mock-1", Source: "mock", HotelName: "Grand Hotel Krasnapolsky", City: cityCode,
			CheckIn: checkIn, CheckOut: checkOut, RoomType: "DELUXE",
			TotalMinor: 189000, Currency: "EUR", OfferRef: "mock-hotel-1"},
		{ID: "ho-mock-2", Source: "mock", HotelName: "The Dylan Amsterdam", City: cityCode,
			CheckIn: checkIn, CheckOut: checkOut, RoomType: "SUITE",
			TotalMinor: 264000, Currency: "EUR", OfferRef: "mock-hotel-2"},
	}, nil
}

// ------------------------------------------------------------- service

// liver is implemented by adapters that can report whether real credentials
// are configured — avoids type-asserting to a concrete adapter.
type liver interface {
	Live() bool
}

type GDSService struct {
	s    *Services
	live GDSPort
	mock GDSPort
}

func NewGDSService(s *Services, live GDSPort) *GDSService {
	if live == nil {
		live = NewAmadeusAdapter("", "", "")
	}
	return &GDSService{s: s, live: live, mock: &MockGDS{}}
}

// liveEnabled reports whether the live adapter has credentials configured.
func (svc *GDSService) liveEnabled() bool {
	if l, ok := svc.live.(liver); ok {
		return l.Live()
	}
	return svc.live != nil // non-liver ports are assumed configured
}

func (svc *GDSService) SearchFlights(ctx context.Context, tenantID, origin, dest, date string, adults int) ([]model.FlightOffer, string, error) {
	if origin == "" || dest == "" || date == "" || adults <= 0 {
		return nil, "", fmt.Errorf("validation")
	}
	source := "mock"
	var offers []model.FlightOffer
	if svc.liveEnabled() {
		if live, err := svc.live.SearchFlights(ctx, origin, dest, date, adults); err == nil {
			offers, source = live, svc.live.Name()
		}
	}
	if offers == nil {
		var err error
		if offers, err = svc.mock.SearchFlights(ctx, origin, dest, date, adults); err != nil {
			return nil, "", err
		}
		source = "mock"
	}
	svc.s.Events.Emit(ctx, tenantID, "supply", "gds", "supply.flights_searched",
		map[string]any{"origin": origin, "dest": dest, "results": len(offers), "source": source})
	return offers, source, nil
}

func (svc *GDSService) SearchHotels(ctx context.Context, tenantID, cityCode, checkIn, checkOut string, adults int) ([]model.HotelOffer, string, error) {
	if cityCode == "" || checkIn == "" || checkOut == "" || adults <= 0 {
		return nil, "", fmt.Errorf("validation")
	}
	source := "mock"
	var offers []model.HotelOffer
	if svc.liveEnabled() {
		if live, err := svc.live.SearchHotels(ctx, cityCode, checkIn, checkOut, adults); err == nil {
			offers, source = live, svc.live.Name()
		}
	}
	if offers == nil {
		var err error
		if offers, err = svc.mock.SearchHotels(ctx, cityCode, checkIn, checkOut, adults); err != nil {
			return nil, "", err
		}
		source = "mock"
	}
	svc.s.Events.Emit(ctx, tenantID, "supply", "gds", "supply.hotels_searched",
		map[string]any{"city": cityCode, "results": len(offers), "source": source})
	return offers, source, nil
}

// FlightStatus (Phase 61): live-first like search — a credentialed
// adapter answers directly; anything else reports timetable-only
// (SCHEDULED, source "scheduled"). Never a fabricated ETA.
func (svc *GDSService) FlightStatus(ctx context.Context, flightNumber, date string) (*model.FlightStatusReport, error) {
	if flightNumber == "" || date == "" {
		return nil, fmt.Errorf("validation")
	}
	if svc.liveEnabled() {
		if rep, err := svc.live.FlightStatus(ctx, flightNumber, date); err == nil {
			return rep, nil
		}
	}
	return svc.mock.FlightStatus(ctx, flightNumber, date)
}

// AttachOffer links a GDS offer to a booking — the attach-rate proof point
// (Amadeus pilot metric): air/hotel attached to a VIP ground booking.
func (svc *GDSService) AttachOffer(ctx context.Context, tenantID, bookingID, offerType, offerRef string, totalMinor int64, currency string) error {
	if offerType != "flight" && offerType != "hotel" {
		return fmt.Errorf("validation")
	}
	if _, err := svc.s.Store.Bookings().Get(ctx, tenantID, bookingID); err != nil {
		return err
	}
	svc.s.Events.Emit(ctx, tenantID, "booking", bookingID, "supply.attached",
		map[string]any{"offer_type": offerType, "offer_ref": offerRef,
			"total_minor": totalMinor, "currency": currency})
	svc.s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "gds_attach", RequestID: bookingID,
	})
	return nil
}
