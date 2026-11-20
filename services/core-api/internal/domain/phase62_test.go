package domain

// Phase 62: chauffeur matching — scoring, exclusion, explainability,
// rider links, and the recorded match decision.

import (
	"context"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func seedCrew62(t *testing.T, s *Services, tenantID, name string, langs, styles []string, quality float64) *model.CrewMember {
	t.Helper()
	c, err := s.Dispatch.AddCrewMember(context.Background(), &model.CrewMember{
		TenantID: tenantID, FullName: name, Languages: langs, Styles: styles,
		QualityScore: quality, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestChauffeurMatching62(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	res, _ := s.Clients.Resolve(ctx, tenantID, "+33612121212", "", " matching rider ")
	cid := res.Client.ID
	// Rider signals: French speaker, quiet rides.
	if _, err := s.Clients.SavePreference(ctx, tenantID, cid, "comms.language", "fr", "service", "", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Clients.SavePreference(ctx, tenantID, cid, "ride.conversation", "quiet", "service", "", "test"); err != nil {
		t.Fatal(err)
	}

	matchA := seedCrew62(t, s, tenantID, "Amadou Diallo", []string{"fr"}, []string{"quiet"}, 9)
	plain := seedCrew62(t, s, tenantID, "Bruno Silva", nil, nil, 5)
	fav := seedCrew62(t, s, tenantID, "Chinwe Okafor", nil, nil, 7)
	blocked := seedCrew62(t, s, tenantID, "Dmitri Petrov", []string{"fr"}, []string{"quiet"}, 10)

	if _, err := s.Dispatch.SetChauffeurLink(ctx, tenantID, cid, fav.ID, model.LinkFavorite); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch.SetChauffeurLink(ctx, tenantID, cid, blocked.ID, model.LinkBlocked); err != nil {
		t.Fatal(err)
	}

	bookingID := bookAt(t, s, tenantID, cid, "m62", time.Now().Add(48*time.Hour).Unix(), 2)
	ranked, err := s.Dispatch.SuggestCrew(ctx, tenantID, bookingID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) != 3 { // blocked excluded
		t.Fatalf("candidates: %+v", ranked)
	}
	// Favorite first, then language+style match, then plain.
	if ranked[0].Crew.ID != fav.ID || ranked[1].Crew.ID != matchA.ID || ranked[2].Crew.ID != plain.ID {
		t.Fatalf("ranking: %s %s %s", ranked[0].Crew.FullName, ranked[1].Crew.FullName, ranked[2].Crew.FullName)
	}
	if ranked[0].Score != 100+7 { // favorite + quality(7/10→7)
		t.Fatalf("favorite score %d", ranked[0].Score)
	}
	if ranked[1].Score != 30+20+9 { // language + style + quality
		t.Fatalf("match score %d", ranked[1].Score)
	}
	if ranked[2].Score != 5 {
		t.Fatalf("plain score %d", ranked[2].Score)
	}
	// Every candidate explains itself.
	for _, r := range ranked {
		if len(r.Reasons) == 0 {
			t.Fatalf("no reasons: %+v", r)
		}
	}

	// AssignMatched takes the favorite and records the decision.
	a, err := s.Dispatch.AssignMatched(ctx, tenantID, bookingID)
	if err != nil {
		t.Fatal(err)
	}
	if a.CrewMemberID != fav.ID || a.MatchScore != ranked[0].Score ||
		len(a.MatchReasons) == 0 || a.MatchReasons[0] != "rider's favorite chauffeur" {
		t.Fatalf("matched assignment: %+v", a)
	}
}

func TestMatchingFamiliarityAndOverride62(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	res, _ := s.Clients.Resolve(ctx, tenantID, "+33613131313", "", "regular rider")
	cid := res.Client.ID
	vet := seedCrew62(t, s, tenantID, "Ebele Nwosu", nil, nil, 8)

	// Two completed trips together → familiarity signal (+20, capped 30).
	for i, idem := range []string{"h1", "h2"} {
		bid := bookAt(t, s, tenantID, cid, idem, time.Now().Add(-time.Duration(72+24*i)*time.Hour).Unix(), 1)
		a, err := s.Dispatch.Assign(ctx, &model.Assignment{
			TenantID: tenantID, BookingID: bid, CrewMemberID: vet.ID})
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range []string{"EN_ROUTE_TO_PICKUP", "ARRIVED", "PASSENGER_ON_BOARD", "IN_TRIP", "COMPLETED_TRIP"} {
			if err := s.Dispatch.UpdateStatus(ctx, tenantID, a.ID, st); err != nil {
				t.Fatal(err)
			}
		}
	}

	bookingID := bookAt(t, s, tenantID, cid, "h3", time.Now().Add(48*time.Hour).Unix(), 1)
	ranked, err := s.Dispatch.SuggestCrew(ctx, tenantID, bookingID)
	if err != nil {
		t.Fatal(err)
	}
	if ranked[0].Score != 8+20 {
		t.Fatalf("familiarity score %d (reasons %v)", ranked[0].Score, ranked[0].Reasons)
	}
	found := false
	for _, r := range ranked[0].Reasons {
		if strings.Contains(r, "2 prior trips together") {
			found = true
		}
	}
	if !found {
		t.Fatalf("reasons: %v", ranked[0].Reasons)
	}

	// A blocked chauffeur assigned manually is recorded as an override —
	// the decision trail stays honest even when the operator overrides.
	if _, err := s.Dispatch.SetChauffeurLink(ctx, tenantID, cid, vet.ID, model.LinkBlocked); err != nil {
		t.Fatal(err)
	}
	a, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: bookingID, CrewMemberID: vet.ID})
	if err != nil {
		t.Fatal(err)
	}
	if a.MatchScore != -1 || !strings.Contains(a.MatchReasons[0], "manual override") {
		t.Fatalf("override record: %+v", a)
	}
}

func TestChauffeurLinks62(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	res, _ := s.Clients.Resolve(ctx, tenantID, "+33614141414", "", "link rider")
	cid := res.Client.ID
	c := seedCrew62(t, s, tenantID, "Femi Adebayo", nil, nil, 6)

	// Bad kind and unknown chauffeur rejected.
	if _, err := s.Dispatch.SetChauffeurLink(ctx, tenantID, cid, c.ID, "adore"); err != httpx.ErrValidation {
		t.Fatalf("bad kind: %v", err)
	}
	if _, err := s.Dispatch.SetChauffeurLink(ctx, tenantID, cid, "no-such-crew", model.LinkFavorite); err != httpx.ErrNotFound {
		t.Fatalf("unknown crew: %v", err)
	}

	// Set, list, re-set (upsert), clear.
	if _, err := s.Dispatch.SetChauffeurLink(ctx, tenantID, cid, c.ID, model.LinkFavorite); err != nil {
		t.Fatal(err)
	}
	links, _ := s.Dispatch.ChauffeurLinks(ctx, tenantID, cid)
	if len(links) != 1 || links[0].Kind != model.LinkFavorite {
		t.Fatalf("links: %+v", links)
	}
	if _, err := s.Dispatch.SetChauffeurLink(ctx, tenantID, cid, c.ID, model.LinkBlocked); err != nil {
		t.Fatal(err)
	}
	links, _ = s.Dispatch.ChauffeurLinks(ctx, tenantID, cid)
	if len(links) != 1 || links[0].Kind != model.LinkBlocked {
		t.Fatalf("upsert: %+v", links)
	}
	if _, err := s.Dispatch.SetChauffeurLink(ctx, tenantID, cid, c.ID, "none"); err != nil {
		t.Fatal(err)
	}
	links, _ = s.Dispatch.ChauffeurLinks(ctx, tenantID, cid)
	if len(links) != 0 {
		t.Fatalf("clear: %+v", links)
	}
}
