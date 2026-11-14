package server_test

// Phase 31: media pipeline + premium template v2.
//
// Media: JSON base64 upload (type-sniffed, raster-only, size-capped),
// tenant-scoped storage, public serving under /site/{slug}/media/{id}
// (cross-tenant ids 404 through the slug), console list/delete.
//
// Template v2: glass nav, full-bleed hero from tenant media, stats strip,
// 16:9 image service cards (monogram fallback), how-it-works, the dark
// platform-features band with the inline booking widget, consented review
// quotes and the CTA band. Export bundles media files and rewrites URLs.

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/model"
)

// tinyPNG is a valid 1x1 PNG — small enough to inline, real enough to
// pass content sniffing.
const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func uploadPNG(t *testing.T, env *testEnv, tenantID, kind string) (string, string) {
	t.Helper()
	var out struct {
		Media model.Media `json:"media"`
		URL   string      `json:"url"`
	}
	env.do("POST", "/v1/media", tenantID, map[string]any{
		"kind": kind, "content_type": "image/png", "data_base64": tinyPNG,
	}, http.StatusCreated, &out)
	if out.Media.ID == "" || out.Media.SizeBytes == 0 {
		t.Fatalf("upload: %+v", out)
	}
	return out.Media.ID, out.URL
}

func TestMediaPipeline31(t *testing.T) {
	env := newEnv(t)

	id, url := uploadPNG(t, env, env.tenantID, "hero")
	if !strings.HasSuffix(url, "/site/taylor/media/"+id) {
		t.Fatalf("public url: %q", url)
	}

	// Public serving: bytes, content type, long immutable cache.
	req, _ := http.NewRequest("GET", env.server.URL+"/site/taylor/media/"+id, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("serve: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("cache: %q", resp.Header.Get("Cache-Control"))
	}
	if base64.StdEncoding.EncodeToString(body) != tinyPNG {
		t.Fatal("media bytes corrupted in round-trip")
	}

	// List: metadata only (no data field in the JSON).
	var list []map[string]any
	env.do("GET", "/v1/media", env.tenantID, nil, http.StatusOK, &list)
	if len(list) != 1 || list[0]["id"] != id {
		t.Fatalf("list: %+v", list)
	}
	if _, has := list[0]["data"]; has {
		t.Fatal("list leaks media bytes")
	}

	// Validation: SVG refused, garbage refused, wrong kind refused.
	env.do("POST", "/v1/media", env.tenantID, map[string]any{
		"content_type": "image/svg+xml", "data_base64": tinyPNG,
	}, http.StatusUnprocessableEntity, nil)
	env.do("POST", "/v1/media", env.tenantID, map[string]any{
		"content_type": "image/png", "data_base64": "!!!not-base64!!!",
	}, http.StatusUnprocessableEntity, nil)
	env.do("POST", "/v1/media", env.tenantID, map[string]any{
		"kind": "evil", "content_type": "image/png", "data_base64": tinyPNG,
	}, http.StatusUnprocessableEntity, nil)

	// Cross-tenant: a second tenant's slug must not serve this media.
	var t2 model.Tenant
	env.do("POST", "/v1/platform/tenants", "", model.Tenant{
		Slug: "other", Name: "Other Co", Archetype: "flagship",
	}, http.StatusCreated, &t2)
	resp, _ = http.Get(env.server.URL + "/site/other/media/" + id)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant media: %d", resp.StatusCode)
	}

	// Delete → 404.
	env.do("DELETE", "/v1/media/"+id, env.tenantID, nil, http.StatusNoContent, nil)
	resp, _ = http.Get(env.server.URL + "/site/taylor/media/" + id)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted media: %d", resp.StatusCode)
	}
}

