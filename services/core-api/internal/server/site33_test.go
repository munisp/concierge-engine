package server_test

// Phase 33 — imagery honesty across the onboarding/catalog/media triangle:
//   - service_imagery onboarding step: derived per-service card-image
//     coverage (every ACTIVE service carries a card image), grouped under
//     "presence", actionable from the Catalog page;
//   - media delete scrubs every reference first (catalog image_id — active
//     AND inactive services — plus site_config hero and gallery), so the
//     public site never serves a dangling media id after a delete;
//   - render-time defense: legacy rows with dangling ids (written before
//     the scrub existed, or via paths that never validated media) resolve
//     to nothing instead of a broken <img>.

import (
	"net/http"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/model"
)

// TestServiceImageryStep33: the derived step tracks per-service card-image
// coverage and participates in skip/unskip like every other step.
func TestServiceImageryStep33(t *testing.T) {
	env := newEnv(t)

	type step struct {
		ID     string `json:"id"`
		Group  string `json:"group"`
		Status string `json:"status"`
		Action string `json:"action"`
		Detail string `json:"detail"`
	}
	get := func() map[string]step {
		var rep struct {
			Steps []step `json:"steps"`
		}
		env.do("GET", "/v1/onboarding", env.tenantID, nil, http.StatusOK, &rep)
		out := map[string]step{}
		for _, s := range rep.Steps {
			out[s.ID] = s
		}
		return out
	}

	// Fresh tenant: no services at all — the step pends with the
	// "add services first" guidance, not a bogus 0-of-0.
	s := get()["service_imagery"]
	if s.Status != "pending" {
		t.Fatalf("service_imagery should pend on an empty catalog, got %q", s.Status)
	}
	if s.Group != "presence" || s.Action != "/catalog" {
		t.Fatalf("step should live under presence with a /catalog action: %+v", s)
	}
	if !strings.Contains(s.Detail, "Add services first") {
		t.Fatalf("empty-catalog detail should guide to services first, got %q", s.Detail)
	}

	// Two services, neither with a card image: still pending (0 of 2).
	var svc1, svc2 model.CatalogService
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "PRIVATE_TOUR", "name": "Sintra Private Tour",
		"base_minor": 35000, "currency": "EUR",
	}, http.StatusCreated, &svc1)
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "name": "Airport Transfer",
		"base_minor": 12000, "currency": "EUR",
	}, http.StatusCreated, &svc2)
	s = get()["service_imagery"]
	if s.Status != "pending" || !strings.Contains(s.Detail, "0 of 2") {
		t.Fatalf("0 of 2 should pend, got %q (%q)", s.Status, s.Detail)
	}

	// Skip/unskip round-trips on the new id (skip only bites while the
	// step is pending — a derived "done" always wins).
	env.do("PUT", "/v1/onboarding", env.tenantID,
		map[string]any{"skip": []string{"service_imagery"}}, http.StatusOK, nil)
	if s = get()["service_imagery"]; s.Status != "skipped" {
		t.Fatalf("service_imagery should be skipped, got %q", s.Status)
	}
	env.do("PUT", "/v1/onboarding", env.tenantID,
		map[string]any{"unskip": []string{"service_imagery"}}, http.StatusOK, nil)
	if s = get()["service_imagery"]; s.Status != "pending" {
		t.Fatalf("unskip should restore the pending status, got %q", s.Status)
	}

	// Image on one of two: still pending with honest coverage.
	imgID, _ := uploadPNG(t, env, env.tenantID, "service")
	env.do("PUT", "/v1/catalog/"+svc1.ID, env.tenantID,
		map[string]any{"image_id": imgID}, http.StatusOK, nil)
	s = get()["service_imagery"]
	if s.Status != "pending" || !strings.Contains(s.Detail, "1 of 2") {
		t.Fatalf("1 of 2 should pend, got %q (%q)", s.Status, s.Detail)
	}

	// Full coverage: done.
	env.do("PUT", "/v1/catalog/"+svc2.ID, env.tenantID,
		map[string]any{"image_id": imgID}, http.StatusOK, nil)
	if s = get()["service_imagery"]; s.Status != "done" {
		t.Fatalf("full coverage should be done, got %q (%q)", s.Status, s.Detail)
	}

	// An INACTIVE service does not block the step. (Create forces Active —
	// deactivation is a deliberate follow-up operation.)
	var svc3 model.CatalogService
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "PRIVATE_TOUR", "name": "Seasonal Tour",
		"base_minor": 50000, "currency": "EUR",
	}, http.StatusCreated, &svc3)
	env.do("PUT", "/v1/catalog/"+svc3.ID, env.tenantID,
		map[string]any{"active": false}, http.StatusOK, nil)
	if s = get()["service_imagery"]; s.Status != "done" {
		t.Fatalf("inactive services must not block the step, got %q", s.Status)
	}
}

