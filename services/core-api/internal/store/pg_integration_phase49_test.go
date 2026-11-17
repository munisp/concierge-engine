//go:build pg

package store

// Postgres integration tests — Phase 49: tenant_media object-storage
// columns (storage_key, storage_engine, size_bytes from migration 0035),
// legacy inline rows with size backfilled, MarkExternal flipping rows to
// external storage idempotently, and RLS isolation. Gate: CE_PG_TEST_DSN.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPGMediaExternal49(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-media49")
	other := pgTenant(t, s, "pg-media49-other")

	// Legacy inline row: bytes in bytea, no storage key.
	legacy := &model.Media{
		ID: newTestUUID(), TenantID: tenant, Kind: model.MediaKindHero,
		ContentType: "image/png", Data: []byte("legacy-bytes"), SizeBytes: len("legacy-bytes"),
		CreatedAt: time.Now().UTC(),
	}
	if err := s.Media().Put(ctx, legacy); err != nil {
		t.Fatalf("put legacy: %v", err)
	}
	got, err := s.Media().Get(ctx, tenant, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != "legacy-bytes" || got.StorageKey != "" || got.StorageEngine != "" {
		t.Fatalf("legacy round-trip: %+v", got)
	}
	if got.SizeBytes != len("legacy-bytes") {
		t.Fatalf("legacy size: %d", got.SizeBytes)
	}

	// Object-backed row: empty bytea, size carried by the column.
	ext := &model.Media{
		ID: newTestUUID(), TenantID: tenant, Kind: model.MediaKindService,
		ContentType: "image/webp", StorageKey: "media/x", StorageEngine: MediaEngineOpendal,
		SizeBytes: 4242, CreatedAt: time.Now().UTC(),
	}
	if err := s.Media().Put(ctx, ext); err != nil {
		t.Fatalf("put external: %v", err)
	}
	got, err = s.Media().Get(ctx, tenant, ext.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.StorageKey != "media/x" || got.StorageEngine != MediaEngineOpendal || got.SizeBytes != 4242 {
		t.Fatalf("external round-trip: %+v", got)
	}
	if len(got.Data) != 0 {
		t.Fatalf("external row carries inline bytes: %d", len(got.Data))
	}

	// List exposes provenance without dragging bytes.
	list, err := s.Media().List(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list: %d rows", len(list))
	}
	seen := map[string]model.Media{}
	for _, md := range list {
		seen[md.ID] = md
	}
	if seen[ext.ID].StorageEngine != MediaEngineOpendal || seen[legacy.ID].StorageEngine != "" {
		t.Fatalf("list provenance: %+v", seen)
	}

	// MarkExternal flips the legacy row: key/engine set, bytes cleared,
	// size preserved. Replay is a no-op (row already external).
	if err := s.Media().MarkExternal(ctx, tenant, legacy.ID, "media/"+legacy.ID, MediaEngineOpendal); err != nil {
		t.Fatalf("mark external: %v", err)
	}
	got, err = s.Media().Get(ctx, tenant, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.StorageKey != "media/"+legacy.ID || got.StorageEngine != MediaEngineOpendal ||
		len(got.Data) != 0 || got.SizeBytes != len("legacy-bytes") {
		t.Fatalf("post-mark row: %+v", got)
	}
	if err := s.Media().MarkExternal(ctx, tenant, legacy.ID, "media/"+legacy.ID, MediaEngineOpendal); err != nil {
		t.Fatalf("mark replay: %v", err)
	}
	// Missing row → ErrNotFound.
	if err := s.Media().MarkExternal(ctx, tenant, newTestUUID(), "media/none", MediaEngineOpendal); err != httpx.ErrNotFound {
		t.Fatalf("mark missing: %v", err)
	}

	// RLS: the other tenant sees nothing.
	list, err = s.Media().List(ctx, other)
	if err != nil || len(list) != 0 {
		t.Fatalf("cross-tenant list: %d %v", len(list), err)
	}
	if _, err := s.Media().Get(ctx, other, legacy.ID); err != httpx.ErrNotFound {
		t.Fatalf("cross-tenant get: %v", err)
	}
}
