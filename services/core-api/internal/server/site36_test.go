package server_test

// Phase 36: lead engine HTTP surface — tenant API, pipeline board,
// transitions, conversion, public capture (utm/referer/honeypot) and
// idempotent replay.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func captureLead36(t *testing.T, env *testEnv, body map[string]any) map[string]any {
	t.Helper()
	var res map[string]any
	env.do("POST", "/v1/leads", env.tenantID, body, http.StatusCreated, &res)
	return res
}

func TestLeadCaptureAPI36(t *testing.T) {
	env := newEnv(t)
	res := captureLead36(t, env, map[string]any{
		"kind": "person", "name": "Ada", "phone": "+971 50 111 0001",
		"consent_marketing": true, "channel": "seo",
	})
	lead := res["lead"].(map[string]any)
	if lead["status"] != "NEW" || lead["kind"] != "person" {
		t.Fatalf("lead: %v", lead)
	}
	if lead["phone"] != "+971501110001" {
		t.Fatalf("phone not normalized: %v", lead["phone"])
	}
	if lead["client_id"] == "" {
		t.Fatal("messaging client not linked")
	}
	if res["deduped"] != false {
		t.Fatalf("first capture deduped? %v", res)
	}

	// Validation: corporate without a company is 422.
	env.do("POST", "/v1/leads", env.tenantID, map[string]any{
		"kind": "corporate", "name": "No Co", "phone": "+971501110002",
	}, http.StatusUnprocessableEntity, nil)
	env.do("POST", "/v1/leads", env.tenantID, map[string]any{
		"kind": "person", "name": "No Contact",
	}, http.StatusUnprocessableEntity, nil)
	env.do("POST", "/v1/leads", env.tenantID, map[string]any{
		"kind": "alien", "name": "X", "phone": "+1",
	}, http.StatusUnprocessableEntity, nil)
}

func TestLeadListFilter36(t *testing.T) {
	env := newEnv(t)
	captureLead36(t, env, map[string]any{"kind": "person", "name": "P1", "phone": "+971501110010"})
	captureLead36(t, env, map[string]any{"kind": "corporate", "name": "C1", "phone": "+971501110011", "company": "Acme"})
	captureLead36(t, env, map[string]any{"kind": "event", "name": "E1", "phone": "+971501110012", "event_type": "gala"})

	var all map[string]any
	env.do("GET", "/v1/leads", env.tenantID, nil, http.StatusOK, &all)
	if len(all["leads"].([]any)) != 3 {
		t.Fatalf("all: %v", all)
	}
	var corp map[string]any
	env.do("GET", "/v1/leads?kind=corporate", env.tenantID, nil, http.StatusOK, &corp)
	if len(corp["leads"].([]any)) != 1 {
		t.Fatalf("kind filter: %v", corp)
	}
	var filtered map[string]any
	env.do("GET", "/v1/leads?status=CONTACTED", env.tenantID, nil, http.StatusOK, &filtered)
	if len(filtered["leads"].([]any)) != 0 {
		t.Fatalf("status filter: %v", filtered)
	}
	// Bad filter values are 422, not silent empty lists.
	env.do("GET", "/v1/leads?kind=alien", env.tenantID, nil, http.StatusUnprocessableEntity, nil)
}

func TestLeadPipelineEndpoint36(t *testing.T) {
	env := newEnv(t)
	captureLead36(t, env, map[string]any{"kind": "person", "name": "P1", "phone": "+971501110020"})
	captureLead36(t, env, map[string]any{"kind": "partner", "name": "N1", "phone": "+971501110021", "company": "Atlas"})

	var sum map[string]any
	env.do("GET", "/v1/leads/pipeline", env.tenantID, nil, http.StatusOK, &sum)
	if sum["total"].(float64) != 2 || sum["open"].(float64) != 2 {
		t.Fatalf("pipeline: %v", sum)
	}
	byKind := sum["by_kind"].(map[string]any)
	if byKind["person"].(float64) != 1 || byKind["partner"].(float64) != 1 {
		t.Fatalf("by_kind: %v", byKind)
	}
}