// TestMediaDeleteScrub33: deleting media removes every reference first —
// catalog card images (active and inactive alike) and the site_config
// hero/gallery — so nothing dangles afterwards.
func TestMediaDeleteScrub33(t *testing.T) {
	env := newEnv(t)

	imgID, _ := uploadPNG(t, env, env.tenantID, "service")

	var active, inactive model.CatalogService
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "PRIVATE_TOUR", "name": "Sintra Private Tour",
		"base_minor": 35000, "currency": "EUR", "image_id": imgID,
	}, http.StatusCreated, &active)
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "PRIVATE_TOUR", "name": "Retired Tour",
		"base_minor": 25000, "currency": "EUR", "image_id": imgID,
	}, http.StatusCreated, &inactive)
	// Create forces Active; retire it so the scrub must reach past List.
	env.do("PUT", "/v1/catalog/"+inactive.ID, env.tenantID,
		map[string]any{"active": false}, http.StatusOK, nil)
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{
			"hero_image_id": imgID,
			"gallery_ids":   []string{imgID},
		},
	}, http.StatusOK, nil)

	env.do("DELETE", "/v1/media/"+imgID, env.tenantID, nil, http.StatusNoContent, nil)

	// Catalog references cleared on the ACTIVE service (the public list).
	var catalog []model.CatalogService
	env.do("GET", "/v1/catalog", env.tenantID, nil, http.StatusOK, &catalog)
	if len(catalog) != 1 || catalog[0].ID != active.ID || catalog[0].ImageID != "" {
		t.Fatalf("active service should keep its row but lose the image: %+v", catalog)
	}
	// …and on the inactive one too (scrub iterates ListAll, not List).
	var fixed model.CatalogService
	env.do("PUT", "/v1/catalog/"+inactive.ID, env.tenantID,
		map[string]any{"active": true}, http.StatusOK, &fixed)
	if fixed.ImageID != "" {
		t.Fatalf("inactive service should have been scrubbed too: %+v", fixed)
	}

	// Site config references cleared.
	var tenant model.Tenant
	env.do("GET", "/v1/tenant", env.tenantID, nil, http.StatusOK, &tenant)
	if tenant.SiteConfig.HeroImageID != "" {
		t.Fatalf("hero should be cleared: %+v", tenant.SiteConfig)
	}
	if len(tenant.SiteConfig.GalleryIDs) != 0 {
		t.Fatalf("gallery should be empty: %+v", tenant.SiteConfig.GalleryIDs)
	}

	// The public site no longer references the deleted id anywhere.
	resp, _ := http.Get(env.server.URL + "/site/taylor/")
	if strings.Contains(readAll(t, resp), imgID) {
		t.Fatal("landing still references the deleted media id")
	}

	// Deleting a now-unknown id is a clean 404.
	env.do("DELETE", "/v1/media/"+imgID, env.tenantID, nil, http.StatusNotFound, nil)
}

// TestDanglingMediaRender33: legacy rows whose site_config points at media
// that no longer exists (written before the delete-scrub, or via paths that
// never validated) render nothing — never a broken <img>.
func TestDanglingMediaRender33(t *testing.T) {
	env := newEnv(t)

	// updateTenant validates shape, not media existence — the same path a
	// legacy row would have taken.
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{
			"hero_image_id": "dangling-hero",
			"gallery_ids":   []string{"dangling-gal"},
		},
	}, http.StatusOK, nil)

	resp, _ := http.Get(env.server.URL + "/site/taylor/")
	body := readAll(t, resp)
	for _, id := range []string{"dangling-hero", "dangling-gal"} {
		if strings.Contains(body, id) {
			t.Fatalf("landing must not reference dangling media id %q", id)
		}
	}

	// A live id mixed with a dangling one: live renders, dangling drops.
	imgID, _ := uploadPNG(t, env, env.tenantID, "misc")
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{"gallery_ids": []string{imgID, "dangling-gal"}},
	}, http.StatusOK, nil)
	resp, _ = http.Get(env.server.URL + "/site/taylor/")
	body = readAll(t, resp)
	if !strings.Contains(body, imgID) || strings.Contains(body, "dangling-gal") {
		t.Fatal("gallery should render the live id and drop the dangling one")
	}
}
