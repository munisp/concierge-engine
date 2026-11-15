package server_test

// Phase 44 server tests: the consent-history endpoint is tenant-scoped,
// the console consent PUT records sourced evidence, unknown sources are
// refused, and the DSAR export carries the consent_history section.

import (
	"net/http"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
)

func TestConsentHistoryEndpoint44(t *testing.T) {
	env := newEnv(t)

	// Create a client via resolve (the ResolveV2 path records genesis
	// evidence rows for the jurisdiction defaults).
	var rr domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID, map[string]any{
		"phone": "+15550001234", "name": "History Buyer",
	}, http.StatusOK, &rr)
	cl := *rr.Client

	// Console toggle with an operator note lands in the ledger.
	env.do("PUT", "/v1/clients/"+cl.ID+"/consent", env.tenantID, map[string]any{
		"scope": "marketing", "granted": false, "detail": "phone call request",
	}, http.StatusOK, nil)

	// Unknown source is a 422, not a silent rewrite — the ledger stays
	// queryable on a closed vocabulary.
	env.do("PUT", "/v1/clients/"+cl.ID+"/consent", env.tenantID, map[string]any{
		"scope": "marketing", "granted": true, "source": "carrier_pigeon",
	}, http.StatusUnprocessableEntity, nil)

	var evs []model.ConsentEvent
	env.do("GET", "/v1/clients/"+cl.ID+"/consent-history", env.tenantID, nil, http.StatusOK, &evs)
	if len(evs) != 4 { // 3 genesis + 1 console revoke
		t.Fatalf("history rows: %d %+v", len(evs), evs)
	}
	if evs[0].Source != model.ConsentSourceConsole || evs[0].Granted || evs[0].Detail != "phone call request" {
		t.Fatalf("latest row: %+v", evs[0])
	}
	genesis := 0
	for _, e := range evs {
		if e.Source == model.ConsentSourceRegistrationDefault {
			genesis++
		}
	}
	if genesis != 3 {
		t.Fatalf("genesis rows: %d", genesis)
	}

	// Cross-tenant access never leaks existence (Get first → 404).
	var other model.Tenant
	env.do("POST", "/v1/platform/tenants", "", model.Tenant{
		Slug: "otherco", Name: "Other Co", Archetype: "smb",
	}, http.StatusCreated, &other)
	env.do("GET", "/v1/clients/"+cl.ID+"/consent-history", other.ID, nil, http.StatusNotFound, nil)

	// The DSAR export carries the consent history section (Phase 44 wraps
	// Phase 16's export — the regulator gets proof, not just state).
	req, err := http.NewRequest("GET", env.server.URL+"/v1/clients/"+cl.ID+"/export", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Tenant-ID", env.tenantID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp)
	if !strings.Contains(body, `"consent_history"`) {
		t.Fatal("export missing consent_history section")
	}
	if !strings.Contains(body, `"registration_default"`) {
		t.Fatal("export consent_history missing genesis rows")
	}
}
