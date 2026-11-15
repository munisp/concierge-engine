package domain

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// setLifecycleConfig applies a tenant lifecycle config for a test (Phase 36
// lead SLA lives here alongside the Phase 25 follow-up posture).
func setLifecycleConfig(t *testing.T, s *Services, tenantID string, cfg model.LifecycleConfig) {
	t.Helper()
	ctx := context.Background()
	tenant, err := s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	tenant.LifecycleConfig = cfg
	if err := s.Store.Tenants().UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}
}

func setAlertPhones(t *testing.T, s *Services, tenantID string, phones ...string) {
	t.Helper()
	ctx := context.Background()
	tenant, err := s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	tenant.OperatorConfig.AlertPhones = phones
	if err := s.Store.Tenants().UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}
}

func backdateLead(t *testing.T, s *Services, tenantID, id string, age time.Duration) {
	t.Helper()
	ctx := context.Background()
	l, err := s.Store.Leads().Get(ctx, tenantID, id)
	if err != nil {
		t.Fatal(err)
	}
	l.CreatedAt = s.Now().Add(-age)
	if err := s.Store.Leads().Update(ctx, l); err != nil {
		t.Fatal(err)
	}
}

func TestLeadCapturePerson(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	res, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "person", Name: "Ada Lovelace", Phone: "+971 50 000 0001",
		Email: "ADA@example.com", ConsentMarketing: true, Source: "site", Channel: "seo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deduped {
		t.Fatal("first capture must not dedupe")
	}
	lead := res.Lead
	if lead.Status != model.LeadStatusNew || lead.Kind != model.LeadKindPerson {
		t.Fatalf("lead: %+v", lead)
	}
	if lead.Phone != "+971500000001" || lead.Email != "ada@example.com" {
		t.Fatalf("contact not normalized: %+v", lead)
	}
	if lead.ClientID == "" {
		t.Fatal("messaging-identity client not linked")
	}
	if lead.Score == 0 || len(lead.ScoreReasons) == 0 {
		t.Fatalf("score: %d reasons %v", lead.Score, lead.ScoreReasons)
	}
	// Consent grant mirrored onto the client (grant-only).
	c, err := s.Store.Clients().Get(ctx, tenantID, lead.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Consent["marketing"] != true {
		t.Fatalf("consent: %v", c.Consent)
	}
	// Attribution: lead_captured feeds the funnel.
	funnel, err := s.Store.Attribution().Funnel(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range funnel {
		if row.Kind == "lead_captured" && row.Channel == "seo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("funnel missing lead_captured: %+v", funnel)
	}
}

func TestLeadCaptureValidation(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	cases := []CaptureLeadInput{
		{Kind: "alien", Name: "X", Phone: "+1"},     // unknown kind
		{Kind: "person", Phone: "+1"},               // no name
		{Kind: "person", Name: "X"},                 // no contact anchor
		{Kind: "corporate", Name: "X", Phone: "+1"}, // no company
		{Kind: "partner", Name: "X", Phone: "+1"},   // no company
		{Kind: "event", Name: "X", Phone: "+1"},     // no event anchor
	}
	for i, in := range cases {
		if _, err := s.Leads.Capture(ctx, tenantID, in); !errors.Is(err, httpx.ErrValidation) {
			t.Fatalf("case %d: got %v, want ErrValidation", i, err)
		}
	}
	// Event with any ONE anchor validates.
	if _, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "event", Name: "X", Phone: "+1", EventType: "wedding",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLeadCaptureDedup(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	first, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "corporate", Name: "Grace", Phone: "+971500000002", Company: "Globex",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Repeat inquiry: same phone (different formatting), richer brief.
	second, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "corporate", Name: "Grace H", Phone: "+971 50 000 0002",
		Email: "grace@globex.com", ReferralCode: "REF-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Deduped {
		t.Fatal("same phone on an open lead must dedupe")
	}
	if second.Lead.ID != first.Lead.ID {
		t.Fatalf("dedupe created a new lead: %s vs %s", second.Lead.ID, first.Lead.ID)
	}
	if second.Lead.Email != "grace@globex.com" || second.Lead.ReferralCode != "REF-1" {
		t.Fatalf("backfill lost: %+v", second.Lead)
	}
	if second.Lead.Company != "Globex" {
		t.Fatalf("existing facts overwritten: %+v", second.Lead)
	}
	if second.Lead.Score < first.Lead.Score {
		t.Fatalf("re-score went backwards: %d -> %d", first.Lead.Score, second.Lead.Score)
	}
	leads, err := s.Store.Leads().List(ctx, tenantID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(leads) != 1 {
		t.Fatalf("want 1 lead, got %d", len(leads))
	}
}

func TestLeadScoringDeterministic(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	date := s.Now().Add(10 * 24 * time.Hour) // within 45 days
	res, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "event", Name: "Big Day", Phone: "+971500000003", Email: "big@day.com",
		EventType: "wedding", EventDate: &date, Headcount: 60, Venue: "Burj Al Arab",
		ReferralCode: "REF-9", ConsentMarketing: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 20 event + 10 phone + 5 email + 10 (20+) + 5 (50+) + 10 (45d) + 5 venue + 10 referral + 5 consent = 80
	if res.Lead.Score != 80 {
		t.Fatalf("score %d, want 80; reasons %v", res.Lead.Score, res.Lead.ScoreReasons)
	}
	// Re-capture (dedupe) must reproduce the identical score — no drift.
	again, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "event", Name: "Big Day", Phone: "+971500000003", Email: "big@day.com",
		EventType: "wedding", EventDate: &date, Headcount: 60, Venue: "Burj Al Arab",
		ReferralCode: "REF-9",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !again.Deduped || again.Lead.Score != 80 {
		t.Fatalf("re-score drifted: %+v", again.Lead)
	}
}

func TestLeadPartnerVerticalOverlap(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	if _, err := s.Catalog.Add(ctx, &model.CatalogService{
		TenantID: tenantID, Vertical: "chauffeur", Name: "Chauffeur", BaseMinor: 9500, Currency: "EUR",
	}); err != nil {
		t.Fatal(err)
	}
	match, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "partner", Name: "Pat", Phone: "+971500000004", Company: "Limo Co",
		Verticals: []string{"Chauffeur"},
	})
	if err != nil {
		t.Fatal(err)
	}
	noMatch, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "partner", Name: "Sam", Phone: "+971500000005", Company: "Yacht Co",
		Verticals: []string{"yacht"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if match.Lead.Score <= noMatch.Lead.Score {
		t.Fatalf("overlap should outscore: %d vs %d", match.Lead.Score, noMatch.Lead.Score)
	}
}

func TestLeadTransitions(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	res, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "person", Name: "Nia", Phone: "+971500000006",
	})
	if err != nil {
		t.Fatal(err)
	}
	id := res.Lead.ID
	if _, err := s.Leads.Transition(ctx, tenantID, id, model.LeadStatusQualified, ""); err != nil {
		t.Fatal(err)
	}
	// Same-state is an idempotent no-op (safe console retries).
	l, err := s.Leads.Transition(ctx, tenantID, id, model.LeadStatusQualified, "")
	if err != nil || l.Status != model.LeadStatusQualified {
		t.Fatalf("idempotent same-state: %v %+v", err, l)
	}
	// Backwards is illegal.
	if _, err := s.Leads.Transition(ctx, tenantID, id, model.LeadStatusNew, ""); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("backwards: %v", err)
	}
	// CONVERTED only via Convert.
	if _, err := s.Leads.Transition(ctx, tenantID, id, model.LeadStatusConverted, ""); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("convert via transition: %v", err)
	}
	// LOST needs a reason.
	if _, err := s.Leads.Transition(ctx, tenantID, id, model.LeadStatusLost, ""); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("lost without reason: %v", err)
	}
	l, err = s.Leads.Transition(ctx, tenantID, id, model.LeadStatusLost, "chose competitor")
	if err != nil || l.Status != model.LeadStatusLost || l.LostReason == "" {
		t.Fatalf("lost: %v %+v", err, l)
	}
	// Terminal states are locked.
	if _, err := s.Leads.Transition(ctx, tenantID, id, model.LeadStatusContacted, ""); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("terminal locked: %v", err)
	}
	if _, err := s.Leads.Convert(ctx, tenantID, id); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("convert lost: %v", err)
	}
}

