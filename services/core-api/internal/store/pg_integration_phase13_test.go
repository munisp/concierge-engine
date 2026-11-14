//go:build pg

// Postgres integration tests — Phase 13: extend live-pg coverage to the
// Phase 1–3 store surfaces (clients, catalog, requests, quotes, bookings,
// conversations, partners, referrals, corporate). Same gate as
// pg_integration_test.go (CE_PG_TEST_DSN).
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGClientsCRUD(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-clients")

	c := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Ada Vip",
		Phone: "+971501234567", Email: "ada@example.com", KYCStatus: "VERIFIED",
		VIPFlags: []string{"vip"}, Consent: map[string]any{"marketing": true}}
	if err := s.Clients().Upsert(ctx, c); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.Clients().Get(ctx, tenant, c.ID)
	if err != nil || got.FullName != "Ada Vip" || got.KYCStatus != "VERIFIED" {
		t.Fatalf("get: %+v, %v", got, err)
	}
	byPhone, err := s.Clients().FindByPhone(ctx, tenant, "+971501234567")
	if err != nil || byPhone.ID != c.ID {
		t.Fatalf("find by phone: %+v, %v", byPhone, err)
	}
	// Upsert is a true upsert: same id, changed name.
	c.FullName = "Ada Lovelace"
	if err := s.Clients().Upsert(ctx, c); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _ = s.Clients().Get(ctx, tenant, c.ID)
	if got.FullName != "Ada Lovelace" {
		t.Fatalf("upsert must update: %+v", got)
	}

	pref := &model.Preference{ID: newTestUUID(), TenantID: tenant, ClientID: c.ID,
		Key: "seat", Value: "window", ConsentScope: "service", RecordedBy: "concierge-1"}
	if err := s.Clients().SavePreference(ctx, pref); err != nil {
		t.Fatalf("save pref: %v", err)
	}
	prefs, err := s.Clients().Preferences(ctx, tenant, c.ID)
	if err != nil || len(prefs) != 1 || prefs[0].Value != "window" {
		t.Fatalf("prefs: %+v, %v", prefs, err)
	}
}

func TestPGCatalogRequestsQuotesBookings(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-booking-flow")

	svc := &model.CatalogService{ID: newTestUUID(), TenantID: tenant,
		Vertical: "chauffeur", Name: "Airport transfer", BaseMinor: 15000,
		Currency: "EUR", PerKMMinor: 250, AutoQuotable: true, Active: true}
	if err := s.Catalog().Add(ctx, svc); err != nil {
		t.Fatalf("catalog add: %v", err)
	}
	cat, err := s.Catalog().List(ctx, tenant)
	if err != nil || len(cat) != 1 || cat[0].Name != "Airport transfer" {
		t.Fatalf("catalog list: %+v, %v", cat, err)
	}

	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Booker",
		Phone: "+971502222222", KYCStatus: "VERIFIED"}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatal(err)
	}

	req := &model.ServiceRequest{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		State: model.StateNew, Version: 1, IdempotencyKey: "req-pg-1",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Items: []model.ServiceItem{{ID: newTestUUID(), TenantID: tenant,
			Vertical: "chauffeur", PickupAddress: "DXB T3", PickupAt: time.Now().Unix(),
			Passengers: 2}}}
	if err := s.Requests().Create(ctx, req); err != nil {
		t.Fatalf("request create: %v", err)
	}
	// Idempotent replay returns the original.
	replay := &model.ServiceRequest{TenantID: tenant, IdempotencyKey: "req-pg-1"}
	if err := s.Requests().Create(ctx, replay); err != nil || replay.ID != req.ID {
		t.Fatalf("idempotent replay: %+v, %v", replay, err)
	}
	gotReq, err := s.Requests().Get(ctx, tenant, req.ID)
	if err != nil || gotReq.ClientID != cl.ID || len(gotReq.Items) != 1 {
		t.Fatalf("request get: %+v, %v", gotReq, err)
	}

	q := &model.Quote{ID: newTestUUID(), TenantID: tenant, RequestID: req.ID,
		Version: 1, Origin: "INSTANT", TotalMinor: 18500, DepositMinor: 5000,
		Currency: "EUR", Lines: []model.QuoteLine{{Description: "base", AmountMinor: 15000}},
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	if err := s.Quotes().Create(ctx, q); err != nil {
		t.Fatalf("quote create: %v", err)
	}
	gotQ, err := s.Quotes().Get(ctx, tenant, q.ID)
	if err != nil || gotQ.TotalMinor != 18500 || len(gotQ.Lines) != 1 {
		t.Fatalf("quote get: %+v, %v", gotQ, err)
	}
	latest, err := s.Quotes().LatestForRequest(ctx, tenant, req.ID)
	if err != nil || latest.ID != q.ID {
		t.Fatalf("latest for request: %+v, %v", latest, err)
	}

	b := &model.Booking{ID: newTestUUID(), TenantID: tenant, RequestID: req.ID,
		QuoteID: q.ID, State: "CONFIRMED", CreatedAt: time.Now()}
	if err := s.Bookings().Create(ctx, b); err != nil {
		t.Fatalf("booking create: %v", err)
	}
	if err := s.Bookings().UpdateState(ctx, tenant, b.ID, "COMPLETED"); err != nil {
		t.Fatalf("booking state: %v", err)
	}
	gotB, err := s.Bookings().Get(ctx, tenant, b.ID)
	if err != nil || gotB.State != "COMPLETED" {
		t.Fatalf("booking get: %+v, %v", gotB, err)
	}
	bookings, err := s.Bookings().List(ctx, tenant)
	if err != nil || len(bookings) != 1 {
		t.Fatalf("booking list: %+v, %v", bookings, err)
	}
}

