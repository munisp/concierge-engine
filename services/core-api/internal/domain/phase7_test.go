package domain

import (
	"context"
	"testing"
	"time"
)

// Phase 11 receipts: channel delivery/read callbacks as SLA evidence.

func TestReceiptsRecordAndQuery(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

	if _, err := s.Receipts.Record(ctx, tenantID, "whatsapp", "wamid.reply-1", "sent", at); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Receipts.Record(ctx, tenantID, "whatsapp", "wamid.reply-1", "delivered", at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Receipts.Record(ctx, tenantID, "whatsapp", "wamid.reply-1", "read", at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	got, err := s.Receipts.For(ctx, tenantID, "wamid.reply-1")
	if err != nil || len(got) != 3 {
		t.Fatalf("receipts: %+v, %v", got, err)
	}
	counts, err := s.Receipts.Counts(ctx, tenantID)
	if err != nil || counts["sent"] != 1 || counts["delivered"] != 1 || counts["read"] != 1 {
		t.Fatalf("counts: %+v, %v", counts, err)
	}
}

func TestReceiptsIdempotentRedelivery(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	for i := 0; i < 3; i++ { // webhook redelivery storm
		if _, err := s.Receipts.Record(ctx, tenantID, "whatsapp", "wamid.x", "delivered", time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.Receipts.For(ctx, tenantID, "wamid.x")
	if len(got) != 1 {
		t.Fatalf("redelivery must dedupe: %+v", got)
	}
}

func TestReceiptsRejectUnknownStatus(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	if _, err := s.Receipts.Record(ctx, tenantID, "whatsapp", "wamid.x", "teleported", time.Time{}); err == nil {
		t.Fatal("unknown status must be rejected — audit vocabulary stays clean")
	}
	if _, err := s.Receipts.Record(ctx, tenantID, "whatsapp", "", "sent", time.Time{}); err == nil {
		t.Fatal("empty external_ref must be rejected")
	}
}

func TestReceiptsCrossTenantIsolation(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	t1, t2 := seedTenant(t, s), seedTenant(t, s)

	if _, err := s.Receipts.Record(ctx, t1, "whatsapp", "wamid.shared", "read", time.Time{}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Receipts.For(ctx, t2, "wamid.shared")
	if len(got) != 0 {
		t.Fatalf("t2 must never see t1 receipts: %+v", got)
	}
	counts, _ := s.Receipts.Counts(ctx, t2)
	if len(counts) != 0 {
		t.Fatalf("t2 counts must be empty: %+v", counts)
	}
}
