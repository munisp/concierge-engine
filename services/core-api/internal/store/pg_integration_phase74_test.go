//go:build pg

package store

// Postgres integration tests — Phase 74: folio checkout intents persist
// with NO quote (quote_id NULL — the charge is against a folio, not a
// guest quote), round-trip verbatim, transition to CAPTURED for
// reconciliation, and stay RLS-isolated.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGFolioCheckoutIntents74(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-fl74")
	other := pgTenant(t, s, "pg-fl74b")
	now := time.Now().UTC().Truncate(time.Second)

	intent := &model.PaymentIntent{
		ID: newTestUUID(), TenantID: tenant, Mode: "INVOICE",
		AmountMinor: 5000, Currency: "EUR", PSP: "stripe",
		State: "LINK_SENT", IdempotencyKey: "foliopay:" + newTestUUID() + ":1",
		CreatedAt: now,
	}
	if err := s.Payments().CreateIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}

	// Round-trip: quote-less intent reads back with an empty quote.
	got, err := s.Payments().GetIntent(ctx, tenant, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.QuoteID != "" || got.Mode != "INVOICE" || got.AmountMinor != 5000 ||
		got.PSP != "stripe" || got.State != "LINK_SENT" {
		t.Fatalf("intent round-trip: %+v", got)
	}

	// Idempotent create on the same key converges to the same intent.
	replay := &model.PaymentIntent{
		ID: newTestUUID(), TenantID: tenant, Mode: "INVOICE",
		AmountMinor: 5000, Currency: "EUR", PSP: "stripe",
		State: "LINK_SENT", IdempotencyKey: intent.IdempotencyKey, CreatedAt: now,
	}
	if err := s.Payments().CreateIntent(ctx, replay); err != nil {
		t.Fatal(err)
	}
	if replay.ID != intent.ID {
		t.Fatalf("idempotent create diverged: %s != %s", replay.ID, intent.ID)
	}

	// Capture transition (the webhook reconciliation step).
	if err := s.Payments().UpdateIntentState(ctx, tenant, intent.ID, "CAPTURED", "cs_folio_pg"); err != nil {
		t.Fatal(err)
	}
	got, err = s.Payments().GetIntent(ctx, tenant, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "CAPTURED" || got.PSPRef != "cs_folio_pg" {
		t.Fatalf("captured intent: %+v", got)
	}

	// RLS: the intent is invisible cross-tenant.
	if _, err := s.Payments().GetIntent(ctx, other, intent.ID); err == nil {
		t.Fatal("cross-tenant intent read must fail")
	}
}
