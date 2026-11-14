//go:build pg

// Postgres integration tests — the first real exercise of the pg store.
// Run with: CE_PG_TEST_DSN=postgres://... go test -tags pg ./internal/store/
// The test applies db/migrations itself, so the migration files are under
// test too (that is how the 0007 TEXT/uuid FK mismatch was caught).
package store

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func pgTestStore(t *testing.T) Store {
	t.Helper()
	dsn := os.Getenv("CE_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("CE_PG_TEST_DSN not set — pg integration tests are opt-in")
	}
	s, err := NewPostgres(dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(s.Close)

	// CI applies migrations as the superuser and tests as a non-superuser
	// role (RLS only bites for non-superusers). For local runs against a
	// superuser DSN, CE_PG_APPLY_MIGRATIONS=1 makes the test apply them —
	// the schema files stay under test either way.
	if os.Getenv("CE_PG_APPLY_MIGRATIONS") == "1" {
		p := s.(*postgres)
		files, err := filepath.Glob("../../../db/migrations/*.sql")
		if err != nil || len(files) == 0 {
			t.Fatalf("migrations not found: %v", err)
		}
		sort.Strings(files)
		for _, f := range files {
			sqlText, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			if _, err := p.db.ExecContext(context.Background(), string(sqlText)); err != nil {
				t.Fatalf("apply %s: %v", filepath.Base(f), err)
			}
		}
	}
	return s
}

func pgTenant(t *testing.T, s Store, slug string) string {
	t.Helper()
	id := newTestUUID()
	err := s.Tenants().Create(context.Background(), &model.Tenant{
		ID: id, Slug: slug, Name: "PG Test " + slug, Archetype: "smb",
	})
	if err != nil {
		t.Fatalf("create tenant %s: %v", slug, err)
	}
	return id
}

func newTestUUID() string {
	// uuid v4, same shape as domain.NewID (store tests can't import domain).
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func TestPGReceiptsRoundtripIdempotent(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-receipts")

	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	for _, st := range []string{"sent", "delivered", "read"} {
		rc := &model.MessageReceipt{ID: newTestUUID(), TenantID: tenant,
			Channel: "whatsapp", ExternalRef: "wamid.pg-1", Status: st,
			At: at, CreatedAt: at}
		if err := s.Receipts().AddReceipt(ctx, rc); err != nil {
			t.Fatalf("add %s: %v", st, err)
		}
	}
	// Redelivery storm: replays must dedupe to one row per status.
	for i := 0; i < 3; i++ {
		rc := &model.MessageReceipt{ID: newTestUUID(), TenantID: tenant,
			Channel: "whatsapp", ExternalRef: "wamid.pg-1", Status: "read",
			At: at, CreatedAt: at}
		if err := s.Receipts().AddReceipt(ctx, rc); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Receipts().Receipts(ctx, tenant, "wamid.pg-1")
	if err != nil || len(got) != 3 {
		t.Fatalf("receipts: %d, %v", len(got), err)
	}
	counts, err := s.Receipts().ReceiptCounts(ctx, tenant)
	if err != nil || counts["sent"] != 1 || counts["read"] != 1 {
		t.Fatalf("counts: %+v, %v", counts, err)
	}
}

func TestPGReceiptLinksAndFailures(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-links")

	c := &model.Case{ID: newTestUUID(), TenantID: tenant, Title: "pg case",
		Kind: "incident", Priority: "P2", State: model.CaseOpen,
		CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := s.Cases().Create(ctx, c); err != nil {
		t.Fatalf("create case: %v", err)
	}
	// Read back: NULL client_id etc. must scan cleanly (Phase 12 fix).
	got, err := s.Cases().Get(ctx, tenant, c.ID)
	if err != nil || got.Title != "pg case" || got.ClientID != "" {
		t.Fatalf("get case: %+v, %v", got, err)
	}

	if err := s.Receipts().LinkReceipt(ctx, tenant, "wamid.pg-2", c.ID); err != nil {
		t.Fatalf("link: %v", err)
	}
	// First link wins.
	if err := s.Receipts().LinkReceipt(ctx, tenant, "wamid.pg-2", newTestUUID()); err != nil {
		t.Fatalf("relink must be a no-op, not an error: %v", err)
	}
	linked, err := s.Receipts().LinkedCase(ctx, tenant, "wamid.pg-2")
	if err != nil || linked != c.ID {
		t.Fatalf("linked case: %q, %v", linked, err)
	}
	refs, err := s.Receipts().RefsForCase(ctx, tenant, c.ID)
	if err != nil || len(refs) != 1 || refs[0] != "wamid.pg-2" {
		t.Fatalf("refs for case: %+v, %v", refs, err)
	}

	at := time.Date(2026, 11, 14, 11, 0, 0, 0, time.UTC)
	rc := &model.MessageReceipt{ID: newTestUUID(), TenantID: tenant,
		Channel: "whatsapp", ExternalRef: "wamid.pg-2", Status: "failed",
		At: at, CreatedAt: at}
	if err := s.Receipts().AddReceipt(ctx, rc); err != nil {
		t.Fatal(err)
	}
	failed, err := s.Receipts().ReceiptsByStatus(ctx, tenant, "failed", 10)
	if err != nil || len(failed) != 1 || failed[0].ExternalRef != "wamid.pg-2" {
		t.Fatalf("failures: %+v, %v", failed, err)
	}
}

func TestPGCrossTenantIsolationRLS(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenantA := pgTenant(t, s, "pg-rls-a")
	tenantB := pgTenant(t, s, "pg-rls-b")

	rc := &model.MessageReceipt{ID: newTestUUID(), TenantID: tenantA,
		Channel: "whatsapp", ExternalRef: "wamid.secret", Status: "read",
		At: time.Now(), CreatedAt: time.Now()}
	if err := s.Receipts().AddReceipt(ctx, rc); err != nil {
		t.Fatal(err)
	}
	// Tenant B must not see tenant A's receipts — RLS, not app filtering.
	got, err := s.Receipts().Receipts(ctx, tenantB, "wamid.secret")
	if err != nil || len(got) != 0 {
		t.Fatalf("RLS leak: tenant B read %d receipts, %v", len(got), err)
	}
	counts, err := s.Receipts().ReceiptCounts(ctx, tenantB)
	if err != nil || len(counts) != 0 {
		t.Fatalf("RLS leak in counts: %+v, %v", counts, err)
	}
	// Cross-tenant link read must be invisible too.
	linked, err := s.Receipts().LinkedCase(ctx, tenantB, "wamid.secret")
	if err != nil || linked != "" {
		t.Fatalf("RLS leak in links: %q, %v", linked, err)
	}
}

func TestPGGeoAreasRoundtrip(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-geo")

	area := &model.ServiceArea{ID: newTestUUID(), TenantID: tenant,
		Name: "Lekki", CreatedAt: time.Now(),
		Polygon: [][2]float64{{6.41, 3.39}, {6.41, 3.44}, {6.45, 3.44}, {6.45, 3.39}}}
	if err := s.Geo().AddArea(ctx, area); err != nil {
		t.Fatalf("add area (postgis live): %v", err)
	}
	areas, err := s.Geo().Areas(ctx, tenant)
	if err != nil || len(areas) != 1 || areas[0].Name != "Lekki" {
		t.Fatalf("areas: %+v, %v", areas, err)
	}
	if len(areas[0].Polygon) != 4 {
		t.Fatalf("polygon ring must survive WKT roundtrip: %+v", areas[0].Polygon)
	}
}
