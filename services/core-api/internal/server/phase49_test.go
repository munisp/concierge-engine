package server_test

// Phase 49: Apache OpenDAL media storage end-to-end. With the store
// decorated by WithBlobMedia, uploads land in the blob-adapter, public and
// console reads hydrate from it, deletes remove the object, the engine
// endpoint reports honest provenance, and backfill migrates legacy inline
// rows idempotently.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"concierge-engine/services/core-api/internal/blobstore"
	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
	"concierge-engine/services/core-api/internal/server"
	"concierge-engine/services/core-api/internal/store"
)

type fakeBlobs49 struct {
	mu      sync.Mutex
	objects map[string][]byte
	srv     *httptest.Server
}

func newFakeBlobs49(t *testing.T) *fakeBlobs49 {
	t.Helper()
	f := &fakeBlobs49{objects: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /v1/blobs/{key...}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		b, _ := io.ReadAll(r.Body)
		f.objects[r.Header.Get("X-Tenant-ID")+"/"+r.PathValue("key")] = b
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("GET /v1/blobs/{key...}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		b, ok := f.objects[r.Header.Get("X-Tenant-ID")+"/"+r.PathValue("key")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("DELETE /v1/blobs/{key...}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.objects, r.Header.Get("X-Tenant-ID")+"/"+r.PathValue("key"))
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/engine", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"engine": "apache-opendal", "scheme": "memory"})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeBlobs49) has(tenant, key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[tenant+"/"+key]
	return ok
}

// newEnv49 builds an env on a blob-decorated memory store; the returned
// inner store lets the test seed legacy inline rows.
func newEnv49(t *testing.T) (*testEnv, store.Store, *fakeBlobs49) {
	t.Helper()
	inner := store.NewMemory()
	fake := newFakeBlobs49(t)
	st := store.WithBlobMedia(inner, blobstore.New(fake.srv.URL))
	svc := domain.NewServices(st, domain.Deps{
		PublicBase:    "http://core",
		StorageEngine: "opendal",
		TripSecret:    "test-secret",
	})
	srv := httptest.NewServer(server.New(svc))
	t.Cleanup(srv.Close)
	env := &testEnv{t: t, server: srv, svc: svc}
	var tenant model.Tenant
	env.do("POST", "/v1/platform/tenants", "", model.Tenant{
		Slug: "taylor", Name: "Taylor Travel Management", Archetype: "flagship",
		PricingRules: model.PricingRules{Currency: "EUR", DepositPct: 0.3, QuoteTTLSeconds: 3600, PlatformFeePct: 0.03},
	}, http.StatusCreated, &tenant)
	env.tenantID = tenant.ID
	return env, inner, fake
}

func TestMediaOpenDAL49(t *testing.T) {
	env, _, fake := newEnv49(t)

	// Upload routes bytes to the adapter; row reports opendal provenance.
	id, _ := uploadPNG(t, env, env.tenantID, "hero")
	if !fake.has(env.tenantID, "media/"+id) {
		t.Fatal("adapter did not receive the upload")
	}
	var list []model.Media
	env.do("GET", "/v1/media", env.tenantID, nil, http.StatusOK, &list)
	if len(list) != 1 || list[0].StorageEngine != "opendal" || list[0].StorageKey != "media/"+id {
		t.Fatalf("media list: %+v", list)
	}

	// Console raw read hydrates from the adapter.
	req, _ := http.NewRequest("GET", env.server.URL+"/v1/media/"+id+"/raw", nil)
	req.Header.Set("X-Tenant-ID", env.tenantID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(body) == 0 {
		t.Fatalf("raw read: %d (%d bytes)", resp.StatusCode, len(body))
	}

	// Public site path serves the same bytes.
	req, _ = http.NewRequest("GET", env.server.URL+"/site/taylor/media/"+id, nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("public serving: %d", resp.StatusCode)
	}

	// Engine endpoint: opendal, reachable, scheme from the adapter.
	var eng map[string]any
	env.do("GET", "/v1/storage/engine", env.tenantID, nil, http.StatusOK, &eng)
	if eng["engine"] != "opendal" || eng["reachable"] != true || eng["detail"] != "apache-opendal/memory" {
		t.Fatalf("engine: %v", eng)
	}

	// Delete removes the object and the row.
	env.do("DELETE", "/v1/media/"+id, env.tenantID, nil, http.StatusNoContent, nil)
	if fake.has(env.tenantID, "media/"+id) {
		t.Fatal("object survived delete")
	}
	env.do("GET", "/v1/media/"+id+"/raw", env.tenantID, nil, http.StatusNotFound, nil)
}

func TestMediaBackfillEndpoint49(t *testing.T) {
	env, inner, fake := newEnv49(t)

	// Two legacy inline rows seeded directly on the inner store.
	png, err := base64.StdEncoding.DecodeString(tinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"legacy-1", "legacy-2"} {
		if err := inner.Media().Put(context.Background(), &model.Media{
			ID: id, TenantID: env.tenantID, Kind: model.MediaKindMisc,
			ContentType: "image/png", Data: png, SizeBytes: len(png),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// One new upload → object storage.
	newID, _ := uploadPNG(t, env, env.tenantID, "misc")

	var rec map[string]int
	env.do("POST", "/v1/media/backfill", env.tenantID, nil, http.StatusOK, &rec)
	if rec["migrated"] != 2 || rec["skipped"] != 1 {
		t.Fatalf("backfill: %v", rec)
	}
	for _, id := range []string{"legacy-1", "legacy-2", newID} {
		if !fake.has(env.tenantID, "media/"+id) {
			t.Fatalf("missing object for %s", id)
		}
	}
	// Legacy rows now serve through the adapter and report provenance.
	var list []model.Media
	env.do("GET", "/v1/media", env.tenantID, nil, http.StatusOK, &list)
	for _, md := range list {
		if md.StorageEngine != "opendal" {
			t.Fatalf("row %s not migrated: %+v", md.ID, md)
		}
	}
	// Replay: everything skipped.
	env.do("POST", "/v1/media/backfill", env.tenantID, nil, http.StatusOK, &rec)
	if rec["migrated"] != 0 || rec["skipped"] != 3 {
		t.Fatalf("backfill replay: %v", rec)
	}
}

func TestStorageEngineLegacy49(t *testing.T) {
	env := newEnv(t) // undecorated memory store
	var eng map[string]any
	env.do("GET", "/v1/storage/engine", env.tenantID, nil, http.StatusOK, &eng)
	if eng["engine"] != "legacy-inline" || eng["legacy"] != true || eng["detail"] != "memory" {
		t.Fatalf("engine: %v", eng)
	}
	// Backfill with no object storage configured → 422.
	env.do("POST", "/v1/media/backfill", env.tenantID, nil, http.StatusUnprocessableEntity, nil)
}