func TestLeadConvertKinds(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	// person -> converted client link
	person, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "person", Name: "Solo", Phone: "+971500000010",
	})
	if err != nil {
		t.Fatal(err)
	}
	pr, err := s.Leads.Convert(ctx, tenantID, person.Lead.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Lead.Status != model.LeadStatusConverted || pr.Lead.ConvertedClientID != person.Lead.ClientID {
		t.Fatalf("person convert: %+v", pr.Lead)
	}
	// Idempotent: re-convert returns the same links, no new records.
	again, err := s.Leads.Convert(ctx, tenantID, person.Lead.ID)
	if err != nil || again.Lead.ConvertedClientID != pr.Lead.ConvertedClientID {
		t.Fatalf("idempotent convert: %v %+v", err, again)
	}

	// corporate -> account + OrgID mirror on the contact client
	corp, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "corporate", Name: "Eve", Phone: "+971500000011", Company: "Initech",
		Email: "eve@initech.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	cr, err := s.Leads.Convert(ctx, tenantID, corp.Lead.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cr.Account == nil || cr.Account.Name != "Initech" {
		t.Fatalf("corporate convert: %+v", cr)
	}
	contact, err := s.Store.Clients().Get(ctx, tenantID, corp.Lead.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if contact.OrgID != cr.Account.ID {
		t.Fatalf("OrgID mirror: %q want %q", contact.OrgID, cr.Account.ID)
	}

	// partner -> PENDING partner in the vetting queue
	partner, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "partner", Name: "Paul", Phone: "+971500000012", Company: "Atlas Limo",
		Verticals: []string{"chauffeur"},
	})
	if err != nil {
		t.Fatal(err)
	}
	nr, err := s.Leads.Convert(ctx, tenantID, partner.Lead.ID)
	if err != nil {
		t.Fatal(err)
	}
	if nr.Partner == nil || nr.Partner.Status != "PENDING" || nr.Partner.Name != "Atlas Limo" {
		t.Fatalf("partner convert: %+v", nr)
	}

	// lead_converted attribution fired for each conversion
	funnel, err := s.Store.Attribution().Funnel(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	converted := 0
	for _, row := range funnel {
		if row.Kind == "lead_converted" {
			converted += row.Count
		}
	}
	if converted != 3 {
		t.Fatalf("lead_converted count %d, want 3", converted)
	}
}

