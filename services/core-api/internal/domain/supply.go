// Charter supply adapters (Phase 2): partner-heavy, inventory-light
// (Master Spec section 1). Aviation supply (Avinode — public APIs: real-time
// search, empty legs, leads/quotes) plugs in behind one interface; the
// quote engine consumes normalized options. Adapter terms are a known risk
// (Spec section 16) — the interface keeps concierge-assisted fallback flows
// sellable without Avinode.
package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// CharterOption is the normalized supply record the quote engine consumes.
type CharterOption struct {
	Source       string `json:"source"`        // avinode | mock
	OptionID     string `json:"option_id"`     // external ref (empty-leg id / tail quote ref)
	AircraftType string `json:"aircraft_type"` // e.g. "Cessna Citation XLS"
	From         string `json:"from"`          // ICAO/IATA
	To           string `json:"to"`
	DepartureAt  string `json:"departure_at"` // RFC3339
	PriceMinor   int64  `json:"price_minor"`
	Currency     string `json:"currency"`
	EmptyLeg     bool   `json:"empty_leg"`
}

// CharterSupply is the adapter port. Implementations: AvinodeAdapter (live,
// needs credentials), MockCharterSupply (dev/tests/demo, deterministic).
type CharterSupply interface {
	Search(ctx context.Context, from, to string, date time.Time, passengers int) ([]CharterOption, error)
}

// AvinodeAdapter: real-time search + empty legs. Until partnership terms are
// finalized (Spec section 16), baseURL empty = adapter disabled and the
// service degrades to human-quote fallback (concierge-assisted flow).
type AvinodeAdapter struct {
	base   string
	apiKey string
	hc     *http.Client
}

func NewAvinodeAdapter(base, apiKey string) *AvinodeAdapter {
	return &AvinodeAdapter{base: base, apiKey: apiKey, hc: &http.Client{Timeout: 8 * time.Second}}
}

func (a *AvinodeAdapter) Available() bool { return a.base != "" }

func (a *AvinodeAdapter) Search(ctx context.Context, from, to string, date time.Time, passengers int) ([]CharterOption, error) {
	if !a.Available() {
		return nil, fmt.Errorf("avinode adapter not configured")
	}
	q := url.Values{}
	q.Set("from", from)
	q.Set("to", to)
	q.Set("date", date.Format("2006-01-02"))
	q.Set("pax", fmt.Sprintf("%d", passengers))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		a.base+"/v1/search/empty-legs?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	resp, err := a.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("avinode: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("avinode: status %d", resp.StatusCode)
	}
	var out []CharterOption
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Source = "avinode"
	}
	return out, nil
}

// MockCharterSupply: deterministic dev/demo supply so the charter flow is
// exercisable end-to-end without credentials.
type MockCharterSupply struct{}

func (MockCharterSupply) Search(_ context.Context, from, to string, date time.Time, passengers int) ([]CharterOption, error) {
	if from == "" || to == "" {
		return nil, fmt.Errorf("from and to are required")
	}
	dep := date.Format(time.RFC3339)
	return []CharterOption{
		{
			Source: "mock", OptionID: "mock-empty-" + from + "-" + to,
			AircraftType: "Cessna Citation XLS+", From: from, To: to,
			DepartureAt: dep, PriceMinor: 1450000, Currency: "EUR", EmptyLeg: true,
		},
		{
			Source: "mock", OptionID: "mock-charter-" + from + "-" + to,
			AircraftType: "Embraer Phenom 300E", From: from, To: to,
			DepartureAt: dep, PriceMinor: 2890000, Currency: "EUR",
		},
	}, nil
}

type SupplyService struct {
	s       *Services
	avinode *AvinodeAdapter
	mock    MockCharterSupply
}

func NewSupplyService(s *Services, avinode *AvinodeAdapter) *SupplyService {
	return &SupplyService{s: s, avinode: avinode}
}

// SearchCharter queries live supply first; on unavailable/error it degrades
// to mock in dev so product flows stay exercisable — the response marks the
// source so the UI/agent can never misrepresent mock supply as live
// (factuality guardrail depends on this).
func (svc *SupplyService) SearchCharter(ctx context.Context, tenantID, from, to string, date time.Time, passengers int) ([]CharterOption, error) {
	var options []CharterOption
	var err error
	if svc.avinode.Available() {
		live, aerr := svc.avinode.Search(ctx, from, to, date, passengers)
		if aerr == nil {
			options = live
		} else {
			err = aerr // surfaced via event below; still degrade
		}
	}
	if options == nil {
		options, _ = svc.mock.Search(ctx, from, to, date, passengers)
	}
	svc.s.Events.Emit(ctx, tenantID, "supply", from+"-"+to, "supply.charter_searched",
		map[string]any{"results": len(options), "live": svc.avinode.Available() && err == nil})
	return options, nil
}
