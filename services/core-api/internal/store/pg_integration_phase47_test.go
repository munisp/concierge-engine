//go:build pg

package store

// Postgres integration tests — Phase 47: KYC case CRUD through RLS, the
// one-open-case unique index, idempotency-key conflict, expiry + erasure
// scrub primitives, tenant isolation, and the kyc_config tenant column
// round-trip. Gate: CE_PG_TEST_DSN. Requires db/migrations/0033_phase47.sql.

import (
	"context"
	"errors"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPGKycCases47(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-kyc")
	other := pgTenant(t, s, "pg-kyc-other")

	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Kyc Client",
		Phone: "+971500000470", KYCStatus: "none", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatalf("client: %v", err)
	}

	// Create + Get round-trip.
	kc := &model.KycCase{
		ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		Level: model.KycLevelStandard, Status: model.KycStatusPending,
		Provider: "manual", IdempotencyKey: "pg-kyc-1",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.KycCases().Create(ctx, kc); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.KycCases().Get(ctx, tenant, kc.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Level != model.KycLevelStandard || got.Status != model.KycStatusPending || got.Provider != "manual" {
		t.Fatalf("round-trip: %+v", got)
	}

	// Idempotency key conflict + GetByIdem replay path.
	dup := *kc
	dup.ID = newTestUUID()
	if err := s.KycCases().Create(ctx, &dup); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("idem replay = %v, want conflict", err)
	}
	byIdem, err := s.KycCases().GetByIdem(ctx, tenant, "pg-kyc-1")
	if err != nil || byIdem.ID != kc.ID {
		t.Fatalf("get by idem: %v %+v", err, byIdem)
	}

	// One open case per (client, level) — the partial unique index.
	second := &model.KycCase{
		ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		Level: model.KycLevelStandard, Status: model.KycStatusPending,
		Provider: "manual", IdempotencyKey: "pg-kyc-2",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.KycCases().Create(ctx, second); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("second open case = %v, want conflict", err)
	}
	// A DIFFERENT level is fine.
	enh := &model.KycCase{
		ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		Level: model.KycLevelEnhanced, Status: model.KycStatusPending,
		Provider: "manual", IdempotencyKey: "pg-kyc-3",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.KycCases().Create(ctx, enh); err != nil {
		t.Fatalf("different level must coexist: %v", err)
	}

	// OpenFor finds the open case at the level.
	open, err := s.KycCases().OpenFor(ctx, tenant, cl.ID, model.KycLevelStandard)
	if err != nil || open.ID != kc.ID {
		t.Fatalf("open for: %v %+v", err, open)
	}

	// Update: submit metadata, then decide.
	kc.Status = model.KycStatusInReview
	kc.DocumentType = "passport"
	kc.DocumentRef = "vault://taylor/pg-1"
	kc.SubmittedAt = time.Now().UTC()
	kc.UpdatedAt = time.Now().UTC()
	if err := s.KycCases().Update(ctx, kc); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = s.KycCases().Get(ctx, tenant, kc.ID)
	if got.Status != model.KycStatusInReview || got.DocumentType != "passport" || got.SubmittedAt.IsZero() {
		t.Fatalf("post-submit: %+v", got)
	}
	kc.Status = model.KycStatusApproved
	kc.DecidedBy = "ops-lead"
	kc.DecidedAt = time.Now().UTC()
	if err := s.KycCases().Update(ctx, kc); err != nil {
		t.Fatalf("decide: %v", err)
	}
	// Once terminal, OpenFor at that level misses.
	if _, err := s.KycCases().OpenFor(ctx, tenant, cl.ID, model.KycLevelStandard); !errors.Is(err, httpx.ErrNotFound) {
		t.Fatalf("open for terminal = %v, want not found", err)
	}

	// List filters by status; ListForClient returns both cases.
	approved, err := s.KycCases().List(ctx, tenant, model.KycStatusApproved, 10)
	if err != nil || len(approved) != 1 {
		t.Fatalf("approved list: %v %+v", err, approved)
	}
	all, err := s.KycCases().ListForClient(ctx, tenant, cl.ID)
	if err != nil || len(all) != 2 {
		t.Fatalf("client list: %v len=%d", err, len(all))
	}

	// ExpireBefore: backdate the ENHANCED case past the window, expire.
	backdate(t, s, tenant, `UPDATE kyc_cases SET created_at = $3 WHERE tenant_id = $1 AND id = $2`,
		tenant, enh.ID, time.Now().UTC().AddDate(0, 0, -31))
	n, err := s.KycCases().ExpireBefore(ctx, tenant, time.Now().UTC().AddDate(0, 0, -30))
	if err != nil || n != 1 {
		t.Fatalf("expire: %v n=%d", err, n)
	}
	if n, _ := s.KycCases().ExpireBefore(ctx, tenant, time.Now().UTC()); n != 0 {
		t.Fatalf("expire not idempotent: %d", n)
	}

	// Erasure scrub: document refs blank, rows survive.
	m, err := s.KycCases().ScrubDocumentRefsForClient(ctx, tenant, cl.ID)
	if err != nil || m != 1 { // only the APPROVED case had refs
		t.Fatalf("scrub: %v n=%d", err, m)
	}
	got, _ = s.KycCases().Get(ctx, tenant, kc.ID)
	if got.DocumentType != "" || got.DocumentRef != "" || got.Status != model.KycStatusApproved {
		t.Fatalf("post-scrub: %+v", got)
	}
	if m, _ := s.KycCases().ScrubDocumentRefsForClient(ctx, tenant, cl.ID); m != 0 {
		t.Fatalf("scrub not idempotent: %d", m)
	}

	// RLS isolation: the other tenant sees nothing of this client's cases.
	if _, err := s.KycCases().Get(ctx, other, kc.ID); !errors.Is(err, httpx.ErrNotFound) {
		t.Fatalf("cross-tenant get = %v, want not found", err)
	}
	otherList, err := s.KycCases().List(ctx, other, "", 10)
	if err != nil || len(otherList) != 0 {
		t.Fatalf("cross-tenant list: %v %+v", err, otherList)
	}
}

func TestPGKycConfig47(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-kyc-config")

	// Zero-value config round-trips.
	tn, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if tn.KycConfig != (model.KycConfig{}) {
		t.Fatalf("fresh tenant kyc_config = %+v, want zero", tn.KycConfig)
	}

	// Patch + read back via Get and List.
	tn.KycConfig = model.KycConfig{StandardAboveMinor: 500000, EnhancedAboveMinor: -1, CaseExpiryDays: 14}
	if err := s.Tenants().UpdateConfig(ctx, tn); err != nil {
		t.Fatal(err)
	}
	back, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if back.KycConfig.StandardAboveMinor != 500000 || back.KycConfig.EnhancedAboveMinor != -1 || back.KycConfig.CaseExpiryDays != 14 {
		t.Fatalf("round-trip: %+v", back.KycConfig)
	}
	list, err := s.Tenants().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, lt := range list {
		if lt.ID == tenant {
			found = true
			if lt.KycConfig.StandardAboveMinor != 500000 {
				t.Fatalf("list kyc_config: %+v", lt.KycConfig)
			}
		}
	}
	if !found {
		t.Fatal("tenant missing from list")
	}
}