func TestPGConversationsTimeline(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-conversations")

	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Chatty",
		Phone: "+971503333333", KYCStatus: "VERIFIED"}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatal(err)
	}
	conv := &model.Conversation{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		Channel: "WHATSAPP", State: "OPEN", ExternalRef: "wamid.conv-1", CreatedAt: time.Now()}
	if err := s.Conversations().CreateConversation(ctx, conv); err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	for _, m := range []struct{ author, body string }{
		{"CLIENT", "need a car at 6am"},
		{"HUMAN_STAFF", "confirmed — chauffeur assigned"},
	} {
		if err := s.Conversations().AppendMessage(ctx, &model.Message{
			ID: newTestUUID(), TenantID: tenant, ConversationID: conv.ID,
			Author: m.author, Body: m.body, SentAt: time.Now()}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	timeline, err := s.Conversations().Timeline(ctx, tenant, conv.ID)
	if err != nil || len(timeline) != 2 || timeline[0].Author != "CLIENT" {
		t.Fatalf("timeline: %+v, %v", timeline, err)
	}
	if err := s.Conversations().SetState(ctx, tenant, conv.ID, "RESOLVED"); err != nil {
		t.Fatalf("set state: %v", err)
	}
	got, err := s.Conversations().GetConversation(ctx, tenant, conv.ID)
	if err != nil || got.State != "RESOLVED" || got.ExternalRef != "wamid.conv-1" {
		t.Fatalf("get conversation: %+v, %v", got, err)
	}
}

func TestPGPartnersAndReferrals(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenantA := pgTenant(t, s, "pg-partner-a")
	tenantB := pgTenant(t, s, "pg-partner-b")

	p := &model.Partner{ID: newTestUUID(), TenantID: tenantA, Name: "Gulf Fleet",
		Verticals: []string{"chauffeur"}, Markets: []string{"DXB"}, Status: "VETTED",
		QualityScore: 0.95, PayoutRail: "MANUAL", Lat: 25.2048, Lng: 55.2708, CreatedAt: time.Now()}
	if err := s.Partners().Create(ctx, p); err != nil {
		t.Fatalf("partner create: %v", err)
	}
	got, err := s.Partners().Get(ctx, tenantA, p.ID)
	if err != nil || got.Name != "Gulf Fleet" || got.Status != "VETTED" {
		t.Fatalf("partner get: %+v, %v", got, err)
	}
	// Phase 6 coordinates must roundtrip — NearestPartners depends on them.
	if got.Lat != 25.2048 || got.Lng != 55.2708 {
		t.Fatalf("partner coordinates lost: %+v", got)
	}
	p.Status = "SUSPENDED"
	if err := s.Partners().Update(ctx, p); err != nil {
		t.Fatalf("partner update: %v", err)
	}
	list, err := s.Partners().List(ctx, tenantA)
	if err != nil || len(list) != 1 || list[0].Status != "SUSPENDED" {
		t.Fatalf("partner list: %+v, %v", list, err)
	}
	// RLS: tenant B must not see tenant A's partner.
	if listB, _ := s.Partners().List(ctx, tenantB); len(listB) != 0 {
		t.Fatalf("RLS leak: tenant B sees %+v", listB)
	}

	// Cross-tenant referral: A refers to B; both see it, nobody else does.
	r := &model.Referral{ID: newTestUUID(), ReferringTenantID: tenantA,
		ReceivingTenantID: tenantB, ClientName: "Referred VIP",
		ClientContact: "+971504444444", RequestSummary: "weekend driver",
		EstValueMinor: 50000, Currency: "EUR", FeePct: 0.10, State: "OFFERED",
		IdempotencyKey: "ref-pg-1", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := s.Referrals().Create(ctx, r); err != nil {
		t.Fatalf("referral create: %v", err)
	}
	refsA, err := s.Referrals().List(ctx, tenantA)
	if err != nil || len(refsA) != 1 {
		t.Fatalf("referrals A: %+v, %v", refsA, err)
	}
	refsB, err := s.Referrals().List(ctx, tenantB)
	if err != nil || len(refsB) != 1 {
		t.Fatalf("referrals B: %+v, %v", refsB, err)
	}
	gotR, err := s.Referrals().Get(ctx, tenantB, r.ID)
	if err != nil || gotR.State != "OFFERED" {
		t.Fatalf("referral get: %+v, %v", gotR, err)
	}
	gotR.State = "ACCEPTED"
	gotR.UpdatedAt = time.Now()
	if err := s.Referrals().Update(ctx, gotR); err != nil {
		t.Fatalf("referral update: %v", err)
	}
}
