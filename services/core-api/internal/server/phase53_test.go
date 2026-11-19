package server_test

// Phase 53 server tests: the visual campaign builder API over HTTP —
// upsert validation, name-keyed replace, list with derived progress,
// delete, and the on-demand sweep.

import (
	"net/http"
	"testing"

	"concierge-engine/services/core-api/internal/domain"
)

type seqProgressRow struct {
	Sequence struct {
		Name  string `json:"name"`
		Steps []struct {
			Body string `json:"body"`
		} `json:"steps"`
	} `json:"sequence"`
	Enrolled int `json:"enrolled"`
	Steps    []struct {
		Index   int `json:"index"`
		Sent    int `json:"sent"`
		Failed  int `json:"failed"`
		Waiting int `json:"waiting"`
	} `json:"steps"`
}

func TestSequenceBuilderAPI53(t *testing.T) {
	env := newEnvDeps(t, domain.Deps{})

	// Validation: an email step without a subject never persists.
	env.do("PUT", "/v1/outreach/sequences/bad", env.tenantID, map[string]any{
		"steps": []map[string]any{{"after_hours": 0, "body": "x", "channel": "email"}},
	}, http.StatusUnprocessableEntity, nil)
	var list struct {
		Sequences []seqProgressRow `json:"sequences"`
	}
	env.do("GET", "/v1/outreach/sequences", env.tenantID, nil, http.StatusOK, &list)
	if len(list.Sequences) != 0 {
		t.Fatalf("invalid upsert must not persist: %+v", list.Sequences)
	}

	// Valid upsert.
	env.do("PUT", "/v1/outreach/sequences/welcome", env.tenantID, map[string]any{
		"steps": []map[string]any{
			{"after_hours": 0, "body": "hi {{client_name}}", "label": "intro"},
			{"after_hours": 24, "body": "follow up", "require_engagement": true, "engagement_timeout_hours": 48},
		},
	}, http.StatusOK, nil)

	// Name-keyed replace: still exactly one sequence.
	env.do("PUT", "/v1/outreach/sequences/welcome", env.tenantID, map[string]any{
		"steps": []map[string]any{{"after_hours": 0, "body": "hi again"}},
	}, http.StatusOK, nil)
	list.Sequences = nil
	env.do("GET", "/v1/outreach/sequences", env.tenantID, nil, http.StatusOK, &list)
	if len(list.Sequences) != 1 || len(list.Sequences[0].Sequence.Steps) != 1 {
		t.Fatalf("upsert must replace by name: %+v", list.Sequences)
	}
	if list.Sequences[0].Enrolled != 0 {
		t.Fatalf("no clients yet — enrolled must be 0: %+v", list.Sequences[0])
	}

	// A client enrolls; the on-demand sweep fires step 0 and the list
	// reflects the derived counters (sent or failed without an adapter).
	var rr domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID, map[string]any{
		"phone": "+971500000401", "name": "Builder Client",
	}, http.StatusOK, &rr)
	env.do("PUT", "/v1/clients/"+rr.Client.ID+"/consent", env.tenantID, map[string]any{
		"scope": "marketing", "granted": true,
	}, http.StatusOK, nil)
	var run domain.SequenceRunResult
	env.do("POST", "/v1/outreach/sequences/run", env.tenantID, nil, http.StatusOK, &run)
	if run.Sent+run.Failed != 1 {
		t.Fatalf("step 0 fires on the run-now sweep: %+v", run)
	}
	list.Sequences = nil
	env.do("GET", "/v1/outreach/sequences", env.tenantID, nil, http.StatusOK, &list)
	if list.Sequences[0].Enrolled != 1 {
		t.Fatalf("enrolled=1: %+v", list.Sequences[0])
	}
	st := list.Sequences[0].Steps[0]
	if st.Sent+st.Failed != 1 {
		t.Fatalf("step 0 counter reflects the ledger: %+v", st)
	}

	// Delete: 204, then 404 on replay.
	env.do("DELETE", "/v1/outreach/sequences/welcome", env.tenantID, nil, http.StatusNoContent, nil)
	env.do("DELETE", "/v1/outreach/sequences/welcome", env.tenantID, nil, http.StatusNotFound, nil)
}
