package domain

// Phase 46: data retention & minimization engine — default posture purges
// aged geo stamps / scrubs aged closed-conversation bodies / purges aged
// attribution; Disabled and negative per-category values opt out; erasure
// scrubs the client's conversation content immediately.

import (
	"context"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// seedRetentionData plants one old + one fresh geo stamp, a CLOSED
// conversation with one old + one fresh message, an OPEN conversation with
// one old message, and one old + one fresh attribution event.
func seedRetentionData(t *testing.T, s *Services, tenantID, clientID string) {
	t.Helper()
	ctx := context.Background()
	now := s.Now()
	old, fresh := now.AddDate(0, 0, -800), now.Add(-time.Hour)

	for i, at := range []time.Time{old, fresh} {
		g := &model.GeoStamp{ID: NewID(), TenantID: tenantID, AssignmentID: NewID(),
			Kind: "PING", Lat: 52.3 + float64(i), Lng: 4.8, At: at}
		if err := s.Store.Dispatch().AddGeoStamp(ctx, g); err != nil {
			t.Fatalf("geostamp: %v", err)
		}
	}

	closed := &model.Conversation{ID: NewID(), TenantID: tenantID, ClientID: clientID,
		Channel: "whatsapp", State: "CLOSED"}
	open := &model.Conversation{ID: NewID(), TenantID: tenantID, ClientID: clientID,
		Channel: "whatsapp", State: "OPEN"}
	for _, c := range []*model.Conversation{closed, open} {
		if err := s.Store.Conversations().CreateConversation(ctx, c); err != nil {
			t.Fatalf("conversation: %v", err)
		}
	}
	msgs := []*model.Message{
		{ID: NewID(), TenantID: tenantID, ConversationID: closed.ID, Author: "CLIENT",
			Body: "old closed body", SentAt: old},
		{ID: NewID(), TenantID: tenantID, ConversationID: closed.ID, Author: "AI",
			Body: "fresh closed body", SentAt: fresh},
		{ID: NewID(), TenantID: tenantID, ConversationID: open.ID, Author: "CLIENT",
			Body: "old open body", SentAt: old},
	}
	for _, m := range msgs {
		if err := s.Store.Conversations().AppendMessage(ctx, m); err != nil {
			t.Fatalf("message: %v", err)
		}
	}

	for _, at := range []time.Time{old, fresh} {
		if err := s.Store.Attribution().Track(ctx, &model.AttributionEvent{
			TenantID: tenantID, Kind: "page_view", Channel: "seo", At: at}); err != nil {
			t.Fatalf("attribution: %v", err)
		}
	}
}

func TestRetentionDefaults46(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	rr, err := s.Clients.ResolveV2(ctx, tenantID, ResolveInput{Phone: "+15550004646", Name: "Retention Client"})
	if err != nil {
		t.Fatal(err)
	}
	seedRetentionData(t, s, tenantID, rr.Client.ID)

	res, err := s.Retention.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	// Defaults: geo 90d, bodies 730d, attribution 730d — the 800-day-old
	// rows go, the hour-old rows stay.
	if res.GeoStampsPurged != 1 || res.MessageBodiesScrubbed != 1 || res.AttributionPurged != 1 {
		t.Fatalf("counts: %+v", res)
	}

	// The scrub is content-only: both message ROWS of the closed
	// conversation survive, the old one marker-replaced.
	tl, err := s.Store.Conversations().Timeline(ctx, tenantID, conversationOf(t, s, tenantID, "CLOSED"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tl) != 2 {
		t.Fatalf("closed timeline rows: %d", len(tl))
	}
	scrubbed, fresh := 0, 0
	for _, m := range tl {
		if m.Body == RetentionScrubMarker {
			scrubbed++
		}
		if m.Body == "fresh closed body" {
			fresh++
		}
	}
	if scrubbed != 1 || fresh != 1 {
		t.Fatalf("closed bodies: %+v", tl)
	}

	// Open conversations are NOT swept by age — an active thread keeps its
	// content regardless of age (only erasure touches it).
	openTL, _ := s.Store.Conversations().Timeline(ctx, tenantID, conversationOf(t, s, tenantID, "OPEN"))
	if len(openTL) != 1 || openTL[0].Body != "old open body" {
		t.Fatalf("open conversation swept: %+v", openTL)
	}

	// Idempotent: a second run counts nothing.
	res2, err := s.Retention.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if res2.GeoStampsPurged+res2.MessageBodiesScrubbed+res2.AttributionPurged != 0 {
		t.Fatalf("second run not idempotent: %+v", res2)
	}

	// The sweep is audit-logged (accountability).
	entries, _ := s.Audit.List(ctx, tenantID, 10)
	found := false
	for _, e := range entries {
		if e.Action == "retention.sweep" {
			found = true
		}
	}
	if !found {
		t.Fatal("retention sweep missing from audit log")
	}
}

// conversationOf finds the test tenant's conversation in the given state.
func conversationOf(t *testing.T, s *Services, tenantID, state string) string {
	t.Helper()
	convs, err := s.Store.Conversations().ForClient(context.Background(), tenantID,
		clientOf(t, s, tenantID), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range convs {
		if c.State == state {
			return c.ID
		}
	}
	t.Fatalf("no %s conversation", state)
	return ""
}

func clientOf(t *testing.T, s *Services, tenantID string) string {
	t.Helper()
	clients, err := s.Store.Clients().List(context.Background(), tenantID, 10)
	if err != nil || len(clients) == 0 {
		t.Fatalf("clients: %d %v", len(clients), err)
	}
	return clients[0].ID
}

func TestRetentionDisabledAndNegative46(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	rr, err := s.Clients.ResolveV2(ctx, tenantID, ResolveInput{Phone: "+15550004647", Name: "Keep Client"})
	if err != nil {
		t.Fatal(err)
	}
	seedRetentionData(t, s, tenantID, rr.Client.ID)

	// Disabled tenant: the sweep is a reported no-op.
	tn, _ := s.Tenants.Get(ctx, tenantID)
	tn.RetentionConfig = model.RetentionConfig{Disabled: true}
	if _, err := s.Tenants.UpdateConfig(ctx, tn); err != nil {
		t.Fatal(err)
	}
	res, err := s.Retention.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Disabled || res.GeoStampsPurged+res.MessageBodiesScrubbed+res.AttributionPurged != 0 {
		t.Fatalf("disabled sweep: %+v", res)
	}

	// Negative category = never purge that category; others still sweep.
	tn.RetentionConfig = model.RetentionConfig{GeoStampsDays: -1, MessageBodiesDays: 30, AttributionDays: -1}
	if _, err := s.Tenants.UpdateConfig(ctx, tn); err != nil {
		t.Fatal(err)
	}
	res, err = s.Retention.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if res.GeoStampsPurged != 0 || res.AttributionPurged != 0 || res.MessageBodiesScrubbed != 1 {
		t.Fatalf("negative-category sweep: %+v", res)
	}
}

func TestRetentionErasureScrub46(t *testing.T) {
	s := testServices(t, Deps{TripSecret: "test-secret"})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	rr, err := s.Clients.ResolveV2(ctx, tenantID, ResolveInput{Phone: "+15550004648", Name: "Erase Me"})
	if err != nil {
		t.Fatal(err)
	}
	seedRetentionData(t, s, tenantID, rr.Client.ID)

	// Art. 17: erasure scrubs the client's conversation content NOW —
	// including the OPEN conversation age-based sweeps leave alone.
	if _, err := s.Clients.EraseWithEvidence(ctx, tenantID, rr.Client.ID); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"CLOSED", "OPEN"} {
		tl, err := s.Store.Conversations().Timeline(ctx, tenantID, conversationOf(t, s, tenantID, state))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range tl {
			if strings.Contains(m.Body, "body") {
				t.Fatalf("%s conversation kept content after erasure: %+v", state, m)
			}
		}
	}
}
