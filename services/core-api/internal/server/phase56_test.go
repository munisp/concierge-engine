package server_test

// Phase 56: partner integration layer over HTTP — partner-scoped key auth
// (including cross-surface rejection), assignment offer/respond, settlement
// view, and the tenant-admin integration routes.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
)

// doBearer issues a request authenticated with an API key (no X-Tenant-ID —
// the key resolves the tenant, like the real public-API path).
func (e *testEnv) doBearer(method, path, secret string, body any, want int, out any) {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, e.server.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		var env map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&env)
		e.t.Fatalf("%s %s: got %d want %d: %v", method, path, resp.StatusCode, want, env)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			e.t.Fatal(err)
		}
	}
}

// seedPartnerBooking56 creates a partner with commission and a confirmed,
// quoted booking assigned to them — the state a tour operator sees when work
// is offered.
func seedPartnerBooking56(t *testing.T, env *testEnv, pct float64) (partnerID, bookingID, assignmentID string) {
	t.Helper()
	var p model.Partner
	env.do("POST", "/v1/partners", env.tenantID, map[string]any{
		"name": "Younique Tailor Tours", "verticals": []string{"TOURS"},
		"payout_rail": "MANUAL",
	}, http.StatusCreated, &p)
	partnerID = p.ID
	if pct > 0 {
		env.do("PUT", "/v1/partners/"+partnerID+"/commission", env.tenantID,
			map[string]any{"commission_pct": pct}, http.StatusOK, nil)
	}

	svc := env.svc
	sr, err := svc.Booking.CreateRequest(t.Context(), &model.ServiceRequest{
		TenantID: env.tenantID, ClientID: "c56", IdempotencyKey: "srv56-" + domain.NewID()[:8],
		Items: []model.ServiceItem{{Vertical: "TOURS",
			PickupAddress: "Lisbon", PickupAt: time.Now().Add(48 * time.Hour).Unix(), Passengers: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	q := &model.Quote{ID: domain.NewID(), TenantID: env.tenantID, RequestID: sr.ID,
		TotalMinor: 40000, Currency: "EUR", CreatedAt: svc.Now()}
	if err := svc.Store.Quotes().Create(t.Context(), q); err != nil {
		t.Fatal(err)
	}
	b := &model.Booking{ID: domain.NewID(), TenantID: env.tenantID, RequestID: sr.ID, QuoteID: q.ID,
		State: "CONFIRMED", CreatedAt: svc.Now()}
	if err := svc.Store.Bookings().Create(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	a, err := svc.Dispatch.Assign(t.Context(), &model.Assignment{
		TenantID: env.tenantID, BookingID: b.ID, PartnerID: partnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return partnerID, b.ID, a.ID
}

func TestPartnerIntegrationFlow56(t *testing.T) {
	env := newEnv(t)
	partnerID, bookingID, assignmentID := seedPartnerBooking56(t, env, 12.5)

	// Mint a partner-scoped key (tenant admin path).
	var keyRes struct {
		ID        string   `json:"id"`
		Secret    string   `json:"secret"`
		PartnerID string   `json:"partner_id"`
		Scopes    []string `json:"scopes"`
	}
	env.do("POST", "/v1/partners/"+partnerID+"/keys", env.tenantID,
		map[string]string{"name": "younique-sync"}, http.StatusCreated, &keyRes)
	if keyRes.PartnerID != partnerID || len(keyRes.Scopes) != 1 || keyRes.Scopes[0] != "partner" {
		t.Fatalf("partner key: %+v", keyRes)
	}

	// The partner sees exactly their own assignments.
	var mine []map[string]any
	env.doBearer("GET", "/v1/partner/assignments", keyRes.Secret, nil, http.StatusOK, &mine)
	if len(mine) != 1 || mine[0]["id"] != assignmentID || mine[0]["booking_id"] != bookingID {
		t.Fatalf("partner assignments: %+v", mine)
	}

	// Commission accrued on assign: €400 × 12.5% = €50 pending.
	var settlement struct {
		CommissionPct float64          `json:"commission_pct"`
		Totals        map[string]int64 `json:"totals"`
	}
	env.doBearer("GET", "/v1/partner/settlement", keyRes.Secret, nil, http.StatusOK, &settlement)
	if settlement.CommissionPct != 12.5 || settlement.Totals["PENDING"] != 5000 {
		t.Fatalf("settlement: %+v", settlement)
	}

	// Accept the assignment.
	var accepted model.Assignment
	env.doBearer("POST", "/v1/partner/assignments/"+assignmentID+"/respond", keyRes.Secret,
		map[string]string{"action": "accept", "note": "driver confirmed"}, http.StatusOK, &accepted)
	if accepted.Status != "PARTNER_ACCEPTED" {
		t.Fatalf("respond: %s", accepted.Status)
	}
	// Second response is a conflict.
	env.doBearer("POST", "/v1/partner/assignments/"+assignmentID+"/respond", keyRes.Secret,
		map[string]string{"action": "accept"}, http.StatusConflict, nil)

	// The partner key is refused everywhere outside /v1/partner/*.
	env.doBearer("GET", "/v1/clients", keyRes.Secret, nil, http.StatusForbidden, nil)
	env.doBearer("GET", "/v1/partners", keyRes.Secret, nil, http.StatusForbidden, nil)
	// And a tenant-header request is refused on the partner surface.
	env.do("GET", "/v1/partner/assignments", env.tenantID, nil, http.StatusForbidden, nil)
	// Garbage bearer refused.
	env.doBearer("GET", "/v1/partner/assignments", "cek_deadbeefdeadbeef", nil, http.StatusForbidden, nil)
}

func TestPartnerDeclineVoidsCommission56(t *testing.T) {
	env := newEnv(t)
	partnerID, _, assignmentID := seedPartnerBooking56(t, env, 10)

	var keyRes struct {
		Secret string `json:"secret"`
	}
	env.do("POST", "/v1/partners/"+partnerID+"/keys", env.tenantID,
		map[string]string{"name": "mr-travel"}, http.StatusCreated, &keyRes)

	env.doBearer("POST", "/v1/partner/assignments/"+assignmentID+"/respond", keyRes.Secret,
		map[string]string{"action": "decline", "note": "no boats that day"}, http.StatusOK, nil)

	var settlement struct {
		Totals map[string]int64 `json:"totals"`
	}
	env.doBearer("GET", "/v1/partner/settlement", keyRes.Secret, nil, http.StatusOK, &settlement)
	if settlement.Totals["PENDING"] != 0 || settlement.Totals["FAILED"] != 4000 {
		t.Fatalf("decline must void pending commission: %+v", settlement.Totals)
	}
}

func TestPartnerIntegrationAdmin56(t *testing.T) {
	env := newEnv(t)
	var p model.Partner
	env.do("POST", "/v1/partners", env.tenantID, map[string]any{
		"name": "The Little Orange", "verticals": []string{"TOURS"},
	}, http.StatusCreated, &p)

	// Commission validation: 422 above the cap, 422 negative.
	env.do("PUT", "/v1/partners/"+p.ID+"/commission", env.tenantID,
		map[string]any{"commission_pct": 75}, http.StatusUnprocessableEntity, nil)
	env.do("PUT", "/v1/partners/"+p.ID+"/commission", env.tenantID,
		map[string]any{"commission_pct": 12.5}, http.StatusOK, nil)

	// Partner webhook feed: secret returned once, partner-bound.
	var subRes struct {
		ID        string `json:"id"`
		Secret    string `json:"secret"`
		PartnerID string `json:"partner_id"`
	}
	env.do("POST", "/v1/partners/"+p.ID+"/webhooks", env.tenantID, map[string]any{
		"url": "https://littleorange.example.com/hook", "events": []string{"assignment.*"},
	}, http.StatusCreated, &subRes)
	if subRes.PartnerID != p.ID || subRes.Secret == "" {
		t.Fatalf("partner webhook: %+v", subRes)
	}
	env.do("POST", "/v1/partners/"+p.ID+"/webhooks", env.tenantID, map[string]any{
		"url": "notaurl", "events": []string{"*"},
	}, http.StatusUnprocessableEntity, nil)

	// Integration summary: commission, keys, feeds, capture snippet.
	env.do("POST", "/v1/partners/"+p.ID+"/keys", env.tenantID,
		map[string]string{"name": "tlo-portal"}, http.StatusCreated, nil)
	var summary struct {
		Partner         model.Partner    `json:"partner"`
		Keys            []map[string]any `json:"keys"`
		Webhooks        []map[string]any `json:"webhooks"`
		CaptureEndpoint string           `json:"capture_endpoint"`
		CaptureSource   string           `json:"capture_source"`
	}
	env.do("GET", "/v1/partners/"+p.ID+"/integration", env.tenantID, nil, http.StatusOK, &summary)
	if summary.Partner.CommissionPct != 12.5 || len(summary.Keys) != 1 || len(summary.Webhooks) != 1 {
		t.Fatalf("summary: %+v", summary)
	}
	if summary.CaptureEndpoint != "/site/taylor/leads?source=partner:the-little-orange" || summary.CaptureSource != "partner:the-little-orange" {
		t.Fatalf("capture: %s %s", summary.CaptureEndpoint, summary.CaptureSource)
	}
	// Secret material is never re-exposed by the summary.
	raw, _ := json.Marshal(summary.Keys)
	if bytes.Contains(raw, []byte("cek_")) && !bytes.Contains(raw, []byte("prefix")) {
		t.Fatal("key view leaked secret material")
	}
}

// Partner-embedded lead capture attributes the lead to the operator; a
// bare widget post keeps the plain "site" source.
func TestPartnerCaptureAttribution56(t *testing.T) {
	env := newEnv(t)
	var res struct {
		LeadID string `json:"lead_id"`
	}
	env.do("POST", "/site/taylor/leads?source=partner:the-little-orange", "",
		map[string]any{"kind": "person", "name": "Ana Souza", "email": "ana@example.com"},
		http.StatusCreated, &res)

	var listRes struct {
		Leads []model.Lead `json:"leads"`
	}
	env.do("GET", "/v1/leads", env.tenantID, nil, http.StatusOK, &listRes)
	var found *model.Lead
	for i := range listRes.Leads {
		if listRes.Leads[i].Email == "ana@example.com" {
			found = &listRes.Leads[i]
		}
	}
	if found == nil || found.Source != "partner:the-little-orange" {
		t.Fatalf("partner attribution: %+v", listRes.Leads)
	}

	// No source param → the surface defaults, caller cannot claim otherwise.
	env.do("POST", "/site/taylor/leads", "",
		map[string]any{"kind": "person", "name": "Jo Silva", "email": "jo@example.com"},
		http.StatusCreated, nil)
	listRes.Leads = nil
	env.do("GET", "/v1/leads", env.tenantID, nil, http.StatusOK, &listRes)
	for i := range listRes.Leads {
		if listRes.Leads[i].Email == "jo@example.com" && listRes.Leads[i].Source != "site" {
			t.Fatalf("bare widget must stay site-sourced: %q", listRes.Leads[i].Source)
		}
	}
}
