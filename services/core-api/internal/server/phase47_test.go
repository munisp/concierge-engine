package server_test

// Phase 47 server tests: the KYC endpoints are tenant-scoped, the case
// workflow runs open → submit → decide end to end, the kyc_config patch
// round-trips through PUT /v1/tenant, and cross-tenant case access is a
// 404, not an existence leak.

import (
	"net/http"
	"testing"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
)

func TestKycEndpoints47(t *testing.T) {
	env := newEnv(t)

	// Client via resolve.
	var rr domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID, map[string]any{
		"phone": "+15550007777", "name": "Gate Buyer",
	}, http.StatusOK, &rr)
	cl := *rr.Client
	if cl.KYCStatus != "none" {
		t.Fatalf("fresh client kyc_status = %q, want none", cl.KYCStatus)
	}

	// Open a STANDARD case.
	var kc model.KycCase
	env.do("POST", "/v1/kyc/cases", env.tenantID, map[string]any{
		"client_id": cl.ID, "level": "STANDARD", "idempotency_key": "srv-kyc-1",
	}, http.StatusCreated, &kc)
	if kc.Status != model.KycStatusPending || kc.Provider != "manual" {
		t.Fatalf("opened case: %+v", kc)
	}

	// Idempotent replay returns the original case.
	var replay model.KycCase
	env.do("POST", "/v1/kyc/cases", env.tenantID, map[string]any{
		"client_id": cl.ID, "level": "STANDARD", "idempotency_key": "srv-kyc-1",
	}, http.StatusCreated, &replay)
	if replay.ID != kc.ID {
		t.Fatalf("replay id = %s, want %s", replay.ID, kc.ID)
	}

	// Unknown level is a 422.
	env.do("POST", "/v1/kyc/cases", env.tenantID, map[string]any{
		"client_id": cl.ID, "level": "PLATINUM", "idempotency_key": "srv-kyc-bad",
	}, http.StatusUnprocessableEntity, nil)

	// Submit + decide.
	var submitted model.KycCase
	env.do("POST", "/v1/kyc/cases/"+kc.ID+"/submit", env.tenantID, map[string]any{
		"document_type": "passport", "document_ref": "vault://taylor/p-77",
	}, http.StatusOK, &submitted)
	if submitted.Status != model.KycStatusInReview {
		t.Fatalf("submitted case status = %s", submitted.Status)
	}
	var decided model.KycCase
	env.do("POST", "/v1/kyc/cases/"+kc.ID+"/decide", env.tenantID, map[string]any{
		"approve": true, "decided_by": "ops-lead",
	}, http.StatusOK, &decided)
	if decided.Status != model.KycStatusApproved || decided.DecidedBy != "ops-lead" {
		t.Fatalf("decided case: %+v", decided)
	}

	// Client status advanced.
	var after domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID, map[string]any{
		"phone": "+15550007777", "name": "Gate Buyer",
	}, http.StatusOK, &after)
	if after.Client.KYCStatus != "standard" {
		t.Fatalf("client kyc_status = %q, want standard", after.Client.KYCStatus)
	}

	// Queue + per-client listing + get.
	var queue []model.KycCase
	env.do("GET", "/v1/kyc/cases?status=APPROVED", env.tenantID, nil, http.StatusOK, &queue)
	if len(queue) != 1 || queue[0].ID != kc.ID {
		t.Fatalf("queue: %+v", queue)
	}
	env.do("GET", "/v1/kyc/cases?status=BOGUS", env.tenantID, nil, http.StatusUnprocessableEntity, nil)
	var forClient []model.KycCase
	env.do("GET", "/v1/clients/"+cl.ID+"/kyc", env.tenantID, nil, http.StatusOK, &forClient)
	if len(forClient) != 1 {
		t.Fatalf("client cases: %+v", forClient)
	}
	var one model.KycCase
	env.do("GET", "/v1/kyc/cases/"+kc.ID, env.tenantID, nil, http.StatusOK, &one)
	if one.ID != kc.ID {
		t.Fatalf("get: %+v", one)
	}

	// Cross-tenant: no existence leak.
	var other model.Tenant
	env.do("POST", "/v1/platform/tenants", "", model.Tenant{
		Slug: "kyc-other", Name: "KYC Other", Archetype: "smb",
	}, http.StatusCreated, &other)
	env.do("GET", "/v1/kyc/cases/"+kc.ID, other.ID, nil, http.StatusNotFound, nil)
	var otherQueue []model.KycCase
	env.do("GET", "/v1/kyc/cases", other.ID, nil, http.StatusOK, &otherQueue)
	if len(otherQueue) != 0 {
		t.Fatalf("cross-tenant queue leak: %+v", otherQueue)
	}

	// kyc_config patch round-trips through PUT /v1/tenant.
	var patched model.Tenant
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"kyc_config": map[string]any{"standard_above_minor": 500000, "case_expiry_days": 14},
	}, http.StatusOK, &patched)
	if patched.KycConfig.StandardAboveMinor != 500000 || patched.KycConfig.CaseExpiryDays != 14 {
		t.Fatalf("patched kyc_config: %+v", patched.KycConfig)
	}
	// Selective patch: the retention config set by earlier phases' posture
	// must not be wiped by a kyc_config write.
	var fetched model.Tenant
	env.do("GET", "/v1/tenant", env.tenantID, nil, http.StatusOK, &fetched)
	if fetched.KycConfig.StandardAboveMinor != 500000 {
		t.Fatalf("fetched kyc_config: %+v", fetched.KycConfig)
	}

	// Expiry sweep endpoint.
	var expireRes struct {
		Expired int `json:"expired"`
	}
	env.do("POST", "/v1/kyc/expire", env.tenantID, nil, http.StatusOK, &expireRes)
	if expireRes.Expired != 0 {
		t.Fatalf("fresh tenant expiry = %d, want 0", expireRes.Expired)
	}

	// No tenant header: 403.
	env.do("GET", "/v1/kyc/cases", "", nil, http.StatusForbidden, nil)
}
