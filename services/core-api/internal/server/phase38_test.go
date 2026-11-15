package server_test

// Phase 38: lead provenance over the wire — contact_basis validation on
// capture, import consent downgrades surfaced as receipt notices, basis
// visible on the board, and the public site surface asserting its own
// self_reported basis regardless of the caller's payload.

import (
	"net/http"
	"strings"
	"testing"
)

func TestLeadImportBasisReceipt38(t *testing.T) {
	env := newEnv(t)
	var res map[string]any
	env.do("POST", "/v1/leads/import", env.tenantID, map[string]any{
		"leads": []map[string]any{
			// Bought list claiming consent, no basis: consent stripped.
			{"kind": "corporate", "name": "Buyer", "company": "Apex", "phone": "+447700900020",
				"consent_marketing": true, "list_source": "cognism-q1-2027"},
			// Partner-consented: grant survives.
			{"kind": "person", "name": "Guest", "phone": "+351961110021",
				"consent_marketing": true, "contact_basis": "partner_consent"},
		},
	}, http.StatusOK, &res)
	if res["created"].(float64) != 2 {
		t.Fatalf("receipt: %v", res)
	}
	notices, ok := res["notices"].([]any)
	if !ok || len(notices) != 1 || notices[0].(map[string]any)["index"].(float64) != 0 {
		t.Fatalf("notices: %v", res["notices"])
	}

	// The board carries the provenance.
	var list map[string]any
	env.do("GET", "/v1/leads?limit=10", env.tenantID, nil, http.StatusOK, &list)
	leads := list["leads"].([]any)
	if len(leads) != 2 {
		t.Fatalf("board: %d leads", len(leads))
	}
	basis := map[string]string{}
	consent := map[string]bool{}
	for _, l := range leads {
		m := l.(map[string]any)
		basis[m["name"].(string)] = m["contact_basis"].(string)
		consent[m["name"].(string)] = m["consent_marketing"].(bool)
		if m["name"] == "Buyer" && m["list_source"] != "cognism-q1-2027" {
			t.Fatalf("list source on wire: %v", m)
		}
	}
	if basis["Buyer"] != "b2b_legitimate_interest" || consent["Buyer"] {
		t.Fatalf("bought row should be consent-free b2b: %v %v", basis, consent)
	}
	if basis["Guest"] != "partner_consent" || !consent["Guest"] {
		t.Fatalf("partner row should keep consent: %v %v", basis, consent)
	}
}

func TestCaptureBasisValidation38(t *testing.T) {
	env := newEnv(t)
	// Direct capture claiming consent on a B2B basis → 422 with a reason.
	var res map[string]any
	env.do("POST", "/v1/leads", env.tenantID, map[string]any{
		"kind": "corporate", "name": "Cold", "company": "ColdCo", "phone": "+971501110060",
		"contact_basis": "b2b_legitimate_interest", "consent_marketing": true,
	}, http.StatusUnprocessableEntity, &res)
	errEnv := res["error"].(map[string]any)
	if !strings.Contains(errEnv["message"].(string), "contact basis") {
		t.Fatalf("error should name the basis gate: %v", res)
	}
	// Unknown basis → 422.
	env.do("POST", "/v1/leads", env.tenantID, map[string]any{
		"kind": "person", "name": "Odd", "phone": "+971501110061", "contact_basis": "telepathy",
	}, http.StatusUnprocessableEntity, nil)
	// B2B basis without consent claims → 201.
	env.do("POST", "/v1/leads", env.tenantID, map[string]any{
		"kind": "corporate", "name": "Cold2", "company": "ColdCo2", "phone": "+971501110062",
		"contact_basis": "b2b_legitimate_interest",
	}, http.StatusCreated, nil)
}

func TestSiteCaptureAssertsBasis38(t *testing.T) {
	env := newEnv(t)
	// The public surface asserts self_reported itself — a caller claiming
	// partner_consent through the site form is ignored, not trusted.
	env.do("POST", "/site/taylor/leads", "", map[string]any{
		"kind": "person", "name": "Web", "phone": "+971501110063",
		"consent_marketing": true, "contact_basis": "partner_consent",
	}, http.StatusCreated, nil)
	var list map[string]any
	env.do("GET", "/v1/leads?limit=10", env.tenantID, nil, http.StatusOK, &list)
	leads := list["leads"].([]any)
	if len(leads) != 1 {
		t.Fatalf("board: %d leads", len(leads))
	}
	m := leads[0].(map[string]any)
	if m["contact_basis"] != "self_reported" || m["source"] != "site" {
		t.Fatalf("site capture must assert self_reported: %v", m)
	}
	// ...and because the prospect genuinely self-reported and ticked the
	// box, the consent grant is legitimate and lands.
	if m["consent_marketing"] != true {
		t.Fatalf("self-reported consent should land: %v", m)
	}
}
