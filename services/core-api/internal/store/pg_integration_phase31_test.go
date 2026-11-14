//go:build pg

package store

// Phase 31: tenant_media round-trip + RLS isolation + catalog image_id
// persistence (the card-image write path).

import (
	"testing"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPGMedia31(t *testing.T) {
	s := pgTestStore(t)
	tenantID := pgTenant(t, s, "media31")
	otherID := pgTenant(t, s, "media31b")

	md := &model.Media{
		ID: newTestUUID(), TenantID: tenantID, Kind: model.MediaKindHero,
		ContentType: "image/png", Data: []byte{0x89, 0x50, 0x4E, 0x47}, SizeBytes: 4,
	}
	if err := s.Media().Put(t.Context(), md); err != nil {
		t.Fatal(err)
	}

	// Round-trip: bytes and metadata survive.
	got, err := s.Media().Get(t.Context(), tenantID, md.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ContentType != "image/png" || got.Kind != "hero" || len(got.Data) != 4 || got.SizeBytes != 4 {
		t.Fatalf("media round-trip: %+v", got)
	}

	// List is metadata-only.
	list, err := s.Media().List(t.Context(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != md.ID || list[0].Data != nil {
		t.Fatalf("list: %+v", list)
	}

	// RLS: the other tenant cannot read, list or delete this row.
	if _, err := s.Media().Get(t.Context(), otherID, md.ID); err != httpx.ErrNotFound {
		t.Fatalf("cross-tenant read: %v", err)
	}
	if l, err := s.Media().List(t.Context(), otherID); err != nil || len(l) != 0 {
		t.Fatalf("cross-tenant list: %v %v", l, err)
	}
	if err := s.Media().Delete(t.Context(), otherID, md.ID); err != httpx.ErrNotFound {
		t.Fatalf("cross-tenant delete: %v", err)
	}

	// Delete is tenant-scoped and final.
	if err := s.Media().Delete(t.Context(), tenantID, md.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Media().Get(t.Context(), tenantID, md.ID); err != httpx.ErrNotFound {
		t.Fatalf("deleted read: %v", err)
	}
}

func TestPGCatalogImage31(t *testing.T) {
	s := pgTestStore(t)
	tenantID := pgTenant(t, s, "catimg31")

	svc := &model.CatalogService{
		ID: newTestUUID(), TenantID: tenantID, Vertical: "PRIVATE_TOUR",
		Name: "Sintra Tour", BaseMinor: 35000, Currency: "EUR", Active: true,
		ImageID: newTestUUID(),
	}
	if err := s.Catalog().Add(t.Context(), svc); err != nil {
		t.Fatal(err)
	}
	list, err := s.Catalog().List(t.Context(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ImageID != svc.ImageID {
		t.Fatalf("image_id lost on add: %+v", list)
	}

	// Update path (the studio/console write) persists image changes.
	list[0].ImageID = ""
	if err := s.Catalog().Update(t.Context(), &list[0]); err != nil {
		t.Fatal(err)
	}
	again, err := s.Catalog().List(t.Context(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 1 || again[0].ImageID != "" {
		t.Fatalf("image_id update lost: %+v", again)
	}
}
