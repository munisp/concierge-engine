package server_test

// Phase 37: bulk lead import endpoint — greenfield bootstrap receipt,
// per-row advisory rejects, batch cap enforcement.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestLeadImportEndpoint37(t *testing.T) {
	env := newEnv(t)
	var res map[string]any
	env.do("POST", "/v1/leads/import", env.tenantID, map[string]any{
		"leads": []map[string]any{
			{"kind": "person", "name": "Ada", "phone": "+971 50 111 0001", "consent_marketing": true, "channel": "crm"},
			{"kind": "corporate", "name": "Rui", "company": "Limo Co", "email": "rui@limo.example"},
			{"kind": "person", "name": "Ada Dup", "phone": "+971501110001"},
			{"kind": "person", "name": "No Contact"},
		},
	}, http.StatusOK, &res)
	if res["total"].(float64) != 4 || res["created"].(float64) != 2 ||
		res["deduped"].(float64) != 1 || res["rejected"].(float64) != 1 {
		t.Fatalf("receipt: %v", res)
	}
	errs := res["errors"].([]any)
	if len(errs) != 1 || errs[0].(map[string]any)["index"].(float64) != 3 {
		t.Fatalf("row errors: %v", errs)
	}
	if !strings.Contains(errs[0].(map[string]any)["reason"].(string), "phone") {
		t.Fatalf("reason should explain the missing contact: %v", errs[0])
	}

	// Leads are live on the board with import provenance: 2 created rows,
	// the dup merged into Ada (dedup), the anchorless row rejected.
	var list map[string]any
	env.do("GET", "/v1/leads?limit=10", env.tenantID, nil, http.StatusOK, &list)
	leads := list["leads"].([]any)
	if len(leads) != 2 {
		t.Fatalf("board: %d leads", len(leads))
	}
	for _, l := range leads {
		if l.(map[string]any)["source"] != "import" {
			t.Fatalf("source: %v", l)
		}
	}
}

func TestLeadImportLimits37(t *testing.T) {
	env := newEnv(t)
	// Empty batch → 422.
	env.do("POST", "/v1/leads/import", env.tenantID, map[string]any{"leads": []any{}},
		http.StatusUnprocessableEntity, nil)
	// Over the 500-row cap → 422.
	rows := make([]map[string]any, 501)
	for i := range rows {
		rows[i] = map[string]any{"kind": "person", "name": "N", "phone": fmt.Sprintf("+9715088%05d", i)}
	}
	env.do("POST", "/v1/leads/import", env.tenantID, map[string]any{"leads": rows},
		http.StatusUnprocessableEntity, nil)
}