func TestLeadPipeline(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	for i, kind := range []string{"person", "person", "corporate", "event"} {
		in := CaptureLeadInput{Kind: kind, Name: "L", Phone: "+97150000002" + string(rune('0'+i))}
		if kind == "corporate" {
			in.Company = "Acme"
		}
		if kind == "event" {
			in.EventType = "gala"
		}
		if _, err := s.Leads.Capture(ctx, tenantID, in); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := s.Leads.Pipeline(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Total != 4 || sum.Open != 4 {
		t.Fatalf("pipeline: %+v", sum)
	}
	if sum.ByKind["person"] != 2 || sum.ByKind["corporate"] != 1 || sum.ByKind["event"] != 1 {
		t.Fatalf("by kind: %+v", sum.ByKind)
	}
	if sum.ByStatus[model.LeadStatusNew] != 4 {
		t.Fatalf("by status: %+v", sum.ByStatus)
	}
}

func TestLeadSLASweep(t *testing.T) {
	calls := 0
	s := testServices(t, Deps{WhatsAppURL: waCountingStub(t, &calls).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	setLifecycleConfig(t, s, tenantID, model.LifecycleConfig{LeadSLAMinutes: 30})
	setAlertPhones(t, s, tenantID, "+971500000099")

	res, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "person", Name: "Slow", Phone: "+971500000030",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Fresh lead: inside the window, no alert.
	if n, err := s.Leads.SweepSLA(ctx, tenantID); err != nil || n != 0 || calls != 0 {
		t.Fatalf("fresh sweep: n=%d calls=%d err=%v", n, calls, err)
	}
	// Breach the window: alert fires once, stamped.
	backdateLead(t, s, tenantID, res.Lead.ID, 31*time.Minute)
	if n, err := s.Leads.SweepSLA(ctx, tenantID); err != nil || n != 1 || calls != 1 {
		t.Fatalf("breach sweep: n=%d calls=%d err=%v", n, calls, err)
	}
	lead, err := s.Leads.Get(ctx, tenantID, res.Lead.ID)
	if err != nil || lead.SLAAlertedAt == nil {
		t.Fatalf("stamp: %v %+v", err, lead)
	}
	// Replay-safe: the stamp suppresses re-alerts.
	if n, err := s.Leads.SweepSLA(ctx, tenantID); err != nil || n != 0 || calls != 1 {
		t.Fatalf("replay sweep: n=%d calls=%d err=%v", n, calls, err)
	}
	// Non-NEW leads never alert.
	res2, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "person", Name: "Fast", Phone: "+971500000031",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Leads.Transition(ctx, tenantID, res2.Lead.ID, model.LeadStatusContacted, ""); err != nil {
		t.Fatal(err)
	}
	backdateLead(t, s, tenantID, res2.Lead.ID, 2*time.Hour)
	if n, err := s.Leads.SweepSLA(ctx, tenantID); err != nil || n != 0 || calls != 1 {
		t.Fatalf("contacted sweep: n=%d calls=%d err=%v", n, calls, err)
	}
	// Negative disables the watch entirely.
	setLifecycleConfig(t, s, tenantID, model.LifecycleConfig{LeadSLAMinutes: -1})
	res3, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "person", Name: "Quiet", Phone: "+971500000032",
	})
	if err != nil {
		t.Fatal(err)
	}
	backdateLead(t, s, tenantID, res3.Lead.ID, 24*time.Hour)
	if n, err := s.Leads.SweepSLA(ctx, tenantID); err != nil || n != 0 || calls != 1 {
		t.Fatalf("disabled sweep: n=%d calls=%d err=%v", n, calls, err)
	}
}

func TestSegmentLeadKind(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	corp, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "corporate", Name: "Corp", Phone: "+971500000040", Company: "Umbrella",
	})
	if err != nil {
		t.Fatal(err)
	}
	// A plain client with no lead, and a person-kind lead, must not match.
	outreachClient(t, s, tenantID, "+971500000041", false)
	if _, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "person", Name: "Per", Phone: "+971500000042",
	}); err != nil {
		t.Fatal(err)
	}
	matched, err := s.SegmentClients(ctx, tenantID, model.SegmentRules{LeadKind: model.LeadKindCorporate})
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 || matched[0].ID != corp.Lead.ClientID {
		t.Fatalf("lead-kind segment: %+v", matched)
	}
	// Converted leads close — the segment empties.
	if _, err := s.Leads.Convert(ctx, tenantID, corp.Lead.ID); err != nil {
		t.Fatal(err)
	}
	matched, err = s.SegmentClients(ctx, tenantID, model.SegmentRules{LeadKind: model.LeadKindCorporate})
	if err != nil || len(matched) != 0 {
		t.Fatalf("converted lead segment: %v %+v", err, matched)
	}
}

