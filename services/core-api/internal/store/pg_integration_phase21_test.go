//go:build pg

// Postgres integration tests — Phase 21: message templates (CRUD + RLS),
// tenant operator_config roundtrip, webhook rotation-state persistence
// (regression: UpdateSubscription used to drop secret rotations silently),
// and the pgTenants.List full-column scan fix. Same gate as
// pg_integration_test.go.
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGTemplateStore(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-tpl")
	other := pgTenant(t, s, "pg-tpl-other")

	tmpl := &model.MessageTemplate{ID: newTestUUID(), TenantID: tenant,
		Name: "arrival", Purpose: "service",
		Body: "Welcome {{client_name}}", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := s.Templates().Upsert(ctx, tmpl); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Upsert by (tenant, name) updates in place — same id survives.
	upd := &model.MessageTemplate{ID: newTestUUID(), TenantID: tenant,
		Name: "arrival", Purpose: "any",
		Body: "Welcome back {{client_name}}", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := s.Templates().Upsert(ctx, upd); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if upd.ID != tmpl.ID {
		t.Fatalf("upsert must keep the original id: %s vs %s", upd.ID, tmpl.ID)
	}
	got, err := s.Templates().GetByName(ctx, tenant, "arrival")
	if err != nil || got.Body != "Welcome back {{client_name}}" || got.Purpose != "any" {
		t.Fatalf("get-by-name after upsert: %+v, %v", got, err)
	}

	list, err := s.Templates().List(ctx, tenant)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v, %v", list, err)
	}

	// RLS: the other tenant sees nothing, even with a known id.
	if cross, err := s.Templates().List(ctx, other); err != nil || len(cross) != 0 {
		t.Fatalf("RLS leak on list: %+v, %v", cross, err)
	}
	if _, err := s.Templates().Get(ctx, other, tmpl.ID); err == nil {
		t.Fatal("RLS leak: cross-tenant Get must not find the template")
	}

	if err := s.Templates().Delete(ctx, tenant, tmpl.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if list, _ := s.Templates().List(ctx, tenant); len(list) != 0 {
		t.Fatal("delete must remove the row")
	}
}

func TestPGOperatorConfig(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-ops")

	got, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.OperatorConfig.AlertPhones) != 0 {
		t.Fatalf("default roster must be empty: %+v", got.OperatorConfig)
	}

	got.OperatorConfig = model.OperatorConfig{AlertPhones: []string{"+971111111111", "+971222222222"}}
	if err := s.Tenants().UpdateConfig(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	back, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("re-get: %v", err)
	}
	if len(back.OperatorConfig.AlertPhones) != 2 ||
		back.OperatorConfig.AlertPhones[0] != "+971111111111" {
		t.Fatalf("roster roundtrip: %+v", back.OperatorConfig)
	}
}

func TestPGWebhookRotationPersistence(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-rot")

	sub := &model.WebhookSubscription{ID: newTestUUID(), TenantID: tenant,
		URL: "https://ops.example.com/hook", Events: []string{"*"},
		Secret: "whk_old", Active: true, CreatedAt: time.Now().UTC()}
	if err := s.Webhooks().CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Rotate: prev retained, rotated_at stamped. Regression — the pg
	// UpdateSubscription used to leave secret columns untouched, silently
	// losing every rotation (memory engine was correct).
	sub.PrevSecret = sub.Secret
	sub.Secret = "whk_new"
	sub.SecretRotatedAt = time.Now().UTC()
	if err := s.Webhooks().UpdateSubscription(ctx, sub); err != nil {
		t.Fatalf("update: %v", err)
	}
	back, err := s.Webhooks().GetSubscription(ctx, tenant, sub.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if back.Secret != "whk_new" || back.PrevSecret != "whk_old" {
		t.Fatalf("rotation state must persist: %+v", back)
	}
	if back.SecretRotatedAt.IsZero() {
		t.Fatal("secret_rotated_at must roundtrip")
	}

	// Never-rotated subscription: empty prev, zero time.
	fresh := &model.WebhookSubscription{ID: newTestUUID(), TenantID: tenant,
		URL: "https://ops.example.com/other", Events: []string{"quote.created"},
		Secret: "whk_fresh", Active: true, CreatedAt: time.Now().UTC()}
	if err := s.Webhooks().CreateSubscription(ctx, fresh); err != nil {
		t.Fatalf("create fresh: %v", err)
	}
	gotFresh, err := s.Webhooks().GetSubscription(ctx, tenant, fresh.ID)
	if err != nil {
		t.Fatalf("get fresh: %v", err)
	}
	if gotFresh.PrevSecret != "" || !gotFresh.SecretRotatedAt.IsZero() {
		t.Fatalf("fresh subscription must have no rotation state: %+v", gotFresh)
	}
}

func TestPGTenantList(t *testing.T) {
	s := pgTestStore(t)
	tenant := pgTenant(t, s, "pg-list")
	// Regression: List selected 14 config columns but scanned 10 — it failed
	// at runtime the first time anyone called it against pg.
	tenants, err := s.Tenants().List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, tn := range tenants {
		if tn.ID == tenant {
			found = true
		}
	}
	if !found {
		t.Fatal("created tenant must appear in List")
	}
}
