package server_test

// Phase 45 server tests: the self-service consent center is public
// (token-as-credential, no tenant header), the console mint endpoint is
// tenant-scoped, the form toggle flows through the evidence ledger, and
// the receipt downloads as JSON.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
)

// noFollow keeps the 303 after a consent toggle observable instead of
// transparently following it back to the page.
var noFollow = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}}

func publicGet45(t *testing.T, env *testEnv, path string, want int) string {
	t.Helper()
	resp, err := http.Get(env.server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("GET %s: got %d want %d", path, resp.StatusCode, want)
	}
	return readAll(t, resp)
}

func TestConsentCenterEndpoint45(t *testing.T) {
	env := newEnv(t)

	var rr domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID, map[string]any{
		"phone": "+15550004321", "name": "Center Buyer",
	}, http.StatusOK, &rr)
	cl := *rr.Client

	// Console-facing mint endpoint is tenant-scoped.
	var link struct {
		URL string `json:"url"`
	}
	env.do("GET", "/v1/clients/"+cl.ID+"/consent-link", env.tenantID, nil, http.StatusOK, &link)
	if !strings.Contains(link.URL, "/v1/consent/") {
		t.Fatalf("link: %q", link.URL)
	}
	token := strings.SplitN(link.URL, "/v1/consent/", 2)[1]

	var other model.Tenant
	env.do("POST", "/v1/platform/tenants", "", model.Tenant{
		Slug: "otherco", Name: "Other Co", Archetype: "smb",
	}, http.StatusCreated, &other)
	env.do("GET", "/v1/clients/"+cl.ID+"/consent-link", other.ID, nil, http.StatusNotFound, nil)

	// Public page — NO tenant header; the token is the credential.
	page := publicGet45(t, env, "/v1/consent/"+token, http.StatusOK)
	for _, want := range []string{
		"Your privacy choices", "Taylor Travel Management", "Marketing",
		"Service updates", "Your consent record", "registration_default",
		"Download consent receipt", "Switch off", // US number: marketing on
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("page missing %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "Switch on") {
		t.Fatal("no scope should offer Switch on for a fresh US client")
	}

	// Locked scope refuses through the form path.
	form := url.Values{"scope": {"service"}, "granted": {"false"}}
	resp, err := noFollow.PostForm(env.server.URL+"/v1/consent/"+token+"/set", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("locked scope: got %d", resp.StatusCode)
	}

	// Toggle marketing off — 303 back to the page, evidence recorded.
	resp, err = noFollow.PostForm(env.server.URL+"/v1/consent/"+token+"/set",
		url.Values{"scope": {"marketing"}, "granted": {"false"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("toggle: got %d", resp.StatusCode)
	}

	var evs []model.ConsentEvent
	env.do("GET", "/v1/clients/"+cl.ID+"/consent-history", env.tenantID, nil, http.StatusOK, &evs)
	if len(evs) != 4 || evs[0].Source != model.ConsentSourceSelfService || evs[0].Granted {
		t.Fatalf("self-service evidence: %+v", evs[0])
	}

	// The page reflects the new state immediately.
	page = publicGet45(t, env, "/v1/consent/"+token, http.StatusOK)
	if !strings.Contains(page, "Switch on") || !strings.Contains(page, "self_service") {
		t.Fatal("page did not reflect the toggle")
	}

	// Receipt downloads as a JSON attachment with the evidence trail.
	req, _ := http.NewRequest("GET", env.server.URL+"/v1/consent/"+token+"/receipt", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("receipt: got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatal("receipt must download as an attachment")
	}
	body := readAll(t, resp)
	for _, want := range []string{`"receipt_id"`, `"controller":"Taylor Travel Management"`, `"self_service"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("receipt missing %q", want)
		}
	}

	// Garbage and cross-family tokens are refused publicly.
	publicGet45(t, env, "/v1/consent/garbage.token", http.StatusForbidden)
	publicGet45(t, env, "/v1/consent/"+env.svc.Trips.UnsubTokenFor(env.tenantID, cl.ID), http.StatusForbidden)
}
