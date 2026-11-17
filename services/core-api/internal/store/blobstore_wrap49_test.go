package store

// Phase 49: blob-media decorator over the memory store, against a fake
// blob-adapter (httptest). Proves: Put routes bytes to the adapter and
// strips them from the row; Get hydrates from the adapter; legacy rows
// still serve inline; Delete removes object then row; BackfillMedia
// migrates idempotently; ErrBlobNotConfigured on an undecorated store.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/blobstore"
	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// fakeBlobAdapter is an in-memory stand-in for the Rust blob-adapter
// implementing the same HTTP contract.
type fakeBlobAdapter struct {
	mu      sync.Mutex
	objects map[string][]byte // tenant/key → bytes
	deleted []string
	srv     *httptest.Server
}

func newFakeBlobAdapter(t *testing.T) *fakeBlobAdapter {
	t.Helper()
	f := &fakeBlobAdapter{objects: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /v1/blobs/{key...}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		b, _ := io.ReadAll(r.Body)
		f.objects[f.srvKey(r)] = b
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"key": r.PathValue("key")})
	})
	mux.HandleFunc("GET /v1/blobs/{key...}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		b, ok := f.objects[f.srvKey(r)]
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
		key := f.srvKey(r)
		delete(f.objects, key)
		f.deleted = append(f.deleted, key)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/engine", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"engine": "apache-opendal", "scheme": "memory"})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeBlobAdapter) srvKey(r *http.Request) string {
	return r.Header.Get("X-Tenant-ID") + "/" + r.PathValue("key")
}

func (f *fakeBlobAdapter) has(tenant, key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[tenant+"/"+key]
	return ok
}

func testMedia(tenantID, id string, data []byte) *model.Media {
	return &model.Media{
		ID: id, TenantID: tenantID, Kind: model.MediaKindService,
		ContentType: "image/png", Data: data, SizeBytes: len(data),
		CreatedAt: time.Now(),
	}
}

func TestBlobMediaWrap49(t *testing.T) {
	ctx := context.Background()
	fake := newFakeBlobAdapter(t)
	inner := NewMemory()
	st := WithBlobMedia(inner, blobstore.New(fake.srv.URL))

	// Put routes bytes to the adapter; the row carries key + engine, no bytes.
	if err := st.Media().Put(ctx, testMedia("t1", "m-1", []byte("png-bytes"))); err != nil {
		t.Fatal(err)
	}
	if !fake.has("t1", "media/m-1") {
		t.Fatal("adapter did not receive the object")
	}
	row, err := inner.Media().Get(ctx, "t1", "m-1")
	if err != nil {
		t.Fatal(err)
	}
	if row.StorageKey != "media/m-1" || row.StorageEngine != MediaEngineOpendal {
		t.Fatalf("row provenance: %+v", row)
	}
	if len(row.Data) != 0 {
		t.Fatal("row still carries inline bytes")
	}

	// Get hydrates from the adapter.
	md, err := st.Media().Get(ctx, "t1", "m-1")
	if err != nil {
		t.Fatal(err)
	}
	if string(md.Data) != "png-bytes" || md.SizeBytes != len("png-bytes") {
		t.Fatalf("hydrated media: %+v", md)
	}

	// Legacy inline row on the inner store still serves.
	if err := inner.Media().Put(ctx, testMedia("t1", "m-legacy", []byte("old-bytes"))); err != nil {
		t.Fatal(err)
	}
	md, err = st.Media().Get(ctx, "t1", "m-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if string(md.Data) != "old-bytes" {
		t.Fatalf("legacy read: %q", md.Data)
	}

	// Delete removes the object and the row.
	if err := st.Media().Delete(ctx, "t1", "m-1"); err != nil {
		t.Fatal(err)
	}
	if fake.has("t1", "media/m-1") {
		t.Fatal("object survived delete")
	}
	if _, err := st.Media().Get(ctx, "t1", "m-1"); err != httpx.ErrNotFound {
		t.Fatalf("deleted row: %v", err)
	}

	// Backfill migrates the legacy row; replay is a no-op.
	mig, skip, err := BackfillMedia(ctx, st, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if mig != 1 || skip != 0 {
		t.Fatalf("backfill: migrated=%d skipped=%d", mig, skip)
	}
	if !fake.has("t1", "media/m-legacy") {
		t.Fatal("backfill did not write the object")
	}
	row, err = inner.Media().Get(ctx, "t1", "m-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if row.StorageEngine != MediaEngineOpendal || len(row.Data) != 0 {
		t.Fatalf("backfilled row: %+v", row)
	}
	md, err = st.Media().Get(ctx, "t1", "m-legacy")
	if err != nil || string(md.Data) != "old-bytes" {
		t.Fatalf("post-backfill read: %v %q", err, md.Data)
	}
	mig, skip, err = BackfillMedia(ctx, st, "t1")
	if err != nil || mig != 0 || skip != 1 {
		t.Fatalf("backfill replay: migrated=%d skipped=%d err=%v", mig, skip, err)
	}

	// Undecorated store: no client, no backfill.
	if BlobClient(inner) != nil {
		t.Fatal("undecorated store exposes a blob client")
	}
	if _, _, err := BackfillMedia(ctx, inner, "t1"); err != ErrBlobNotConfigured {
		t.Fatalf("backfill undecorated: %v", err)
	}

	// Engine provenance endpoint.
	info, err := blobstore.New(fake.srv.URL).Engine(ctx)
	if err != nil || info.Engine != "apache-opendal" || info.Scheme != "memory" {
		t.Fatalf("engine: %+v %v", info, err)
	}
}