func TestLeadTransitionConvertEndpoints36(t *testing.T) {
	env := newEnv(t)
	res := captureLead36(t, env, map[string]any{
		"kind": "corporate", "name": "Eve", "phone": "+971501110030",
		"email": "eve@initech.com", "company": "Initech",
	})
	lead := res["lead"].(map[string]any)
	id := lead["id"].(string)

	// GET one.
	var got map[string]any
	env.do("GET", "/v1/leads/"+id, env.tenantID, nil, http.StatusOK, &got)
	if got["id"] != id {
		t.Fatalf("get: %v", got)
	}

	// NEW -> QUALIFIED legal; backwards 422.
	var moved map[string]any
	env.do("POST", fmt.Sprintf("/v1/leads/%s/transition", id), env.tenantID,
		map[string]any{"to": "QUALIFIED"}, http.StatusOK, &moved)
	if moved["status"] != "QUALIFIED" {
		t.Fatalf("transition: %v", moved)
	}
	env.do("POST", fmt.Sprintf("/v1/leads/%s/transition", id), env.tenantID,
		map[string]any{"to": "NEW"}, http.StatusUnprocessableEntity, nil)
	// CONVERTED via transition is 422 — conversion mints records.
	env.do("POST", fmt.Sprintf("/v1/leads/%s/transition", id), env.tenantID,
		map[string]any{"to": "CONVERTED"}, http.StatusUnprocessableEntity, nil)

	// Convert mints the corporate account.
	var conv map[string]any
	env.do("POST", fmt.Sprintf("/v1/leads/%s/convert", id), env.tenantID,
		map[string]any{}, http.StatusOK, &conv)
	convLead := conv["lead"].(map[string]any)
	if convLead["status"] != "CONVERTED" || convLead["converted_account_id"] == "" {
		t.Fatalf("convert: %v", conv)
	}
	acct := conv["account"].(map[string]any)
	if acct["name"] != "Initech" {
		t.Fatalf("account: %v", acct)
	}
	// Terminal: further transitions conflict.
	env.do("POST", fmt.Sprintf("/v1/leads/%s/transition", id), env.tenantID,
		map[string]any{"to": "LOST", "lost_reason": "x"}, http.StatusConflict, nil)

	// LOST requires a reason over the wire too.
	res2 := captureLead36(t, env, map[string]any{"kind": "person", "name": "Lost", "phone": "+971501110031"})
	id2 := res2["lead"].(map[string]any)["id"].(string)
	env.do("POST", fmt.Sprintf("/v1/leads/%s/transition", id2), env.tenantID,
		map[string]any{"to": "LOST"}, http.StatusUnprocessableEntity, nil)
}

func TestLeadIdempotentReplay36(t *testing.T) {
	env := newEnv(t)
	body := map[string]any{
		"kind": "person", "name": "Replay", "phone": "+971501110040",
		"idempotency_key": "form-submit-abc",
	}
	captureLead36(t, env, body)
	// Replay: 200 quiet ack, no duplicate row.
	var replay map[string]any
	env.do("POST", "/v1/leads", env.tenantID, body, http.StatusOK, &replay)
	if replay["idempotent_replay"] != true {
		t.Fatalf("replay: %v", replay)
	}
	var list map[string]any
	env.do("GET", "/v1/leads", env.tenantID, nil, http.StatusOK, &list)
	if len(list["leads"].([]any)) != 1 {
		t.Fatalf("replay duplicated: %v", list)
	}
}

func TestLeadPublicCapture36(t *testing.T) {
	env := newEnv(t)
	// utm params win; referer fills the gaps; campaign window last.
	var res map[string]any
	env.do("POST", "/site/taylor/leads?utm_medium=social&utm_campaign=launch",
		"", map[string]any{
			"kind": "event", "name": "Gala Planner", "phone": "+971501110050",
			"event_type": "gala", "headcount": 120,
		}, http.StatusCreated, &res)
	lead := res["lead"].(map[string]any)
	if lead["source"] != "site" || lead["channel"] != "social" || lead["campaign"] != "launch" {
		t.Fatalf("attribution: %v", lead)
	}
	if lead["status"] != "NEW" {
		t.Fatalf("public capture status: %v", lead)
	}

	// Unknown slug: 404.
	env.do("POST", "/site/nope/leads", "", map[string]any{
		"kind": "person", "name": "X", "phone": "+1",
	}, http.StatusNotFound, nil)

	// Validation over the wire: event without an anchor is 422.
	env.do("POST", "/site/taylor/leads", "", map[string]any{
		"kind": "event", "name": "Empty Brief", "phone": "+971501110051",
	}, http.StatusUnprocessableEntity, nil)
}

func TestLeadPublicCaptureReferer36(t *testing.T) {
	env := newEnv(t)
	req, _ := http.NewRequest("POST", env.server.URL+"/site/taylor/leads",
		strings.NewReader(`{"kind":"person","name":"Ref","phone":"+971501110060"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", "https://www.google.com/search?q=vip+concierge")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(readAll(t, resp)), &res); err != nil {
		t.Fatal(err)
	}
	lead := res["lead"].(map[string]any)
	if lead["channel"] != "seo" || lead["campaign"] != "organic:google" {
		t.Fatalf("referer attribution: %v", lead)
	}
}

func TestLeadHoneypot36(t *testing.T) {
	env := newEnv(t)
	// Bots fill the invisible field: silently accepted, never stored.
	var res map[string]any
	env.do("POST", "/site/taylor/leads", "", map[string]any{
		"kind": "person", "name": "Bot", "phone": "+971501110070",
		"website": "http://spam.example",
	}, http.StatusCreated, &res)
	if res["ok"] != true {
		t.Fatalf("honeypot ack: %v", res)
	}
	if _, dup := res["lead"]; dup {
		t.Fatalf("honeypot stored a lead: %v", res)
	}
	var list map[string]any
	env.do("GET", "/v1/leads", env.tenantID, nil, http.StatusOK, &list)
	if len(list["leads"].([]any)) != 0 {
		t.Fatalf("honeypot lead persisted: %v", list)
	}
}
