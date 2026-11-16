//go:build pg

// Postgres integration tests — Phase 46: retention purge/scrub primitives
// through RLS, tenant isolation, and the retention_config tenant column
// round-trip. Gate: CE_PG_TEST_DSN. Requires db/migrations/0032_phase46.sql.
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// backdate moves rows into the past through an RLS-scoped tenant tx —
// geostamps/messages carry no caller-set timestamp on insert (the column
// defaults to now()), so tests adjust after the fact.
func backdate(t *testing.T, s Store, tenant, sqlText string, args ...any) {
	t.Helper()
	tx, err := s.(*postgres).tenantTx(context.Background(), tenant)
	if err != nil {
		t.Fatalf("backdate tx: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(context.Background(), sqlText, args...); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("backdate commit: %v", err)
	}
}

func TestPGRetentionPrimitives46(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-retention")
	other := pgTenant(t, s, "pg-retention-other")

	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Retention Client",
		Phone: "+971500000460", KYCStatus: "none", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatalf("client: %v", err)
	}
	old := time.Now().UTC().AddDate(0, 0, -800)
	const marker = "[content removed by data retention policy]"

	// Geo: one old, one fresh stamp on a real assignment; one old stamp for
	// the OTHER tenant.
	bk := pgQuoteBornBooking(t, s, tenant, cl.ID, "pg-ret-b1")
	a := &model.Assignment{ID: newTestUUID(), TenantID: tenant, BookingID: bk, Status: "COMPLETED"}
	if err := s.Dispatch().CreateAssignment(ctx, a); err != nil {
		t.Fatalf("assignment: %v", err)
	}
	oldGeo := &model.GeoStamp{ID: newTestUUID(), TenantID: tenant, AssignmentID: a.ID, Kind: "PING", Lat: 1, Lng: 2}
	freshGeo := &model.GeoStamp{ID: newTestUUID(), TenantID: tenant, AssignmentID: a.ID, Kind: "PING", Lat: 3, Lng: 4}
	for _, g := range []*model.GeoStamp{oldGeo, freshGeo} {
		if err := s.Dispatch().AddGeoStamp(ctx, g); err != nil {
			t.Fatalf("geostamp: %v", err)
		}
	}
	backdate(t, s, tenant, `UPDATE geostamps SET at=$1 WHERE id=$2`, old, oldGeo.ID)

	clOther := &model.Client{ID: newTestUUID(), TenantID: other, FullName: "Retention Other",
		Phone: "+971500000461", KYCStatus: "none", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, clOther); err != nil {
		t.Fatalf("other client: %v", err)
	}
	bk2 := pgQuoteBornBooking(t, s, other, clOther.ID, "pg-ret-b2")
	a2 := &model.Assignment{ID: newTestUUID(), TenantID: other, BookingID: bk2, Status: "COMPLETED"}
	if err := s.Dispatch().CreateAssignment(ctx, a2); err != nil {
		t.Fatalf("assignment2: %v", err)
	}
	otherGeo := &model.GeoStamp{ID: newTestUUID(), TenantID: other, AssignmentID: a2.ID, Kind: "PING", Lat: 5, Lng: 6}
	if err := s.Dispatch().AddGeoStamp(ctx, otherGeo); err != nil {
		t.Fatalf("other geostamp: %v", err)
	}
	backdate(t, s, other, `UPDATE geostamps SET at=$1 WHERE id=$2`, old, otherGeo.ID)

	// Messages: CLOSED conversation with an old + a fresh body; OPEN
	// conversation with an old body (must survive the age sweep).
	closed := &model.Conversation{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		Channel: "whatsapp", State: "CLOSED"}
	open := &model.Conversation{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		Channel: "whatsapp", State: "OPEN"}
	for _, c := range []*model.Conversation{closed, open} {
		if err := s.Conversations().CreateConversation(ctx, c); err != nil {
			t.Fatalf("conversation: %v", err)
		}
	}
	oldMsg := &model.Message{ID: newTestUUID(), TenantID: tenant, ConversationID: closed.ID,
		Author: "CLIENT", Body: "old closed body"}
	freshMsg := &model.Message{ID: newTestUUID(), TenantID: tenant, ConversationID: closed.ID,
		Author: "AI", Body: "fresh closed body"}
	openMsg := &model.Message{ID: newTestUUID(), TenantID: tenant, ConversationID: open.ID,
		Author: "CLIENT", Body: "old open body"}
	for _, m := range []*model.Message{oldMsg, freshMsg, openMsg} {
		if err := s.Conversations().AppendMessage(ctx, m); err != nil {
			t.Fatalf("message: %v", err)
		}
	}
	backdate(t, s, tenant, `UPDATE messages SET sent_at=$1 WHERE id IN ($2, $3)`, old, oldMsg.ID, openMsg.ID)

	// Attribution: one old (explicit At is honored by Track), one fresh.
	if err := s.Attribution().Track(ctx, &model.AttributionEvent{
		TenantID: tenant, Kind: "page_view", Channel: "seo", At: old}); err != nil {
		t.Fatalf("track old: %v", err)
	}
	if err := s.Attribution().Track(ctx, &model.AttributionEvent{
		TenantID: tenant, Kind: "page_view", Channel: "seo"}); err != nil {
		t.Fatalf("track fresh: %v", err)
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -90)

	// Geo purge: the old stamp of THIS tenant goes, fresh + other-tenant survive.
	n, err := s.Dispatch().PurgeGeoStampsBefore(ctx, tenant, cutoff)
	if err != nil || n != 1 {
		t.Fatalf("geo purge: %d %v", n, err)
	}
	if _, err := s.Dispatch().LatestGeoStamp(ctx, tenant, a.ID); err != nil {
		t.Fatalf("fresh stamp must survive: %v", err)
	}
	if got, err := s.Dispatch().LatestGeoStamp(ctx, other, a2.ID); err != nil || got.ID != otherGeo.ID {
		t.Fatalf("RLS: other tenant's stamp swept: %v", err)
	}

	// Body scrub: old CLOSED-conversation body marker-replaced; fresh closed
	// and old OPEN bodies survive.
	n, err = s.Conversations().ScrubMessageBodiesBefore(ctx, tenant, time.Now().UTC().AddDate(0, 0, -730), marker)
	if err != nil || n != 1 {
		t.Fatalf("body scrub: %d %v", n, err)
	}
	tl, err := s.Conversations().Timeline(ctx, tenant, closed.ID)
	if err != nil || len(tl) != 2 {
		t.Fatalf("closed timeline: %d %v", len(tl), err)
	}
	bodies := map[string]string{}
	for _, m := range tl {
		bodies[m.ID] = m.Body
	}
	if bodies[oldMsg.ID] != marker || bodies[freshMsg.ID] != "fresh closed body" {
		t.Fatalf("closed bodies: %+v", bodies)
	}
	openTL, _ := s.Conversations().Timeline(ctx, tenant, open.ID)
	if len(openTL) != 1 || openTL[0].Body != "old open body" {
		t.Fatalf("open conversation swept by age: %+v", openTL)
	}

	// Scrub idempotency: the same window again counts 0 (the marker is
	// excluded by the body <> marker predicate).
	if n, err = s.Conversations().ScrubMessageBodiesBefore(ctx, tenant, time.Now().UTC().AddDate(0, 0, -730), marker); err != nil || n != 0 {
		t.Fatalf("scrub not idempotent: %d %v", n, err)
	}

	// Erasure path: per-client scrub takes the OPEN conversation too.
	n, err = s.Conversations().ScrubMessageBodiesForClient(ctx, tenant, cl.ID, marker)
	if err != nil || n != 2 { // open old body + fresh closed body
		t.Fatalf("client scrub: %d %v", n, err)
	}

	// Attribution purge: old event goes, fresh stays; other tenant untouched.
	n, err = s.Attribution().PurgeBefore(ctx, tenant, cutoff)
	if err != nil || n != 1 {
		t.Fatalf("attribution purge: %d %v", n, err)
	}
	funnel, err := s.Attribution().Funnel(ctx, tenant)
	if err != nil {
		t.Fatalf("funnel: %v", err)
	}
	total := 0
	for _, f := range funnel {
		total += f.Count
	}
	if total != 1 {
		t.Fatalf("fresh attribution must survive: %+v", funnel)
	}
}

