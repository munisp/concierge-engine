package domain

// Phase 37: bulk lead import — greenfield bootstrap over the Capture path.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestLeadImportMixed(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	res, err := s.Leads.Import(ctx, tenantID, []CaptureLeadInput{
		{Kind: "person", Name: "Ada", Phone: "+971 50 111 0001", ConsentMarketing: true, Channel: "crm"},
		{Kind: "corporate", Name: "Rui", Company: "Limo Co", Email: "rui@limo.example", Channel: "crm"},
		{Kind: "person", Name: "Ada Again", Phone: "+971501110001"}, // dup of row 0
		{Kind: "person", Name: "Nobody"},                          // no contact anchor
		{Kind: "corporate", Name: "NoCo", Phone: "+971501110009"}, // missing company
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 5 || res.Created != 2 || res.Deduped != 1 || res.Rejected != 2 {
		t.Fatalf("receipt: %+v", res)
	}
	if len(res.LeadIDs) != 3 {
		t.Fatalf("lead ids: %v", res.LeadIDs)
	}
	if len(res.Errors) != 2 || res.Errors[0].Index != 3 || res.Errors[1].Index != 4 {
		t.Fatalf("row errors: %+v", res.Errors)
	}
	// Source defaults to import so the funnel separates bootstrapped demand.
	leads, _ := s.Leads.List(ctx, tenantID, "", "", 10)
	for _, l := range leads {
		if l.Source != "import" {
			t.Fatalf("source: %+v", l)
		}
	}
	// Funnel: one batch event + per-row lead_captured (created rows only).
	funnel, _ := s.Store.Attribution().Funnel(ctx, tenantID)
	var batch, captured int
	for _, row := range funnel {
		if row.Kind == "lead_imported" && row.Channel == "import" {
			batch = row.Count
		}
		if row.Kind == "lead_captured" {
			captured += row.Count
		}
	}
	if batch != 1 || captured != 2 {
		t.Fatalf("funnel: batch=%d captured=%d (%+v)", batch, captured, funnel)
	}
	// Consent mirrored grant-only onto the resolved client.
	first, err := s.Leads.Get(ctx, tenantID, res.LeadIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.Store.Clients().Get(ctx, tenantID, first.ClientID)
	if c.Consent["marketing"] != true {
		t.Fatalf("consent: %v", c.Consent)
	}
}

func TestLeadImportKeyedReplay(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	rows := []CaptureLeadInput{
		{Kind: "person", Name: "A", Phone: "+971501110020", IdempotencyKey: "crm-2027-01:0"},
		{Kind: "person", Name: "B", Phone: "+971501110021", IdempotencyKey: "crm-2027-01:1"},
	}
	first, err := s.Leads.Import(ctx, tenantID, rows)
	if err != nil {
		t.Fatal(err)
	}
	if first.Created != 2 {
		t.Fatalf("first import: %+v", first)
	}
	// Re-importing the same keyed export is replay-safe: nothing duplicated.
	second, err := s.Leads.Import(ctx, tenantID, rows)
	if err != nil {
		t.Fatal(err)
	}
	if second.Created != 0 || second.Deduped != 2 {
		t.Fatalf("replay: %+v", second)
	}
	leads, _ := s.Leads.List(ctx, tenantID, "", "", 10)
	if len(leads) != 2 {
		t.Fatalf("replay duplicated leads: %d", len(leads))
	}
}

func TestLeadImportBatchLimits(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	if _, err := s.Leads.Import(ctx, tenantID, nil); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("empty batch: %v", err)
	}
	rows := make([]CaptureLeadInput, MaxImportBatch+1)
	for i := range rows {
		rows[i] = CaptureLeadInput{Kind: "person", Name: "N", Phone: fmt.Sprintf("+9715077%05d", i)}
	}
	if _, err := s.Leads.Import(ctx, tenantID, rows); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("oversize batch: %v", err)
	}
	// Exactly at the cap is accepted.
	ok, err := s.Leads.Import(ctx, tenantID, rows[:MaxImportBatch])
	if err != nil {
		t.Fatal(err)
	}
	if ok.Created != MaxImportBatch {
		t.Fatalf("at-cap import: %+v", ok)
	}
	pipe, _ := s.Leads.Pipeline(ctx, tenantID)
	if pipe.Total != MaxImportBatch || pipe.ByKind[model.LeadKindPerson] != MaxImportBatch {
		t.Fatalf("pipeline after import: %+v", pipe)
	}
}

func TestLeadImportScoresAndSegments(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	// Imported corporate leads score and join lead-kind segments like live
	// captures — an imported list is immediately broadcast-able.
	res, err := s.Leads.Import(ctx, tenantID, []CaptureLeadInput{
		{Kind: "corporate", Name: "L", Company: "Wayne", Phone: "+971501110030", ConsentMarketing: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	lead, _ := s.Leads.Get(ctx, tenantID, res.LeadIDs[0])
	if lead.Score == 0 || len(lead.ScoreReasons) == 0 {
		t.Fatalf("imported lead not scored: %+v", lead)
	}
	matched, err := s.SegmentClients(ctx, tenantID, model.SegmentRules{LeadKind: model.LeadKindCorporate})
	if err != nil || len(matched) != 1 || matched[0].ID != lead.ClientID {
		t.Fatalf("segment: %+v %v", matched, err)
	}
}
