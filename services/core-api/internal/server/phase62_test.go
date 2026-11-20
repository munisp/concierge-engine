package server_test

// Phase 62: chauffeur-matching endpoints — suggestions, matched assign,
// console link management, and rider self-serve marks on the track page.

import (
	"net/http"
	"testing"

	"concierge-engine/services/core-api/internal/model"
)

func TestChauffeurMatchingAPI62(t *testing.T) {
	env := newEnv(t)

	var res struct {
		Client struct {
			ID string `json:"id"`
		} `json:"client"`
	}
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"phone": "+2348060606060", "name": "Ngozi Eze"}, http.StatusOK, &res)
	cid := res.Client.ID
	env.do("PUT", "/v1/clients/"+cid+"/preferences/comms.language", env.tenantID,
		map[string]string{"value": "fr", "recorded_by": "console"}, http.StatusOK, nil)
	env.do("PUT", "/v1/clients/"+cid+"/preferences/ride.conversation", env.tenantID,
		map[string]string{"value": "quiet", "recorded_by": "console"}, http.StatusOK, nil)

	// Crew: one language+style match, one plain, one the rider blocked.
	match, err := env.svc.Dispatch.AddCrewMember(t.Context(), &model.CrewMember{
		TenantID: env.tenantID, FullName: "Amadou Diallo",
		Languages: []string{"fr"}, Styles: []string{"quiet"}, QualityScore: 9, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.Dispatch.AddCrewMember(t.Context(), &model.CrewMember{
		TenantID: env.tenantID, FullName: "Bruno Silva", QualityScore: 5, Active: true}); err != nil {
		t.Fatal(err)
	}
	blocked, err := env.svc.Dispatch.AddCrewMember(t.Context(), &model.CrewMember{
		TenantID: env.tenantID, FullName: "Dmitri Petrov", QualityScore: 10, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	env.do("PUT", "/v1/clients/"+cid+"/chauffeur-links/"+blocked.ID, env.tenantID,
		map[string]string{"kind": "blocked"}, http.StatusOK, nil)
	env.do("PUT", "/v1/clients/"+cid+"/chauffeur-links/"+blocked.ID, env.tenantID,
		map[string]string{"kind": "adore"}, http.StatusUnprocessableEntity, nil)

	bookingID := seedBookingForClient58(t, env, cid)

	// Suggestions: match first, blocked excluded.
	var sugg struct {
		Candidates []struct {
			Crew    model.CrewMember `json:"crew"`
			Score   int              `json:"score"`
			Reasons []string         `json:"reasons"`
		} `json:"candidates"`
	}
	env.do("GET", "/v1/bookings/"+bookingID+"/crew-suggestions", env.tenantID, nil, http.StatusOK, &sugg)
	if len(sugg.Candidates) != 2 || sugg.Candidates[0].Crew.ID != match.ID {
		t.Fatalf("suggestions: %+v", sugg.Candidates)
	}
	if sugg.Candidates[0].Score != 30+20+9 || len(sugg.Candidates[0].Reasons) != 3 {
		t.Fatalf("top candidate: %+v", sugg.Candidates[0])
	}

	// Matched assign records the decision on the assignment.
	var assigned struct {
		Assignment model.Assignment `json:"assignment"`
	}
	env.do("POST", "/v1/bookings/"+bookingID+"/assign-matched", env.tenantID, nil, http.StatusCreated, &assigned)
	if assigned.Assignment.CrewMemberID != match.ID || assigned.Assignment.MatchScore != 59 ||
		len(assigned.Assignment.MatchReasons) == 0 {
		t.Fatalf("matched assignment: %+v", assigned.Assignment)
	}

	// Rider self-serve: favorite this trip's chauffeur from the track page.
	trackToken := env.svc.Trips.ClientTokenFor(env.tenantID, assigned.Assignment.ID)
	env.doBearer("PUT", "/v1/track/"+trackToken+"/chauffeur", "",
		map[string]string{"kind": "favorite"}, http.StatusOK, nil)
	var track struct {
		CrewMemberID  string `json:"crew_member_id"`
		ChauffeurLink string `json:"chauffeur_link"`
	}
	env.doBearer("GET", "/v1/track/"+trackToken, "", nil, http.StatusOK, &track)
	if track.CrewMemberID != match.ID || track.ChauffeurLink != "favorite" {
		t.Fatalf("track link round-trip: %+v", track)
	}

	// Console sees both marks; clearing works.
	var links struct {
		Links []model.ChauffeurLink `json:"links"`
	}
	env.do("GET", "/v1/clients/"+cid+"/chauffeur-links", env.tenantID, nil, http.StatusOK, &links)
	if len(links.Links) != 2 {
		t.Fatalf("links: %+v", links.Links)
	}
	env.do("DELETE", "/v1/clients/"+cid+"/chauffeur-links/"+blocked.ID, env.tenantID, nil, http.StatusNoContent, nil)
	env.do("GET", "/v1/clients/"+cid+"/chauffeur-links", env.tenantID, nil, http.StatusOK, &links)
	if len(links.Links) != 1 || links.Links[0].Kind != "favorite" {
		t.Fatalf("after clear: %+v", links.Links)
	}
}
