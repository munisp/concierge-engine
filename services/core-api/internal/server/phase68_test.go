package server_test

// Phase 68 over HTTP: the partner portal shows the partner their own
// bill-to-desk statements — accruing (OPEN), payable with derived due
// date (ISSUED), and paid (SETTLED) — through the same magic-link token,
// and a suspended partner stays locked out.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

type portalView68 struct {
	PartnerName string `json:"partner_name"`
	Folios      []struct {
		ID          string `json:"id"`
		Period      string `json:"period"`
		Currency    string `json:"currency"`
		State       string `json:"state"`
		TotalMinor  int64  `json:"total_minor"`
		IssuedAt    string `json:"issued_at"`
		DueAt       string `json:"due_at"`
		OverdueDays int    `json:"overdue_days"`
		Lines       []struct {
			GuestName   string `json:"guest_name"`
			AmountMinor int64  `json:"amount_minor"`
		} `json:"lines"`
	} `json:"folios"`
}

func portalToken68(t *testing.T, env *testEnv, partnerID string) string {
	t.Helper()
	var link struct {
		URL string `json:"url"`
	}
	env.do("GET", "/v1/partners/"+partnerID+"/portal-link", env.tenantID, nil, http.StatusOK, &link)
	i := strings.Index(link.URL, "token=")
	if i < 0 {
		t.Fatalf("no token in portal link: %s", link.URL)
	}
	return link.URL[i+len("token="):]
}

func TestPortalFolioVisibility68(t *testing.T) {
	env := newEnv(t)

	// A desk-billing hotel partner with one sourced, confirmed booking.
	var p model.Partner
	env.do("POST", "/v1/partners", env.tenantID, map[string]any{
		"name": "Portal Grand", "verticals": []string{"CHAUFFEUR_POINT_TO_POINT"},
		"payout_rail": "MANUAL", "status": "VETTED", "kind": "hotel", "phone": "+31695550688",
	}, http.StatusCreated, &p)
	env.do("PUT", "/v1/partners/"+p.ID+"/bill-to-desk", env.tenantID,
		map[string]any{"enabled": true}, http.StatusOK, &p)
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "name": "Sedan|http68",
		"base_minor": 7500, "currency": "EUR", "per_km_minor": 0,
		"auto_quotable": true, "active": true,
	}, http.StatusCreated, nil)
	var keyRes struct {
		Secret string `json:"secret"`
	}
	env.do("POST", "/v1/partners/"+p.ID+"/keys", env.tenantID,
		map[string]string{"name": "desk-68"}, http.StatusCreated, &keyRes)
	var req model.ServiceRequest
	env.doBearer("POST", "/v1/partner/requests", keyRes.Secret, map[string]any{
		"guest_name": "Portal Guest", "guest_phone": "+31700000068",
		"items": []map[string]any{{
			"vertical": "CHAUFFEUR_POINT_TO_POINT", "pickup_address": "Grand",
			"dropoff_address": "Schiphol", "pickup_at": time.Now().Add(48 * time.Hour).Unix(),
			"passengers": 1,
		}}, "idempotency_key": "pf-http-a",
	}, http.StatusCreated, &req)
	var q model.Quote
	env.do("POST", "/v1/requests/"+req.ID+"/quote", env.tenantID, nil, http.StatusOK, &q)
	env.do("POST", "/v1/quotes/"+q.ID+"/confirm", env.tenantID, nil, http.StatusCreated, nil)

	token := portalToken68(t, env, p.ID)

	// Accruing: the OPEN folio is visible in the portal with its line.
	var view portalView68
	env.doBearer("GET", "/v1/portal/"+token, "", nil, http.StatusOK, &view)
	if view.PartnerName != "Portal Grand" || len(view.Folios) != 1 {
		t.Fatalf("portal view (open): %+v", view)
	}
	if view.Folios[0].State != "OPEN" || view.Folios[0].TotalMinor != 7500 ||
		view.Folios[0].DueAt != "" || len(view.Folios[0].Lines) != 1 ||
		view.Folios[0].Lines[0].GuestName != "Portal Guest" {
		t.Fatalf("open folio in portal: %+v", view.Folios[0])
	}

	// Issue → the portal shows the statement payable with a derived due
	// date (default 14-day terms) and zero days overdue.
	var f model.Folio
	env.do("POST", "/v1/partners/"+p.ID+"/folios/"+view.Folios[0].Period+"/EUR/issue",
		env.tenantID, nil, http.StatusOK, &f)
	env.doBearer("GET", "/v1/portal/"+token, "", nil, http.StatusOK, &view)
	if len(view.Folios) != 1 || view.Folios[0].State != "ISSUED" || view.Folios[0].DueAt == "" {
		t.Fatalf("issued folio in portal: %+v", view.Folios)
	}
	issuedAt, err1 := time.Parse(time.RFC3339, view.Folios[0].IssuedAt)
	dueAt, err2 := time.Parse(time.RFC3339, view.Folios[0].DueAt)
	if err1 != nil || err2 != nil || dueAt.Sub(issuedAt) != 14*24*time.Hour ||
		view.Folios[0].OverdueDays > 0 {
		t.Fatalf("derived due/overdue: issued=%s due=%s overdue=%d",
			view.Folios[0].IssuedAt, view.Folios[0].DueAt, view.Folios[0].OverdueDays)
	}

	// Settle → the portal shows the statement paid, still visible history.
	env.do("POST", "/v1/folios/"+f.ID+"/settle", env.tenantID, nil, http.StatusOK, &f)
	env.doBearer("GET", "/v1/portal/"+token, "", nil, http.StatusOK, &view)
	if len(view.Folios) != 1 || view.Folios[0].State != "SETTLED" {
		t.Fatalf("settled folio in portal: %+v", view.Folios)
	}

	// A partner with no desk billing gets no folios key — and the portal
	// payload is otherwise unchanged (best-effort, never a lockout).
	var p2 model.Partner
	env.do("POST", "/v1/partners", env.tenantID, map[string]any{
		"name": "No Folios Tours", "verticals": []string{"CHAUFFEUR_POINT_TO_POINT"},
		"payout_rail": "MANUAL", "status": "VETTED",
	}, http.StatusCreated, &p2)
	var view2 portalView68
	env.doBearer("GET", "/v1/portal/"+portalToken68(t, env, p2.ID), "", nil, http.StatusOK, &view2)
	if view2.PartnerName != "No Folios Tours" || len(view2.Folios) != 0 {
		t.Fatalf("no-folio partner: %+v", view2)
	}

	// Suspended partner: the magic link stops working entirely.
	env.do("PUT", "/v1/partners/"+p.ID+"/status", env.tenantID,
		map[string]string{"status": "SUSPENDED"}, http.StatusOK, &p)
	env.doBearer("GET", "/v1/portal/"+token, "", nil, http.StatusForbidden, nil)
}
