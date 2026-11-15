package domain

// Phase 38: lead provenance — list_source + contact_basis, with marketing
// consent gated on the basis. A bought B2B list imports as human-outreach-
// only; consent is earned on a repeat touch, never claimed at import.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestImportBasisGovernance(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	res, err := s.Leads.Import(ctx, tenantID, []CaptureLeadInput{
		// Bought list, no basis declared, consent claimed: consent is
		// stripped with a notice, basis defaults to b2b_legitimate_interest.
		{Kind: "corporate", Name: "Buyer", Company: "Apex Racing", Phone: "+447700900001", ConsentMarketing: true, ListSource: "cognism-q1-2027"},
		// Partner-consented list keeps the grant.
		{Kind: "person", Name: "Guest", Phone: "+351961110002", ConsentMarketing: true, ContactBasis: model.ContactBasisPartnerConsent, ListSource: "apex-hotels-csv"},
		// Self-reported (booth sign-up sheet) keeps the grant.
		{Kind: "person", Name: "Fan", Email: "fan@example.com", ConsentMarketing: true, ContactBasis: model.ContactBasisSelfReported},
		// Unknown basis value: rejected with a reason, not coerced.
		{Kind: "person", Name: "Mystery", Phone: "+351961110003", ContactBasis: "bought_list"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 3 || res.Rejected != 1 {
		t.Fatalf("receipt: %+v", res)
	}
	if len(res.Notices) != 1 || res.Notices[0].Index != 0 ||
		!strings.Contains(res.Notices[0].Reason, "contact basis") {
		t.Fatalf("notices: %+v", res.Notices)
	}
	if len(res.Errors) != 1 || res.Errors[0].Index != 3 ||
		!strings.Contains(res.Errors[0].Reason, "contact basis") {
		t.Fatalf("errors: %+v", res.Errors)
	}

	// Row 0: imported, basis defaulted, consent stripped — and NOT mirrored
	// onto the messaging identity.
	bought, err := s.Leads.Get(ctx, tenantID, res.LeadIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if bought.ContactBasis != model.ContactBasisB2B || bought.ConsentMarketing {
		t.Fatalf("bought row should import consent-free on b2b basis: %+v", bought)
	}
	if bought.ListSource != "cognism-q1-2027" {
		t.Fatalf("list source: %+v", bought)
	}
	c, _ := s.Store.Clients().Get(ctx, tenantID, bought.ClientID)
	if c.Consent["marketing"] == true {
		t.Fatalf("b2b row must not grant marketing consent: %v", c.Consent)
	}

	// Row 1: partner consent survives the import end to end.
	partner, _ := s.Leads.Get(ctx, tenantID, res.LeadIDs[1])
	if partner.ContactBasis != model.ContactBasisPartnerConsent || !partner.ConsentMarketing {
		t.Fatalf("partner row: %+v", partner)
	}
	pc, _ := s.Store.Clients().Get(ctx, tenantID, partner.ClientID)
	if pc.Consent["marketing"] != true {
		t.Fatalf("partner consent should mirror: %v", pc.Consent)
	}
}

func TestCaptureBasisDefaultsAndValidation(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	// Organic inbound defaults to self_reported and may hold consent.
	organic, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "person", Name: "Walk In", Phone: "+971501110040", ConsentMarketing: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if organic.Lead.ContactBasis != model.ContactBasisSelfReported || !organic.Lead.ConsentMarketing {
		t.Fatalf("organic capture: %+v", organic.Lead)
	}

	// A direct capture cannot claim marketing consent on a B2B basis.
	_, err = s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "corporate", Name: "Cold", Company: "ColdCo", Phone: "+971501110041",
		ContactBasis: model.ContactBasisB2B, ConsentMarketing: true,
	})
	if !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("b2b+consent should 422: %v", err)
	}

	// Unknown basis values are refused.
	_, err = s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "person", Name: "Odd", Phone: "+971501110042", ContactBasis: "telepathy",
	})
	if !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("unknown basis should 422: %v", err)
	}

	// B2B basis without consent claims is fine — human outreach lane.
	ok, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "corporate", Name: "Cold2", Company: "ColdCo2", Phone: "+971501110043",
		ContactBasis: model.ContactBasisB2B, ListSource: "apollo-export",
	})
	if err != nil || ok.Lead.ContactBasis != model.ContactBasisB2B || ok.Lead.ListSource != "apollo-export" {
		t.Fatalf("b2b capture: %+v %v", ok.Lead, err)
	}
}

