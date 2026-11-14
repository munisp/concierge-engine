//go:build pg

// Postgres integration tests — Phase 15: client locale/timezone roundtrip,
// search, merge (re-point + delete, RLS), client-scoped listings, webhook
// delivery lookup. Same gate as pg_integration_test.go.
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGClientLocaleSearchMerge(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-crm360")
	other := pgTenant(t, s, "pg-crm360-other")

	winner := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Ada Lovelace",
		Phone: "+2348012345678", KYCStatus: "VERIFIED", Consent: map[string]any{},
		Locale: "en-NG", Timezone: "Africa/Lagos"}
	if err := s.Clients().Upsert(ctx, winner); err != nil {
		t.Fatalf("winner: %v", err)
	}
	got, err := s.Clients().Get(ctx, tenant, winner.ID)
	if err != nil || got.Locale != "en-NG" || got.Timezone != "Africa/Lagos" {
		t.Fatalf("locale roundtrip: %+v, %v", got, err)
	}

	loser := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Ada L",
		Email: "ada@example.com", KYCStatus: "none", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, loser); err != nil {
		t.Fatalf("loser: %v", err)
	}

	// Search (ILIKE) finds both; other tenant's search sees nothing.
	found, err := s.Clients().Search(ctx, tenant, "ada", 10)
	if err != nil || len(found) != 2 {
		t.Fatalf("search: %+v, %v", found, err)
	}
	cross, err := s.Clients().Search(ctx, other, "ada", 10)
	if err != nil || len(cross) != 0 {
		t.Fatalf("RLS leak on search: %+v, %v", cross, err)
	}

	// Loser carries a request and a case; merge folds them into the winner.
	req := &model.ServiceRequest{ID: newTestUUID(), TenantID: tenant, ClientID: loser.ID,
		State: "NEW", Version: 1, IdempotencyKey: "pg-merge-r1",
		Items:    []model.ServiceItem{{ID: newTestUUID(), TenantID: tenant, Vertical: "CHAUFFEUR_POINT_TO_POINT"}},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := s.Requests().Create(ctx, req); err != nil {
		t.Fatalf("request: %v", err)
	}
	cs := &model.Case{ID: newTestUUID(), TenantID: tenant, ClientID: loser.ID,
		Title: "lost bag", Kind: "incident", Priority: "P3", State: "OPEN",
		IdempotencyKey: "pg-merge-c1", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := s.Cases().Create(ctx, cs); err != nil {
		t.Fatalf("case: %v", err)
	}
	conv := &model.Conversation{ID: newTestUUID(), TenantID: tenant, ClientID: loser.ID,
		Channel: "WHATSAPP", State: "OPEN", CreatedAt: time.Now().UTC()}
	if err := s.Conversations().CreateConversation(ctx, conv); err != nil {
		t.Fatalf("conversation: %v", err)
	}

	if err := s.Clients().Merge(ctx, tenant, winner.ID, loser.ID); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, err := s.Clients().Get(ctx, tenant, loser.ID); err == nil {
		t.Fatal("loser must be deleted")
	}
	reqs, err := s.Requests().ForClient(ctx, tenant, winner.ID, 10)
	if err != nil || len(reqs) != 1 || reqs[0].ID != req.ID {
		t.Fatalf("requests ForClient: %+v, %v", reqs, err)
	}
	cases, err := s.Cases().ForClient(ctx, tenant, winner.ID, 10)
	if err != nil || len(cases) != 1 || cases[0].ID != cs.ID {
		t.Fatalf("cases ForClient: %+v, %v", cases, err)
	}
	convs, err := s.Conversations().ForClient(ctx, tenant, winner.ID, 10)
	if err != nil || len(convs) != 1 || convs[0].ID != conv.ID {
		t.Fatalf("conversations ForClient: %+v, %v", convs, err)
	}
}

func TestPGBookingsForClient(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-bfc")

	c := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Booker",
		Phone: "+971500000300", KYCStatus: "none", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, c); err != nil {
		t.Fatal(err)
	}
	req := &model.ServiceRequest{ID: newTestUUID(), TenantID: tenant, ClientID: c.ID,
		State: "NEW", Version: 1, IdempotencyKey: "pg-bfc-r1",
		Items:    []model.ServiceItem{{ID: newTestUUID(), TenantID: tenant, Vertical: "CHAUFFEUR_POINT_TO_POINT"}},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := s.Requests().Create(ctx, req); err != nil {
		t.Fatal(err)
	}
	q := &model.Quote{ID: newTestUUID(), TenantID: tenant, RequestID: req.ID, Version: 1,
		Origin: "INSTANT", TotalMinor: 9000, Currency: "EUR",
		ExpiresAt: time.Now().UTC().Add(time.Hour), CreatedAt: time.Now().UTC()}
	if err := s.Quotes().Create(ctx, q); err != nil {
		t.Fatal(err)
	}
	b := &model.Booking{ID: newTestUUID(), TenantID: tenant, RequestID: req.ID, QuoteID: q.ID,
		State: "CONFIRMED", CreatedAt: time.Now().UTC()}
	if err := s.Bookings().Create(ctx, b); err != nil {
		t.Fatal(err)
	}
	bks, err := s.Bookings().ForClient(ctx, tenant, c.ID, 10)
	if err != nil || len(bks) != 1 || bks[0].ID != b.ID {
		t.Fatalf("bookings ForClient: %+v, %v", bks, err)
	}
}

func TestPGWebhookGetDelivery(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-whd")

	sub := &model.WebhookSubscription{ID: newTestUUID(), TenantID: tenant,
		URL: "https://receiver.example/hooks", Events: []string{"booking.*"},
		Secret: "whk_test", Active: true, CreatedAt: time.Now().UTC()}
	if err := s.Webhooks().CreateSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	d := &model.WebhookDelivery{ID: newTestUUID(), TenantID: tenant, SubscriptionID: sub.ID,
		EventID: "1", EventKind: "booking.confirmed", Payload: "{}",
		MaxAttempts: 8, NextAttemptAt: time.Now().UTC(), CreatedAt: time.Now().UTC()}
	if err := s.Webhooks().EnqueueDelivery(ctx, d); err != nil {
		t.Fatal(err)
	}
	got, err := s.Webhooks().GetDelivery(ctx, tenant, d.ID)
	if err != nil || got.EventKind != "booking.confirmed" {
		t.Fatalf("get delivery: %+v, %v", got, err)
	}
	if _, err := s.Webhooks().GetDelivery(ctx, tenant, newTestUUID()); err == nil {
		t.Fatal("unknown delivery must not resolve")
	}
}