func TestSiteTemplateV2Landing31(t *testing.T) {
	env := newEnv(t)
	heroID, _ := uploadPNG(t, env, env.tenantID, "hero")
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{
			"locales":       []string{"en"},
			"hero_image_id": heroID,
		},
		"brand": map[string]any{"whatsapp_number": "+351912345678"},
	}, http.StatusOK, nil)

	imgID, _ := uploadPNG(t, env, env.tenantID, "service")
	var svc model.CatalogService
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "PRIVATE_TOUR", "name": "Sintra Private Tour",
		"base_minor": 35000, "currency": "EUR", "image_id": imgID,
	}, http.StatusCreated, &svc)
	if svc.ImageID != imgID {
		t.Fatalf("image_id not stored on create: %+v", svc)
	}
	// A second, image-less service exercises the monogram fallback.
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "name": "Airport Transfer",
		"base_minor": 12000, "currency": "EUR",
	}, http.StatusCreated, nil)

	resp, _ := http.Get(env.server.URL + "/site/taylor/")
	body := readAll(t, resp)
	for _, want := range []string{
		`class="nav"`,                       // sticky glass nav
		`class="hero-bg"`,                   // full-bleed hero image
		"/media/" + heroID,                  // hero wired to tenant media
		`class="stats"`,                     // stats strip
		"How it works",                      // 3-step section
		"One platform behind every booking", // platform band
		"/embed/taylor",                     // inline booking widget iframe
		"/media/" + imgID,                   // service card image
		"Instant online booking",            // feature cards
		"Live trip tracking",
		"Vetted fleet &amp; chauffeurs",
		`class="cta-band"`,
		`class="card-top"`, // card grid present (image or monogram)
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("template v2 landing missing %q", want)
		}
	}

	// Service page: the card image doubles as the page hero.
	resp, _ = http.Get(env.server.URL + "/site/taylor/book/" + svc.ID)
	body = readAll(t, resp)
	if !strings.Contains(body, `class="hero-bg"`) || !strings.Contains(body, "/media/"+imgID) {
		t.Fatal("service page hero not wired to the card image")
	}
	if !strings.Contains(body, `og:image" content="`) {
		t.Fatal("og:image missing")
	}
}

func TestCatalogImageAndExport31(t *testing.T) {
	env := newEnv(t)

	// Bogus image ids are rejected at create and update.
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "PRIVATE_TOUR", "name": "Bad", "base_minor": 100, "currency": "EUR",
		"image_id": "00000000-0000-0000-0000-000000000000",
	}, http.StatusUnprocessableEntity, nil)

	imgID, _ := uploadPNG(t, env, env.tenantID, "service")
	var svc model.CatalogService
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "PRIVATE_TOUR", "name": "Sintra", "base_minor": 35000, "currency": "EUR",
	}, http.StatusCreated, &svc)
	env.do("PUT", "/v1/catalog/"+svc.ID, env.tenantID, map[string]any{
		"image_id": "00000000-0000-0000-0000-000000000000",
	}, http.StatusUnprocessableEntity, nil)
	var updated model.CatalogService
	env.do("PUT", "/v1/catalog/"+svc.ID, env.tenantID, map[string]any{
		"image_id": imgID,
	}, http.StatusOK, &updated)
	if updated.ImageID != imgID {
		t.Fatalf("update did not set image: %+v", updated)
	}
	// Catalog list surfaces the image id (the site's card lookup path).
	var catalog []model.CatalogService
	env.do("GET", "/v1/catalog", env.tenantID, nil, http.StatusOK, &catalog)
	if len(catalog) != 1 || catalog[0].ImageID != imgID {
		t.Fatalf("catalog list: %+v", catalog)
	}

	// Export bundles the media file and rewrites the URL to the flat layout.
	req, _ := http.NewRequest("GET", env.server.URL+"/v1/studio/export", nil)
	req.Header.Set("X-Tenant-ID", env.tenantID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	zipBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: %d", resp.StatusCode)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		names[f.Name] = string(b)
	}
	mediaName := "media-" + imgID + ".png"
	if _, ok := names[mediaName]; !ok {
		t.Fatalf("export missing bundled media %q; has %v", mediaName, keysOf31(names))
	}
	idx := names["index.html"]
	if !strings.Contains(idx, mediaName) {
		t.Fatal("export index does not reference bundled media")
	}
	if strings.Contains(idx, "/media/"+imgID) {
		t.Fatal("export still references the live media route")
	}
}

func keysOf31(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
