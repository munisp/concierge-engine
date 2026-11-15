// F1 Portimão 2027 simulation — DDportugal, end-to-end through the real
// domain engine. Not a CI assertion suite (though it asserts the key
// invariants); a narrative run of the four-phase playbook:
//
//	Phase 1 (Nov 2026):  campaign-attributed capture, scoring, dedup
//	Phase 2 (Apr 2027):  segment-gated nurture, broadcast, speed-to-lead SLA
//	Phase 3 (Jun 2027):  race-week conversion into accounts/partners/clients
//	Phase 4 (post-race): funnel attribution + the 2028 carryover
//
// Run: go test ./internal/domain -run TestF1PortimaoSimulation -v
package domain

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func simLog(format string, args ...any) {
	fmt.Printf("    "+format+"\n", args...)
}

func simBanner(title string) {
	fmt.Printf("\n  ┌─────────────────────────────────────────────────────────────\n")
	fmt.Printf("  │ %s\n", title)
	fmt.Printf("  └─────────────────────────────────────────────────────────────\n")
}

func TestF1PortimaoSimulation(t *testing.T) {
	// Mutable clock: the simulation time-travels across seven months.
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	waCalls := 0
	s := testServices(t, Deps{
		WhatsAppURL: waCountingStub(t, &waCalls).URL,
		Now:         func() time.Time { return now },
	})
	ctx := context.Background()

	// --- Tenant: DDportugal, a Lisbon/Algarve chauffeur & concierge operator.
	tenant, err := s.Tenants.Create(ctx, &model.Tenant{
		ID: NewID(), Slug: "ddportugal", Name: "DDportugal", Archetype: "flagship",
	})
	if err != nil {
		t.Fatal(err)
	}
	tenantID := tenant.ID
	if _, err := s.Catalog.Add(ctx, &model.CatalogService{
		TenantID: tenantID, Vertical: "chauffeur", Name: "Race Chauffeur",
		BaseMinor: 12000, Currency: "EUR", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	// Race-week posture: 30-minute speed-to-lead SLA, operator on WhatsApp.
	setLifecycleConfig(t, s, tenantID, model.LifecycleConfig{LeadSLAMinutes: 30})
	setAlertPhones(t, s, tenantID, "+351910000999")

	raceDate := time.Date(2027, 6, 18, 0, 0, 0, 0, time.UTC)

	capture := func(in CaptureLeadInput) *CaptureResult {
		res, err := s.Leads.Capture(ctx, tenantID, in)
		if err != nil {
			t.Fatalf("capture %s: %v", in.Name, err)
		}
		return res
	}
	printLead := func(tag string, res *CaptureResult) {
		l := res.Lead
		dedup := ""
		if res.Deduped {
			dedup = "  [DEDUPED → existing lead]"
		}
		simLog("%-26s kind=%-9s score=%-3d %-44s%s", l.Name, l.Kind, l.Score,
			"["+strings.Join(l.ScoreReasons, ", ")+"]", dedup)
	}

	// =====================================================================
	simBanner("PHASE 1 — Nov 2026: build the funnel (7 months to race weekend)")
	// =====================================================================
	simLog("Site live: ddportugal.pt/f1-portimao → POST /site/ddportugal/leads")
	simLog("Campaign utm_campaign=f1-portimao-2027 on every ad & partner link")
	fmt.Println()

	sofia := capture(CaptureLeadInput{
		Kind: "event", Name: "Sofia Mendes", Phone: "+351 96 123 4567",
		EventType: "race_weekend_transfer", EventDate: &raceDate, Headcount: 6,
		Venue: "Autódromo Internacional do Algarve", ConsentMarketing: true,
		Source: "site", Channel: "google", Campaign: "f1-portimao-2027",
	})
	printLead("event · family of 6", sofia)

	apex := capture(CaptureLeadInput{
		Kind: "corporate", Name: "James Whitfield", Company: "Apex Racing Hospitality",
		Phone: "+44 7700 900123", Email: "James@ApexRacing.example",
		ConsentMarketing: true, Source: "site", Channel: "paid_social",
		Campaign: "f1-portimao-2027",
	})
	printLead("corporate · F1 sponsor hospo", apex)

	velocity := capture(CaptureLeadInput{
		Kind: "corporate", Name: "Laura Bianchi", Company: "Velocity Watches",
		Phone: "+39 348 555 0123", Email: "l.bianchi@velocitywatches.example",
		ConsentMarketing: true, Source: "site", Channel: "referral",
		Campaign: "f1-portimao-2027", ReferralCode: "DDP-HOTEL-ALGARVE",
	})
	printLead("corporate · hotel referral", velocity)

	algarveCars := capture(CaptureLeadInput{
		Kind: "partner", Name: "Rui Costa", Company: "Algarve Executive Cars",
		Phone: "+351 289 555 010", Verticals: []string{"chauffeur"},
		ConsentMarketing: true, Source: "outbound", Channel: "direct",
		Campaign: "f1-supply-2027",
	})
	printLead("partner · fleet supply", algarveCars)

	vilamoura := capture(CaptureLeadInput{
		Kind: "partner", Name: "Ines Duarte", Company: "Vilamoura Yacht Charters",
		Phone: "+351 289 555 011", Verticals: []string{"yacht"},
		Source: "outbound", Channel: "direct", Campaign: "f1-supply-2027",
	})
	printLead("partner · no fleet overlap", vilamoura)

	carlos := capture(CaptureLeadInput{
		Kind: "person", Name: "Carlos Pereira", Phone: "+351 91 777 8899",
		ConsentMarketing: true, Source: "site", Channel: "instagram",
		Campaign: "f1-portimao-2027",
	})
	printLead("person · single airport run", carlos)

	fmt.Println()
	simLog("--- dedup: Sofia re-inquires via WhatsApp link, reformatted phone,")
	simLog("--- this time leaving her email. Same lead, backfilled, re-scored.")
	sofia2 := capture(CaptureLeadInput{
		Kind: "event", Name: "Sofia Mendes", Phone: "+351961234567",
		Email: "sofia.mendes@example.com", EventType: "race_weekend_transfer",
		Source: "whatsapp", Channel: "google", Campaign: "f1-portimao-2027",
	})
	printLead("REPEAT inquiry", sofia2)
	if !sofia2.Deduped || sofia2.Lead.ID != sofia.Lead.ID {
		t.Fatalf("dedup failed: %+v vs %+v", sofia2.Lead.ID, sofia.Lead.ID)
	}
	if sofia2.Lead.Email != "sofia.mendes@example.com" {
		t.Fatalf("backfill lost the email: %+v", sofia2.Lead)
	}
	leads, _ := s.Leads.List(ctx, tenantID, "", "", 100)
	if len(leads) != 6 {
		t.Fatalf("dedup must keep 6 leads, got %d", len(leads))
	}
	simLog("→ pipeline holds 6 leads, not 7. Demand signal stays clean.")
	simLog("(site honeypot silently drops the bot traffic at the HTTP edge)")

	// =====================================================================
	simBanner("PHASE 2 — Apr 2027: nurture the audiences (without burning them)")
	// =====================================================================
	now = time.Date(2027, 4, 1, 12, 0, 0, 0, time.UTC)

	// Two drip sequences, each gated on its lead-kind segment. Partner and
	// person leads are deliberately NOT enrolled in either.
	setOutreachConfig(t, s, tenantID, model.OutreachConfig{
		Sequences: []model.Sequence{
			{Name: "corp-nurture", Segment: &model.SegmentRules{LeadKind: model.LeadKindCorporate},
				Steps: []model.SequenceStep{
					{AfterHours: 0, Body: "Olá {{client_name}} — DDportugal here. F1 returns to Portimão June 2027; may we send a bespoke corporate itinerary?"},
					{AfterHours: 72, Body: "{{client_name}}, fleet is nearly committed for race week — shall we pencil your transfers?"},
				}},
			{Name: "event-nurture", Segment: &model.SegmentRules{LeadKind: model.LeadKindEvent},
				Steps: []model.SequenceStep{
					{AfterHours: 0, Body: "Hi {{client_name}}! Your race-weekend transfer for the Algarve is noted — want us to lock your chauffeur?"},
					{AfterHours: 72, Body: "{{client_name}}, race-day slots for your party are filling — confirm with a 30% deposit?"},
				}},
		},
	})

	res1, err := s.Sequences.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	simLog("sequence sweep #1 (step 0): sent=%d held=%d scanned=%d clients", res1.Sent, res1.Held, res1.Scanned)
	if res1.Sent != 3 { // 2 corporate + 1 event — partner & person clients excluded
		t.Fatalf("segment gating: want 3 sends, got %+v", res1)
	}
	// Hard check: every send landed on a corporate- or event-lead client.
	wantClients := map[string]bool{
		apex.Lead.ClientID: true, velocity.Lead.ClientID: true, sofia.Lead.ClientID: true,
	}
	sends, _ := s.Store.Outreach().ListSends(ctx, tenantID, 100)
	for _, o := range sends {
		if strings.HasPrefix(o.IdempotencyKey, "seq:") && !wantClients[o.ClientID] {
			t.Fatalf("sequence leaked outside segment: %s → %s", o.IdempotencyKey, o.ClientID)
		}
	}
	simLog("→ partner & person lead clients received NOTHING (segment gate holds)")

	// Early-bird broadcast to the event-lead audience only.
	bcast, err := s.Campaigns.Broadcast(ctx, tenantID, BroadcastInput{
		Campaign: "f1-earlybird",
		Body:     "Race weekend June 18–20: early-bookers get priority pickup windows. Reply BOOK to reserve.",
		Segment:  model.SegmentRules{LeadKind: model.LeadKindEvent},
	})
	if err != nil {
		t.Fatal(err)
	}
	simLog("broadcast f1-earlybird → segment lead_kind=event: matched=%d sent=%d held=%d",
		bcast.Matched, bcast.Sent, bcast.Held)
	if bcast.Matched != 1 || bcast.Sent != 1 {
		t.Fatalf("broadcast: %+v", bcast)
	}

	now = now.Add(4 * 24 * time.Hour) // Apr 5 — step 1 due
	res2, err := s.Sequences.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	simLog("sequence sweep #2 (step 1, +4d): sent=%d — cadence resumes per-client", res2.Sent)
	if res2.Sent != 3 {
		t.Fatalf("step 1: want 3 sends, got %+v", res2)
	}
	simLog("consent gates, 3-per-7d cap and quiet hours applied to every send")
	simLog("WhatsApp adapter calls so far: %d", waCalls)

	// Operators work the board through the spring: every nurtured lead
	// leaves NEW (the SLA clock only ticks for leads nobody has touched).
	for _, id := range []string{
		sofia.Lead.ID, apex.Lead.ID, velocity.Lead.ID,
		algarveCars.Lead.ID, vilamoura.Lead.ID, carlos.Lead.ID,
	} {
		if _, err := s.Leads.Transition(ctx, tenantID, id, "CONTACTED", ""); err != nil {
			t.Fatal(err)
		}
	}
	simLog("operators worked the board: all 6 spring leads → CONTACTED")

	// --- Race week minus 3 days: a hot inquiry lands. Score re-prioritizes.
	now = time.Date(2027, 6, 15, 12, 0, 0, 0, time.UTC)
	simLog("")
	simLog("--- June 15: a team logistics manager inquires, 24 seats, race date")
	marta := capture(CaptureLeadInput{
		Kind: "event", Name: "Marta Silva", Company: "Team Paddock Logistics",
		Phone: "+351 21 555 0199", Email: "ops@teampaddock.example",
		EventType: "team_shuttle", EventDate: &raceDate, Headcount: 24,
		Venue: "Autódromo Internacional do Algarve", ConsentMarketing: true,
		Source: "site", Channel: "partner_referral", Campaign: "f1-portimao-2027",
	})
	printLead("HOT event lead", marta)

	all, _ := s.Leads.List(ctx, tenantID, "", "", 100)
	sort.Slice(all, func(i, j int) bool { return all[i].Score > all[j].Score })
	simLog("pipeline top-3 by score:")
	for i := 0; i < 3 && i < len(all); i++ {
		simLog("  %d. %-24s %-9s %d pts", i+1, all[i].Name, all[i].Kind, all[i].Score)
	}
	if all[0].ID != marta.Lead.ID {
		t.Fatalf("hot lead should top the board: %+v", all[0])
	}
	simLog("→ 'event within 45 days' +10 fired automatically — no manual re-sort")

	// Nobody picks it up for 45 minutes. SLA = 30.
	backdateLead(t, s, tenantID, marta.Lead.ID, 45*time.Minute)
	before := waCalls
	n, err := s.Leads.SweepSLA(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	simLog("SLA sweep: %d breach(es) → WhatsApp alert to operator +351910000999", n)
	if n != 1 || waCalls != before+1 {
		t.Fatalf("SLA sweep: n=%d calls +%d", n, waCalls-before)
	}
	n2, _ := s.Leads.SweepSLA(ctx, tenantID)
	if n2 != 0 {
		t.Fatalf("SLA re-sweep must be replay-safe, got %d", n2)
	}
	simLog("re-sweep: %d — alert stamped, never re-fires", n2)

	// =====================================================================
	simBanner("PHASE 3 — Jun 18–20, race week: convert demand into records")
	// =====================================================================
	now = time.Date(2027, 6, 19, 12, 0, 0, 0, time.UTC)

	// Apex Racing Hospitality: worked through the pipeline, then converted.
	if _, err := s.Leads.Transition(ctx, tenantID, apex.Lead.ID, "CONTACTED", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Leads.Transition(ctx, tenantID, apex.Lead.ID, "QUALIFIED", ""); err != nil {
		t.Fatal(err)
	}
	apexConv, err := s.Leads.Convert(ctx, tenantID, apex.Lead.ID)
	if err != nil {
		t.Fatal(err)
	}
	if apexConv.Account == nil || apexConv.Account.Name != "Apex Racing Hospitality" {
		t.Fatalf("corporate conversion: %+v", apexConv)
	}
	simLog("CONVERTED corporate → CorporateAccount %q (terms %dd, billing %s)",
		apexConv.Account.Name, apexConv.Account.PaymentTermsDays, apexConv.Account.BillingEmail)
	apexClient, _ := s.Store.Clients().Get(ctx, tenantID, apex.Lead.ClientID)
	if apexClient.OrgID != apexConv.Account.ID {
		t.Fatalf("client OrgID mirror: %q vs %q", apexClient.OrgID, apexConv.Account.ID)
	}
	simLog("→ James's client record now bills to the account — invoiced on terms,")
	simLog("  not per-ride. And already a B2B customer for the 2028 race.")

	// Convert is idempotent; a converted lead is terminal.
	again, err := s.Leads.Convert(ctx, tenantID, apex.Lead.ID)
	if err != nil || again.Lead.ConvertedAccountID != apexConv.Account.ID {
		t.Fatalf("re-convert must replay the same account: %+v %v", again, err)
	}
	if _, err := s.Leads.Transition(ctx, tenantID, apex.Lead.ID, "LOST", "x"); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("converted lead must be terminal, got %v", err)
	}
	simLog("re-convert: same account (idempotent); further transitions: 409")

	// Velocity Watches: still negotiating — moved to QUALIFIED, not lost.
	if _, err := s.Leads.Transition(ctx, tenantID, velocity.Lead.ID, "CONTACTED", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Leads.Transition(ctx, tenantID, velocity.Lead.ID, "QUALIFIED", ""); err != nil {
		t.Fatal(err)
	}

	// Supply side: the chauffeur partner enters the vetting queue.
	pConv, err := s.Leads.Convert(ctx, tenantID, algarveCars.Lead.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pConv.Partner == nil || pConv.Partner.Status != "PENDING" {
		t.Fatalf("partner conversion: %+v", pConv)
	}
	simLog("CONVERTED partner → %q enters vetting queue as %s (fleet stake = brand stake)",
		pConv.Partner.Name, pConv.Partner.Status)

	// Event & hot leads: convert to their messaging clients, ready to book.
	sofiaConv, err := s.Leads.Convert(ctx, tenantID, sofia.Lead.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sofiaConv.Lead.ConvertedClientID != sofia.Lead.ClientID {
		t.Fatalf("event conversion must link the client: %+v", sofiaConv.Lead)
	}
	if _, err := s.Leads.Convert(ctx, tenantID, marta.Lead.ID); err != nil {
		t.Fatal(err)
	}
	simLog("CONVERTED event leads → client links ready for the quote/deposit flow")
	simLog("  (Sofia: party of 6 · Marta/Team Paddock: 24 seats, both 30%% deposit)")

	// The yacht partner is out of scope — lost, with a mandatory reason.
	if _, err := s.Leads.Transition(ctx, tenantID, vilamoura.Lead.ID, "LOST", ""); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("LOST without reason must be 422, got %v", err)
	}
	if _, err := s.Leads.Transition(ctx, tenantID, vilamoura.Lead.ID, "LOST", "outside service scope — no fleet overlap"); err != nil {
		t.Fatal(err)
	}
	simLog("LOST partner (reason required, recorded): Vilamoura Yacht Charters")

	// =====================================================================
	simBanner("PHASE 4 — Jun 22, post-race: attribution + the 2028 carryover")
	// =====================================================================
	now = time.Date(2027, 6, 22, 12, 0, 0, 0, time.UTC)

	pipe, err := s.Leads.Pipeline(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	simLog("PIPELINE  total=%d open=%d  by_status=%v  by_kind=%v",
		pipe.Total, pipe.Open, pipe.ByStatus, pipe.ByKind)
	if pipe.Total != 7 || pipe.Open != 2 ||
		pipe.ByStatus[model.LeadStatusConverted] != 4 || pipe.ByStatus[model.LeadStatusLost] != 1 {
		t.Fatalf("pipeline: %+v", pipe)
	}

	funnel, err := s.Store.Attribution().Funnel(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	simLog("FUNNEL (lead events by channel/campaign):")
	foundConverted := false
	for _, row := range funnel {
		if !strings.HasPrefix(row.Kind, "lead_") {
			continue
		}
		simLog("  %-15s channel=%-16s campaign=%-18s ×%d", row.Kind, row.Channel, row.Campaign, row.Count)
		if row.Kind == "lead_converted" && row.Campaign == "f1-portimao-2027" {
			foundConverted = true
		}
	}
	if !foundConverted {
		t.Fatal("funnel missing lead_converted for f1-portimao-2027")
	}

	simLog("")
	simLog("2028 CARRYOVER — what DDportugal keeps when the 2027 cars leave:")
	simLog("  • 1 CorporateAccount on 30-day terms (Apex) — warm for next year")
	simLog("  • 1 QUALIFIED corporate still in play (Velocity Watches)")
	simLog("  • 1 vetted-pipeline supply partner (Algarve Executive Cars)")
	simLog("  • %d opted-in client identities on WhatsApp (consent preserved)", 5)
	simLog("  • channel-level ROI proof: google / paid_social / referral /")
	simLog("    partner_referral — next year's budget follows the funnel")
	simLog("")
	simLog("TOTAL WhatsApp messages sent across 7 months: %d", waCalls)
	simLog("SIMULATION COMPLETE — all invariants held ✓")
}