func TestSequenceSegmentGating(t *testing.T) {
	calls := 0
	s := testServices(t, Deps{WhatsAppURL: waCountingStub(t, &calls).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	setOutreachConfig(t, s, tenantID, model.OutreachConfig{
		Sequences: []model.Sequence{{
			Name:    "corp-nurture",
			Steps:   []model.SequenceStep{{AfterHours: 0, Body: "Hello {{client_name}}"}},
			Segment: &model.SegmentRules{LeadKind: model.LeadKindCorporate},
		}},
	})
	// One corporate lead (consented) and one plain consented client.
	if _, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "corporate", Name: "Corp", Phone: "+971500000050", Company: "Wayne",
		ConsentMarketing: true,
	}); err != nil {
		t.Fatal(err)
	}
	outreachClient(t, s, tenantID, "+971500000051", true)

	res, err := s.Sequences.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 1 {
		t.Fatalf("segment-gated run: %+v (calls %d)", res, calls)
	}
	sends, err := s.Store.Outreach().ListSends(ctx, tenantID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range sends {
		if !strings.HasPrefix(o.IdempotencyKey, "seq:corp-nurture:") {
			continue
		}
		// The single send must belong to the lead-linked client.
		leads, _ := s.Store.Leads().List(ctx, tenantID, 10)
		if leads[0].ClientID != o.ClientID {
			t.Fatalf("send went to %s, want lead client %s", o.ClientID, leads[0].ClientID)
		}
	}
}
