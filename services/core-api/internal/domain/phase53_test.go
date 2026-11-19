// Phase 53 domain tests: visual campaign builder — typed sequence
// nodes (template/channel/subject), the engagement decision gate
// (stall without writing, fire on real engagement, terminal halt on
// timeout, halt blocks downstream progression), CRUD validation, and
// ledger-derived per-node progress.
package domain

import (
	"context"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func seqSends53(t *testing.T, s *Services, tenantID string) map[string]model.OutreachSend {
	t.Helper()
	sends, err := s.Store.Outreach().ListSends(context.Background(), tenantID, 1000)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]model.OutreachSend{}
	for _, o := range sends {
		out[o.IdempotencyKey] = o
	}
	return out
}

// The gate stalls honestly: no engagement → nothing sent, nothing
// recorded, and the next sweep re-evaluates (no backlog, no record).
func TestSequenceGateStallsWithoutEngagement53(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	s := testServices(t, Deps{Now: func() time.Time { return clock }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	setOutreachConfig(t, s, tenantID, model.OutreachConfig{
		Sequences: []model.Sequence{{
			Name: "gated",
			Steps: []model.SequenceStep{
				{AfterHours: 0, Body: "hello"},
				{AfterHours: 0, Body: "did you see?", RequireEngagement: true},
			},
		}},
	})
	cid := outreachClient(t, s, tenantID, "+971500000301", true)

	res, err := s.Sequences.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	// No channel adapter in the test harness — the send records as sent
	// or failed, but it MUST record exactly once (that record is what
	// the gate anchors on).
	if res.Sent+res.Failed != 1 {
		t.Fatalf("only step 0 should fire: %+v", res)
	}
	sends := seqSends53(t, s, tenantID)
	if _, ok := sends["seq:gated:0:"+cid]; !ok {
		t.Fatalf("step 0 record missing: %v", sends)
	}
	if _, ok := sends["seq:gated:1:"+cid]; ok {
		t.Fatal("gated step must NOT be recorded while waiting")
	}
	// Still stalled hours later — and no duplicate step-0 send.
	clock = now.Add(6 * time.Hour)
	res, _ = s.Sequences.Run(ctx, tenantID)
	if res.Sent+res.Held+res.Failed != 0 {
		t.Fatalf("gate must keep stalling without engagement: %+v", res)
	}
	if len(seqSends53(t, s, tenantID)) != 1 {
		t.Fatal("stall must not accumulate records")
	}
}

// Real engagement on the previous send opens the gate.
func TestSequenceGateFiresOnEngagement53(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	s := testServices(t, Deps{Now: func() time.Time { return clock }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	setOutreachConfig(t, s, tenantID, model.OutreachConfig{
		Sequences: []model.Sequence{{
			Name: "gated",
			Steps: []model.SequenceStep{
				{AfterHours: 0, Body: "hello"},
				{AfterHours: 0, Body: "did you see?", RequireEngagement: true},
			},
		}},
	})
	cid := outreachClient(t, s, tenantID, "+971500000302", true)
	if _, err := s.Sequences.Run(ctx, tenantID); err != nil {
		t.Fatal(err)
	}
	// The client opens the email AFTER the step-0 send.
	if err := s.Store.Attribution().Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "email_opened", ClientID: cid,
		Channel: model.OutreachChannelEmail, At: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	clock = now.Add(2 * time.Hour)
	res, err := s.Sequences.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent+res.Failed != 1 {
		t.Fatalf("engaged client should receive the gated step: %+v", res)
	}
	if _, ok := seqSends53(t, s, tenantID)["seq:gated:1:"+cid]; !ok {
		t.Fatal("gated step record missing after engagement")
	}
}

// Timeout without engagement writes a terminal halt marker — visible,
// never a silent drop — and the halt blocks all downstream steps.
func TestSequenceGateTimeoutHaltsAndBlocks53(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	s := testServices(t, Deps{Now: func() time.Time { return clock }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	setOutreachConfig(t, s, tenantID, model.OutreachConfig{
		Sequences: []model.Sequence{{
			Name: "gated",
			Steps: []model.SequenceStep{
				{AfterHours: 0, Body: "hello"},
				{AfterHours: 0, Body: "did you see?", RequireEngagement: true, EngagementTimeoutHours: 24},
				{AfterHours: 0, Body: "last call"},
			},
		}},
	})
	cid := outreachClient(t, s, tenantID, "+971500000303", true)
	if _, err := s.Sequences.Run(ctx, tenantID); err != nil {
		t.Fatal(err)
	}
	clock = now.Add(25 * time.Hour) // beyond the 24h engagement window
	res, err := s.Sequences.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Held != 1 || res.Sent != 0 {
		t.Fatalf("timeout should record exactly one halt: %+v", res)
	}
	sends := seqSends53(t, s, tenantID)
	halt, ok := sends["seq:gated:1:"+cid]
	if !ok || halt.Status != "held" || !strings.HasPrefix(halt.Detail, "sequence halted:") {
		t.Fatalf("halt marker missing or unlabeled: %+v", halt)
	}
	// Downstream step never fires — not this sweep, not ever after.
	if _, ok := sends["seq:gated:2:"+cid]; ok {
		t.Fatal("halt must block the next step in the same sweep")
	}
	clock = now.Add(72 * time.Hour)
	res, _ = s.Sequences.Run(ctx, tenantID)
	if res.Sent+res.Held+res.Failed != 0 {
		t.Fatalf("halted sequence stays halted: %+v", res)
	}
	if _, ok := seqSends53(t, s, tenantID)["seq:gated:2:"+cid]; ok {
		t.Fatal("halt must block downstream progression permanently")
	}
}

// Typed nodes: channel + subject + template ride the step into the send.
func TestSequenceTypedNodesPassthrough53(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	s := testServices(t, Deps{Now: func() time.Time { return clock }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	if err := s.Store.Templates().Upsert(ctx, &model.MessageTemplate{
		ID: "tpl-arrival", TenantID: tenantID,
		Name: "arrival", Purpose: "any", Body: "Welcome {{client_name}}!",
	}); err != nil {
		t.Fatal(err)
	}
	setOutreachConfig(t, s, tenantID, model.OutreachConfig{
		Sequences: []model.Sequence{{
			Name: "typed",
			Steps: []model.SequenceStep{
				{AfterHours: 0, TemplateID: "tpl-arrival", Channel: model.OutreachChannelEmail,
					Subject: "Your arrival", Label: "arrival email"},
			},
		}},
	})
	rr, err := s.Clients.Resolve(ctx, tenantID, "", "vip@example.com", "Email Client")
	if err != nil {
		t.Fatal(err)
	}
	cid := rr.Client.ID
	rr.Client.Consent = map[string]any{"marketing": true}
	if err := s.Store.Clients().Upsert(ctx, rr.Client); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sequences.Run(ctx, tenantID); err != nil {
		t.Fatal(err)
	}
	o, ok := seqSends53(t, s, tenantID)["seq:typed:0:"+cid]
	if !ok {
		t.Fatal("typed step send missing")
	}
	if o.Channel != model.OutreachChannelEmail || o.Subject != "Your arrival" {
		t.Fatalf("channel/subject must ride the step: %+v", o)
	}
	if !strings.Contains(o.Body, "Welcome") {
		t.Fatalf("template body must be rendered and recorded: %q", o.Body)
	}
}

// Progress: the canvas counters come from the same ledger the sweep
// acts on — tallies per node, waiting audiences, halt visibility.
func TestSequenceProgressDerivedFromLedger53(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	s := testServices(t, Deps{Now: func() time.Time { return clock }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	setOutreachConfig(t, s, tenantID, model.OutreachConfig{
		Sequences: []model.Sequence{{
			Name: "prog",
			Steps: []model.SequenceStep{
				{AfterHours: 0, Body: "a"},
				{AfterHours: 0, Body: "b", RequireEngagement: true, EngagementTimeoutHours: 24},
			},
		}},
	})
	outreachClient(t, s, tenantID, "+971500000305", true) // A: never engages
	cidB := outreachClient(t, s, tenantID, "+971500000306", true)
	if _, err := s.Sequences.Run(ctx, tenantID); err != nil {
		t.Fatal(err)
	}
	// B engages; A times out into a halt.
	if err := s.Store.Attribution().Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "email_clicked", ClientID: cidB,
		At: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	clock = now.Add(25 * time.Hour)
	if _, err := s.Sequences.Run(ctx, tenantID); err != nil {
		t.Fatal(err)
	}

	rows, err := s.Sequences.Progress(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("one sequence expected: %+v", rows)
	}
	p := rows[0]
	if p.Enrolled != 2 {
		t.Fatalf("enrolled = 2 clients, got %d", p.Enrolled)
	}
	if len(p.Steps) != 2 {
		t.Fatalf("two nodes expected: %+v", p.Steps)
	}
	if p.Steps[0].Sent+p.Steps[0].Failed != 2 {
		t.Fatalf("step 0 fired=2, got %+v", p.Steps[0])
	}
	if p.Steps[1].Sent+p.Steps[1].Failed != 1 || p.Steps[1].Halted != 1 || p.Steps[1].Held != 1 {
		t.Fatalf("step 1: one fired (B), one halted (A): %+v", p.Steps[1])
	}
	if p.Steps[1].Waiting != 0 {
		t.Fatalf("nobody left waiting — B fired, A halted: %+v", p.Steps[1])
	}
}

// CRUD: validation before persistence, name-keyed upsert, delete with
// honest not-found, and a resume-safe re-create.
func TestSequenceCRUDValidation53(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s := testServices(t, Deps{Now: func() time.Time { return now }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	bad := []model.Sequence{
		{Name: "bad:name", Steps: []model.SequenceStep{{Body: "x"}}},
		{Name: "empty"},
		{Name: "gate0", Steps: []model.SequenceStep{{Body: "x", RequireEngagement: true}}},
		{Name: "mail", Steps: []model.SequenceStep{{Body: "x", Channel: model.OutreachChannelEmail}}},
		{Name: "nocontent", Steps: []model.SequenceStep{{AfterHours: 1}}},
	}
	for _, seq := range bad {
		if err := s.Sequences.UpsertSequence(ctx, tenantID, seq); err == nil {
			t.Fatalf("sequence %q must fail validation", seq.Name)
		}
	}
	seq := model.Sequence{
		Name: "welcome",
		Steps: []model.SequenceStep{
			{AfterHours: 1, Body: "hi", Label: "intro"},
			{AfterHours: 24, Body: "follow up", RequireEngagement: true, EngagementTimeoutHours: 48},
		},
	}
	if err := s.Sequences.UpsertSequence(ctx, tenantID, seq); err != nil {
		t.Fatalf("valid sequence upserts: %v", err)
	}
	// Replace-by-name: two steps become one, still one sequence.
	seq.Steps = seq.Steps[:1]
	if err := s.Sequences.UpsertSequence(ctx, tenantID, seq); err != nil {
		t.Fatal(err)
	}
	tenant, _ := s.Tenants.Get(ctx, tenantID)
	if len(tenant.OutreachConfig.Sequences) != 1 || len(tenant.OutreachConfig.Sequences[0].Steps) != 1 {
		t.Fatalf("upsert must replace by name: %+v", tenant.OutreachConfig.Sequences)
	}
	if err := s.Sequences.DeleteSequence(ctx, tenantID, "missing"); err != httpx.ErrNotFound {
		t.Fatalf("deleting a missing sequence = ErrNotFound, got %v", err)
	}
	if err := s.Sequences.DeleteSequence(ctx, tenantID, "welcome"); err != nil {
		t.Fatal(err)
	}
	tenant, _ = s.Tenants.Get(ctx, tenantID)
	if len(tenant.OutreachConfig.Sequences) != 0 {
		t.Fatal("delete must remove the sequence")
	}
}
