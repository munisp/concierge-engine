package domain

// Phase 48: Mojaloop settlement/payout depth — quote expiry, callback-auth
// plumbing lives in the server tests, core-side timeout sweeps, settlement
// batching (netting) and the partner settlement statement.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// Quote expiry: the scheme's FX lock is real — Execute refuses a stale
// quote instead of riding a rate the scheme no longer honors.
func TestPayoutQuoteExpiry48(t *testing.T) {
	adapter := fakeMojaloop(t)
	defer adapter.Close()
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	s := testServices(t, Deps{MojaloopURL: adapter.URL, PublicBase: "http://core",
		Now: func() time.Time { return now }})
	tenantID := seedTenant(t, s)
	ctx := context.Background()
	partner := vettedPartner(t, s, tenantID, "MOJALOOP")

	p, err := s.Payouts.Create(ctx, tenantID, partner.ID, "b-x", 10000, "EUR", "po-x48")
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.Payouts.Quote(ctx, tenantID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.QuoteExpiresAt.IsZero() {
		t.Fatal("quote expiry not stored from the scheme response")
	}
	// Jump past the scheme lock (fake adapter locks to 2030-01-01).
	now = time.Date(2030, 6, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.Payouts.Execute(ctx, tenantID, p.ID); err == nil {
		t.Fatal("executed against an expired quote")
	}
}

// Core-side recovery: an IN_FLIGHT payout whose callback never arrives is
// compensated by the sweep — dropped notifications must never strand funds.
func TestPayoutTimeoutSweep48(t *testing.T) {
	adapter := fakeMojaloop(t)
	defer adapter.Close()
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	s := testServices(t, Deps{MojaloopURL: adapter.URL, PublicBase: "http://core",
		Now: func() time.Time { return now }})
	tenantID := seedTenant(t, s)
	ctx := context.Background()
	partner := vettedPartner(t, s, tenantID, "MOJALOOP")

	p, err := s.Payouts.Create(ctx, tenantID, partner.ID, "b-t", 10000, "EUR", "po-t48")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Payouts.Quote(ctx, tenantID, p.ID); err != nil {
		t.Fatal(err)
	}
	p, err = s.Payouts.Execute(ctx, tenantID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Not yet stale: the sweep leaves it alone.
	now = now.Add(24 * time.Hour)
	if n, err := s.Payouts.SweepTimeouts(ctx, tenantID); err != nil || n != 0 {
		t.Fatalf("swept a fresh payout: n=%d err=%v", n, err)
	}
	// Past the 48h settlement timeout: compensated, payable restored.
	now = now.Add(25 * time.Hour)
	if n, err := s.Payouts.SweepTimeouts(ctx, tenantID); err != nil || n != 1 {
		t.Fatalf("sweep n=%d err=%v", n, err)
	}
	p, err = s.Payouts.Get(ctx, tenantID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.State != model.PayoutCompensated {
		t.Fatalf("state %q", p.State)
	}
	// Sweep replay is a no-op — compensation is absorbing.
	if n, err := s.Payouts.SweepTimeouts(ctx, tenantID); err != nil || n != 0 {
		t.Fatalf("sweep replay n=%d err=%v", n, err)
	}
}

// Batch lifecycle: assemble 3 EUR payouts into one scheme transfer, quote
// the netted amount, execute, fulfil — lines settle pro-rata with the same
// ledger keys as the single flow.
func TestSettlementBatchLifecycle48(t *testing.T) {
	adapter := fakeMojaloop(t)
	defer adapter.Close()
	s := testServices(t, Deps{MojaloopURL: adapter.URL, PublicBase: "http://core"})
	tenantID := seedTenant(t, s)
	ctx := context.Background()
	partner := vettedPartner(t, s, tenantID, "MOJALOOP")

	amounts := []int64{10000, 20000, 40000}
	var ids []string
	for i, amt := range amounts {
		p, err := s.Payouts.Create(ctx, tenantID, partner.ID, "b-"+string(rune('a'+i)), amt, "EUR", "po-b48-"+string(rune('a'+i)))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}

	b, err := s.Settlements.Assemble(ctx, tenantID, partner.ID, "EUR", "asm-1")
	if err != nil {
		t.Fatal(err)
	}
	if b.State != model.BatchOpen || b.AmountMinor != 70000 {
		t.Fatalf("batch: %+v", b)
	}
	// Assemble replay returns the same batch.
	replay, err := s.Settlements.Assemble(ctx, tenantID, partner.ID, "EUR", "asm-1")
	if err != nil || replay.ID != b.ID {
		t.Fatal("assemble not idempotent")
	}
	// Lines are linked and no longer eligible for new batches.
	for _, id := range ids {
		p, err := s.Payouts.Get(ctx, tenantID, id)
		if err != nil {
			t.Fatal(err)
		}
		if p.BatchID != b.ID {
			t.Fatalf("line %s not linked to batch", id)
		}
	}
	if _, err := s.Settlements.Assemble(ctx, tenantID, partner.ID, "EUR", "asm-2"); err == nil {
		t.Fatal("assembled a batch from already-batched lines")
	}
	// Nothing to batch with a single pending payout.
	p2, err := s.Payouts.Create(ctx, tenantID, partner.ID, "b-solo", 5000, "EUR", "po-solo48")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Settlements.Assemble(ctx, tenantID, partner.ID, "EUR", "asm-3"); err == nil {
		t.Fatal("batched a single payout")
	}
	_ = p2

	if _, err := s.Settlements.ExecuteBatch(ctx, tenantID, b.ID); err == nil {
		t.Fatal("executed an unquoted batch")
	}
	b, err = s.Settlements.QuoteBatch(ctx, tenantID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.State != model.BatchQuoted || b.ReceiveMinor != 9500 || b.QuoteExpiresAt.IsZero() {
		t.Fatalf("batch quote: %+v", b)
	}
	b, err = s.Settlements.ExecuteBatch(ctx, tenantID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.State != model.BatchInFlight || b.TransferID == "" {
		t.Fatalf("batch execute: %+v", b)
	}
	for _, id := range ids {
		p, err := s.Payouts.Get(ctx, tenantID, id)
		if err != nil {
			t.Fatal(err)
		}
		if p.State != model.PayoutInFlight || p.TransferID != "" {
			t.Fatalf("line %s not in flight (or leaked the batch transfer id): %+v", id, p)
		}
	}

	// Fulfil: pro-rata receive allocation, remainder to the last line.
	b, err = s.Settlements.HandleBatchCallback(ctx, tenantID, b.TransferID, "transfer.fulfilled")
	if err != nil {
		t.Fatal(err)
	}
	if b.State != model.BatchSettled {
		t.Fatalf("batch state %q", b.State)
	}
	var settledReceive int64
	for _, id := range ids {
		p, err := s.Payouts.Get(ctx, tenantID, id)
		if err != nil {
			t.Fatal(err)
		}
		if p.State != model.PayoutSettled || p.SettledAt.IsZero() {
			t.Fatalf("line %s not settled: %+v", id, p)
		}
	}
	// Callback replay is a no-op (absorbing state).
	if _, err := s.Settlements.HandleBatchCallback(ctx, tenantID, b.TransferID, "transfer.fulfilled"); err != nil {
		t.Fatal("batch callback replay not idempotent")
	}

	// Ledger: per-line fulfil postings with single-flow idem keys summing to
	// the netted receive amount.
	postings, err := s.Store.Payments().Postings(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, lp := range postings {
		for _, id := range ids {
			if lp.IdempotencyKey == "payout:fulfil:"+id && lp.CreditCode == "PARTNER_CLEARING" {
				seen[id] = true
				settledReceive += lp.AmountMinor
			}
		}
	}
	if len(seen) != 3 || settledReceive != 9500 {
		t.Fatalf("pro-rata allocation wrong: seen=%d sum=%d", len(seen), settledReceive)
	}
}

// Batch abort: every member line compensates back to the partner payable.
func TestSettlementBatchAbort48(t *testing.T) {
	adapter := fakeMojaloop(t)
	defer adapter.Close()
	s := testServices(t, Deps{MojaloopURL: adapter.URL, PublicBase: "http://core"})
	tenantID := seedTenant(t, s)
	ctx := context.Background()
	partner := vettedPartner(t, s, tenantID, "MOJALOOP")

	var ids []string
	for i, amt := range []int64{10000, 30000} {
		p, err := s.Payouts.Create(ctx, tenantID, partner.ID, "b-ab"+string(rune('a'+i)), amt, "EUR", "po-ab48-"+string(rune('a'+i)))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	b, err := s.Settlements.Assemble(ctx, tenantID, partner.ID, "EUR", "asm-ab")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Settlements.QuoteBatch(ctx, tenantID, b.ID); err != nil {
		t.Fatal(err)
	}
	b, err = s.Settlements.ExecuteBatch(ctx, tenantID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.Settlements.HandleBatchCallback(ctx, tenantID, b.TransferID, "transfer.aborted")
	if err != nil {
		t.Fatal(err)
	}
	if b.State != model.BatchCompensated {
		t.Fatalf("batch state %q", b.State)
	}
	for _, id := range ids {
		p, err := s.Payouts.Get(ctx, tenantID, id)
		if err != nil {
			t.Fatal(err)
		}
		if p.State != model.PayoutCompensated {
			t.Fatalf("line %s state %q", id, p.State)
		}
	}
}

// Batch timeout sweep: stale IN_FLIGHT batch compensates itself.
func TestSettlementBatchTimeoutSweep48(t *testing.T) {
	adapter := fakeMojaloop(t)
	defer adapter.Close()
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	s := testServices(t, Deps{MojaloopURL: adapter.URL, PublicBase: "http://core",
		Now: func() time.Time { return now }})
	tenantID := seedTenant(t, s)
	ctx := context.Background()
	partner := vettedPartner(t, s, tenantID, "MOJALOOP")

	for i, amt := range []int64{10000, 10000} {
		if _, err := s.Payouts.Create(ctx, tenantID, partner.ID, "b-sw"+string(rune('a'+i)), amt, "EUR", "po-sw48-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	b, err := s.Settlements.Assemble(ctx, tenantID, partner.ID, "EUR", "asm-sw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Settlements.QuoteBatch(ctx, tenantID, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Settlements.ExecuteBatch(ctx, tenantID, b.ID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(49 * time.Hour)
	if n, err := s.Settlements.SweepBatchTimeouts(ctx, tenantID); err != nil || n != 1 {
		t.Fatalf("batch sweep n=%d err=%v", n, err)
	}
	b, err = s.Settlements.GetBatch(ctx, tenantID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.State != model.BatchCompensated {
		t.Fatalf("batch state %q", b.State)
	}
}

// Statement: per-currency reconciliation — pending, in-flight, settled and
// compensated buckets never blend currencies.
func TestSettlementStatement48(t *testing.T) {
	adapter := fakeMojaloop(t)
	defer adapter.Close()
	s := testServices(t, Deps{MojaloopURL: adapter.URL, PublicBase: "http://core"})
	tenantID := seedTenant(t, s)
	ctx := context.Background()
	partner := vettedPartner(t, s, tenantID, "MOJALOOP")

	// Two EUR payouts settle as a batch; one USD payout stays pending.
	var ids []string
	for i, amt := range []int64{10000, 20000} {
		p, err := s.Payouts.Create(ctx, tenantID, partner.ID, "b-st"+string(rune('a'+i)), amt, "EUR", "po-st48-"+string(rune('a'+i)))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	if _, err := s.Payouts.Create(ctx, tenantID, partner.ID, "b-st-usd", 7000, "USD", "po-st48-usd"); err != nil {
		t.Fatal(err)
	}
	b, err := s.Settlements.Assemble(ctx, tenantID, partner.ID, "EUR", "asm-st")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Settlements.QuoteBatch(ctx, tenantID, b.ID); err != nil {
		t.Fatal(err)
	}
	b, err = s.Settlements.ExecuteBatch(ctx, tenantID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Settlements.HandleBatchCallback(ctx, tenantID, b.TransferID, "transfer.fulfilled"); err != nil {
		t.Fatal(err)
	}

	st, err := s.Settlements.Statement(ctx, tenantID, partner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Currencies) != 2 {
		t.Fatalf("currencies: %+v", st.Currencies)
	}
	byCur := map[string]SettlementLine{}
	for _, c := range st.Currencies {
		byCur[c.Currency] = c
	}
	eur := byCur["EUR"]
	if eur.Payouts != 2 || eur.SettledMinor != 30000 || eur.SettledReceiveMinor != 9500 {
		t.Fatalf("EUR bucket: %+v", eur)
	}
	usd := byCur["USD"]
	if usd.Payouts != 1 || usd.PendingMinor != 7000 || usd.SettledMinor != 0 {
		t.Fatalf("USD bucket: %+v", usd)
	}
	if len(st.Batches) != 1 || st.Batches[0].State != model.BatchSettled {
		t.Fatalf("batches: %+v", st.Batches)
	}
	// Cross-partner isolation: another partner sees an empty statement.
	other := vettedPartner(t, s, tenantID, "MANUAL")
	st2, err := s.Settlements.Statement(ctx, tenantID, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(st2.Currencies) != 0 || len(st2.Batches) != 0 {
		t.Fatalf("cross-partner leakage: %+v", st2)
	}
}

// Batch quote expiry: same rule as single payouts — no riding stale locks.
func TestSettlementBatchQuoteExpiry48(t *testing.T) {
	adapter := fakeMojaloop(t)
	defer adapter.Close()
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	s := testServices(t, Deps{MojaloopURL: adapter.URL, PublicBase: "http://core",
		Now: func() time.Time { return now }})
	tenantID := seedTenant(t, s)
	ctx := context.Background()
	partner := vettedPartner(t, s, tenantID, "MOJALOOP")

	for i, amt := range []int64{10000, 10000} {
		if _, err := s.Payouts.Create(ctx, tenantID, partner.ID, "b-ex"+string(rune('a'+i)), amt, "EUR", "po-ex48-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	b, err := s.Settlements.Assemble(ctx, tenantID, partner.ID, "EUR", "asm-ex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Settlements.QuoteBatch(ctx, tenantID, b.ID); err != nil {
		t.Fatal(err)
	}
	now = time.Date(2030, 6, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.Settlements.ExecuteBatch(ctx, tenantID, b.ID); err == nil {
		t.Fatal("executed batch against an expired quote")
	}
}