func TestPGRetentionConfig46(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-retcfg")

	tn, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	tn.RetentionConfig = model.RetentionConfig{GeoStampsDays: 30, MessageBodiesDays: -1, AttributionDays: 365}
	if err := s.Tenants().UpdateConfig(ctx, tn); err != nil {
		t.Fatalf("update: %v", err)
	}
	back, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("reget: %v", err)
	}
	if back.RetentionConfig.GeoStampsDays != 30 || back.RetentionConfig.MessageBodiesDays != -1 ||
		back.RetentionConfig.AttributionDays != 365 || back.RetentionConfig.Disabled {
		t.Fatalf("retention_config roundtrip: %+v", back.RetentionConfig)
	}

	// List (the hourly sweeper's path) carries it too.
	tenants, err := s.Tenants().List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, row := range tenants {
		if row.ID == tenant {
			found = true
			if row.RetentionConfig.GeoStampsDays != 30 {
				t.Fatalf("List lost retention_config: %+v", row.RetentionConfig)
			}
		}
	}
	if !found {
		t.Fatal("tenant missing from List")
	}

	// A config-less tenant reads as zero-value (defaults apply at sweep time).
	fresh := pgTenant(t, s, "pg-retcfg-fresh")
	fn, err := s.Tenants().Get(ctx, fresh)
	if err != nil {
		t.Fatalf("get fresh: %v", err)
	}
	if fn.RetentionConfig != (model.RetentionConfig{}) {
		t.Fatalf("fresh tenant must read zero retention config: %+v", fn.RetentionConfig)
	}
}
