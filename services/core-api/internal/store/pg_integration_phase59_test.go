//go:build pg

package store

// Postgres integration tests — Phase 59: soundscapes round-trip
// (create/get/update/list), the one-render-per-booking idempotency unique
// index conflicts, RLS isolation holds across tenants, and migration 0038
// columns survive the round-trip verbatim (provenance is never implied).

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func pgSoundscapeBooking(t *testing.T, s Store, tenant, suffix string) (clientID, bookingID string) {
	t.Helper()
	ctx := context.Background()
	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Sound " + suffix,
		Phone: "+97150" + suffix, KYCStatus: "VERIFIED"}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatal(err)
	}
	req := &model.ServiceRequest{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		State: model.StateNew, Version: 1, IdempotencyKey: "req-pg59-" + suffix,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Items: []model.ServiceItem{{ID: newTestUUID(), TenantID: tenant,
			Vertical: "chauffeur", PickupAddress: "Lekki", PickupAt: time.Now().Unix(),
			Passengers: 1}}}
	if err := s.Requests().Create(ctx, req); err != nil {
		t.Fatal(err)
	}
	q := &model.Quote{ID: newTestUUID(), TenantID: tenant, RequestID: req.ID,
		Version: 1, Origin: "INSTANT", TotalMinor: 12000, Currency: "EUR",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	if err := s.Quotes().Create(ctx, q); err != nil {
		t.Fatal(err)
	}
	b := &model.Booking{ID: newTestUUID(), TenantID: tenant, RequestID: req.ID,
		QuoteID: q.ID, State: "CONFIRMED", CreatedAt: time.Now()}
	if err := s.Bookings().Create(ctx, b); err != nil {
		t.Fatal(err)
	}
	return cl.ID, b.ID
}

func TestPGSoundscapes59(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-sound59")
	clientID, bookingID := pgSoundscapeBooking(t, s, tenant, "7000001")

	sc := &model.Soundscape{
		ID: newTestUUID(), TenantID: tenant, BookingID: bookingID, ClientID: clientID,
		Status: model.SoundscapeRendered, Genre: "afrobeats", Language: "yo",
		Mood: "chill", TempoBPM: 90, Title: "Signature Ride — afrobeats",
		Duration: 180, Provider: "mubert", AiComposed: true,
		Detail: "ref:ext-1", MediaID: "m-1",
		IdempotencyKey: "soundscape:" + bookingID,
		CreatedAt:      time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.Soundscapes().Create(ctx, sc); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Idempotency: same key conflicts, never double-renders.
	dup := &model.Soundscape{
		ID: newTestUUID(), TenantID: tenant, BookingID: bookingID, ClientID: clientID,
		Status: model.SoundscapeRendering, IdempotencyKey: "soundscape:" + bookingID,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.Soundscapes().Create(ctx, dup); err != httpx.ErrConflict {
		t.Fatalf("idem conflict: %v", err)
	}

	got, err := s.Soundscapes().Get(ctx, tenant, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Genre != "afrobeats" || got.Language != "yo" || !got.AiComposed ||
		got.Provider != "mubert" || got.TempoBPM != 90 || got.MediaID != "m-1" {
		t.Fatalf("round-trip: %+v", got)
	}

	fb, err := s.Soundscapes().ForBooking(ctx, tenant, bookingID)
	if err != nil || fb.ID != sc.ID {
		t.Fatalf("for booking: %+v %v", fb, err)
	}

	// Update: degrade honestly.
	got.Status = model.SoundscapeDegraded
	got.AiComposed = false
	got.Provider = ""
	got.Detail = "provider-error: timeout"
	got.FallbackID = "curated:afrobeats-lounge"
	got.MediaID = ""
	got.UpdatedAt = time.Now().UTC()
	if err := s.Soundscapes().Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, err := s.Soundscapes().Get(ctx, tenant, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != model.SoundscapeDegraded || again.AiComposed ||
		again.FallbackID != "curated:afrobeats-lounge" || again.Detail != "provider-error: timeout" {
		t.Fatalf("update: %+v", again)
	}

	// List for client.
	list, err := s.Soundscapes().ListForClient(ctx, tenant, clientID, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}

	// RLS: another tenant sees nothing.
	tenant2 := pgTenant(t, s, "pg-sound59-b")
	if _, err := s.Soundscapes().Get(ctx, tenant2, sc.ID); err != httpx.ErrNotFound {
		t.Fatalf("rls get: %v", err)
	}
	if _, err := s.Soundscapes().ForBooking(ctx, tenant2, bookingID); err != httpx.ErrNotFound {
		t.Fatalf("rls for-booking: %v", err)
	}
}