func TestRepeatTouchEarnsConsent(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	// A bought B2B row starts consent-free.
	res, err := s.Leads.Import(ctx, tenantID, []CaptureLeadInput{
		{Kind: "corporate", Name: "Buyer", Company: "Apex", Phone: "+447700900010", ListSource: "cognism"},
	})
	if err != nil {
		t.Fatal(err)
	}
	leadID := res.LeadIDs[0]
	before, _ := s.Leads.Get(ctx, tenantID, leadID)
	beforeScore := before.Score

	// Human outreach happens; the prospect replies "yes, send me offers".
	// The operator re-captures the same contact with consent — the repeat
	// touch dedups into the existing lead and the grant lands on the
	// messaging identity. Provenance (b2b basis, list source) is preserved.
	dup, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "corporate", Name: "Buyer", Company: "Apex", Phone: "+44 7700 900010",
		ConsentMarketing: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !dup.Deduped || dup.Lead.ID != leadID {
		t.Fatalf("repeat touch should dedup: %+v", dup)
	}
	if !dup.Lead.ConsentMarketing {
		t.Fatalf("consent earned on repeat touch must stick: %+v", dup.Lead)
	}
	if dup.Lead.ContactBasis != model.ContactBasisB2B || dup.Lead.ListSource != "cognism" {
		t.Fatalf("provenance must survive the grant: %+v", dup.Lead)
	}
	if dup.Lead.Score <= beforeScore {
		t.Fatalf("consent grant should re-score up: %d -> %d", beforeScore, dup.Lead.Score)
	}
	c, _ := s.Store.Clients().Get(ctx, tenantID, dup.Lead.ClientID)
	if c.Consent["marketing"] != true {
		t.Fatalf("client consent: %v", c.Consent)
	}
}

func TestSegmentContactBasis(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	b2b, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "corporate", Name: "Cold", Company: "ColdCo", Phone: "+971501110050",
		ContactBasis: model.ContactBasisB2B,
	})
	if err != nil {
		t.Fatal(err)
	}
	organic, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: "person", Name: "Warm", Phone: "+971501110051",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Basis-only segments pick the right messaging identities.
	b2bSeg, err := s.SegmentClients(ctx, tenantID, model.SegmentRules{ContactBasis: model.ContactBasisB2B})
	if err != nil || len(b2bSeg) != 1 || b2bSeg[0].ID != b2b.Lead.ClientID {
		t.Fatalf("b2b segment: %+v %v", b2bSeg, err)
	}
	selfSeg, err := s.SegmentClients(ctx, tenantID, model.SegmentRules{ContactBasis: model.ContactBasisSelfReported})
	if err != nil || len(selfSeg) != 1 || selfSeg[0].ID != organic.Lead.ClientID {
		t.Fatalf("self_reported segment: %+v %v", selfSeg, err)
	}
	// Basis composes with kind (AND semantics).
	combo, err := s.SegmentClients(ctx, tenantID, model.SegmentRules{
		LeadKind: model.LeadKindCorporate, ContactBasis: model.ContactBasisB2B,
	})
	if err != nil || len(combo) != 1 {
		t.Fatalf("combo segment: %+v %v", combo, err)
	}
	none, err := s.SegmentClients(ctx, tenantID, model.SegmentRules{
		LeadKind: model.LeadKindCorporate, ContactBasis: model.ContactBasisSelfReported,
	})
	if err != nil || len(none) != 0 {
		t.Fatalf("contradictory segment should be empty: %+v %v", none, err)
	}
	// Converted (non-open) leads leave the segment.
	if _, err := s.Leads.Convert(ctx, tenantID, b2b.Lead.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := s.SegmentClients(ctx, tenantID, model.SegmentRules{ContactBasis: model.ContactBasisB2B})
	if len(after) != 0 {
		t.Fatalf("converted lead should leave the open-lead segment: %+v", after)
	}
}
