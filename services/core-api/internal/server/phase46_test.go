package server_test

// Phase 46 server tests: the retention run endpoint is tenant-scoped and
// idempotent, the retention_config patch round-trips through PUT
// /v1/tenant, and one tenant's sweep never touches another's data.

import (
	"net/http"
	"testing"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
)

func TestRetentionEndpoints46(t *testing.T) {
	env := newEnv(t)
	ctx := t.Context()

	// Seed sweepable data straight through the domain: an 800-day-old geo
	// stamp and attribution event for the seeded tenant.
	old := env.svc.Now().AddDate(0, 0, -800)
	if err := env.svc.Store.Dispatch().AddGeoStamp(ctx, &model.GeoStamp{
		ID: domain.NewID(), TenantID: env.tenantID, AssignmentID: domain.NewID(),
		Kind: "PING", Lat: 52.3, Lng: 4.8, At: old}); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.Store.Attribution().Track(ctx, &model.AttributionEvent{
		TenantID: env.tenantID, Kind: "page_view", Channel: "seo", At: old}); err != nil {
		t.Fatal(err)
	}

	// A second tenant with its own old stamp — isolation check.
	var other model.Tenant
	env.do("POST", "/v1/platform/tenants", "", model.Tenant{
		Slug: "retention-other", Name: "Retention Other", Archetype: "smb",
	}, http.StatusCreated, &other)
	if err := env.svc.Store.Dispatch().AddGeoStamp(ctx, &model.GeoStamp{
		ID: domain.NewID(), TenantID: other.ID, AssignmentID: domain.NewID(),
		Kind: "PING", Lat: 1.0, Lng: 2.0, At: old}); err != nil {
		t.Fatal(err)
	}

	// Run the sweep for the seeded tenant.
	var res struct {
		GeoStampsPurged       int  `json:"geo_stamps_purged"`
		MessageBodiesScrubbed int  `json:"message_bodies_scrubbed"`
		AttributionPurged     int  `json:"attribution_purged"`
		Disabled              bool `json:"disabled"`
	}
	env.do("POST", "/v1/retention/run", env.tenantID, nil, http.StatusOK, &res)
	if res.GeoStampsPurged != 1 || res.AttributionPurged != 1 || res.Disabled {
		t.Fatalf("run: %+v", res)
	}

	// Idempotent re-run.
	env.do("POST", "/v1/retention/run", env.tenantID, nil, http.StatusOK, &res)
	if res.GeoStampsPurged+res.MessageBodiesScrubbed+res.AttributionPurged != 0 {
		t.Fatalf("re-run not idempotent: %+v", res)
	}

	// The other tenant's stamp survived our sweep (and vice versa its own
	// sweep removes exactly its own row).
	env.do("POST", "/v1/retention/run", other.ID, nil, http.StatusOK, &res)
	if res.GeoStampsPurged != 1 {
		t.Fatalf("other tenant sweep: %+v", res)
	}

	// retention_config patches through PUT /v1/tenant and reads back.
	var tn model.Tenant
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"retention_config": map[string]any{"geo_stamps_days": 30, "attribution_days": -1},
	}, http.StatusOK, &tn)
	if tn.RetentionConfig.GeoStampsDays != 30 || tn.RetentionConfig.AttributionDays != -1 {
		t.Fatalf("patch round-trip: %+v", tn.RetentionConfig)
	}
	env.do("GET", "/v1/tenant", env.tenantID, nil, http.StatusOK, &tn)
	if tn.RetentionConfig.GeoStampsDays != 30 {
		t.Fatalf("persisted config: %+v", tn.RetentionConfig)
	}

	// Disabled tenant reports a no-op sweep.
	env.do("PUT", "/v1/tenant", other.ID, map[string]any{
		"retention_config": map[string]any{"disabled": true},
	}, http.StatusOK, &tn)
	env.do("POST", "/v1/retention/run", other.ID, nil, http.StatusOK, &res)
	if !res.Disabled || res.GeoStampsPurged+res.MessageBodiesScrubbed+res.AttributionPurged != 0 {
		t.Fatalf("disabled run: %+v", res)
	}

	// Unauthenticated (no tenant header) is refused.
	env.do("POST", "/v1/retention/run", "", nil, http.StatusForbidden, nil)
}
