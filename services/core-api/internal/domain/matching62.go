package domain

// Phase 62: chauffeur matching — dispatch stops being first-free-slot
// and starts being a scored, explainable decision.
//
// The scorer only consumes signals the platform already holds honestly:
// the rider's validated preference vocabulary (Phase 58), declared crew
// capabilities (languages / conversation styles), the rider's explicit
// favorite / blocked links, and completed-trip history. Every score is
// recorded on the assignment with human-readable reasons — a match is
// never an opaque ranking.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// Score weights — additive, deterministic, documented in docs/phase62.md.
const (
	matchFavorite    = 100 // rider asked for this chauffeur by name
	matchLanguage    = 30  // speaks the rider's comms.language
	matchStyle       = 20  // fits the ride.conversation style
	matchPerTrip     = 10  // per completed trip together
	matchTripCap     = 30  // familiarity never outweighs explicit signals
	matchQualityBase = 10  // quality_score normalized onto 0..10
)

// ScoredCrew is one matching candidate with its explanation.
type ScoredCrew struct {
	Crew    model.CrewMember `json:"crew"`
	Score   int              `json:"score"`
	Reasons []string         `json:"reasons"`
}

// MatchScore scores one crew member for one client. Blocked is excluded
// upstream (SuggestCrew) — this function never sees a blocked candidate.
func (svc *DispatchService) MatchScore(ctx context.Context, tenantID, clientID string,
	c *model.CrewMember, links map[string]string, rp *RideProfile) (int, []string) {
	score, reasons := 0, []string{}
	if links[c.ID] == model.LinkFavorite {
		score += matchFavorite
		reasons = append(reasons, "rider's favorite chauffeur")
	}
	if rp != nil {
		if rp.Language != "" && containsFold(c.Languages, rp.Language) {
			score += matchLanguage
			reasons = append(reasons, "speaks "+rp.Language)
		}
		if rp.Conversation != "" && containsFold(c.Styles, rp.Conversation) {
			score += matchStyle
			reasons = append(reasons, rp.Conversation+"-style match")
		}
	}
	if n, err := svc.s.Store.Dispatch().CountClientTrips(ctx, tenantID, clientID, c.ID); err == nil && n > 0 {
		pts := n * matchPerTrip
		if pts > matchTripCap {
			pts = matchTripCap
		}
		score += pts
		reasons = append(reasons, fmt.Sprintf("%d prior trip%s together", n, plural(n)))
	}
	if c.QualityScore > 0 {
		q := int(c.QualityScore / 10.0 * matchQualityBase) // quality 0..10 → 0..10
		if q > matchQualityBase {
			q = matchQualityBase
		}
		score += q
		if q >= matchQualityBase/2 {
			reasons = append(reasons, fmt.Sprintf("quality %.1f", c.QualityScore))
		}
	}
	return score, reasons
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(v)) {
			return true
		}
	}
	return false
}

// SuggestCrew ranks active crew for a booking's client. Blocked
// chauffeurs are hard-excluded; the rest sort by score desc, then
// quality desc, then name — deterministic on ties.
func (svc *DispatchService) SuggestCrew(ctx context.Context, tenantID, bookingID string) ([]ScoredCrew, error) {
	b, err := svc.s.Store.Bookings().Get(ctx, tenantID, bookingID)
	if err != nil {
		return nil, err
	}
	r, err := svc.s.Store.Requests().Get(ctx, tenantID, b.RequestID)
	if err != nil {
		return nil, err
	}
	crew, err := svc.s.Store.Dispatch().ListCrew(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	linksList, err := svc.s.Store.ChauffeurLinks().ListForClient(ctx, tenantID, r.ClientID)
	if err != nil {
		return nil, err
	}
	links := map[string]string{}
	for _, l := range linksList {
		links[l.CrewMemberID] = l.Kind
	}
	rp, err := svc.s.Clients.RideProfile(ctx, tenantID, r.ClientID)
	if err != nil {
		rp = nil // profile is a bonus signal, never a blocker
	}
	out := []ScoredCrew{}
	for i := range crew {
		c := &crew[i]
		if !c.Active || links[c.ID] == model.LinkBlocked {
			continue
		}
		score, reasons := svc.MatchScore(ctx, tenantID, r.ClientID, c, links, rp)
		if len(reasons) == 0 {
			reasons = []string{"available — no specific match signals"}
		}
		out = append(out, ScoredCrew{Crew: *c, Score: score, Reasons: reasons})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Crew.QualityScore != out[j].Crew.QualityScore {
			return out[i].Crew.QualityScore > out[j].Crew.QualityScore
		}
		return out[i].Crew.FullName < out[j].Crew.FullName
	})
	return out, nil
}

// AssignMatched assigns the top-ranked crew member to the booking. The
// recorded score/reasons are the decision's provenance. No candidates →
// 422 with an honest explanation, never a silent random pick.
func (svc *DispatchService) AssignMatched(ctx context.Context, tenantID, bookingID string) (*model.Assignment, error) {
	ranked, err := svc.SuggestCrew(ctx, tenantID, bookingID)
	if err != nil {
		return nil, err
	}
	if len(ranked) == 0 {
		return nil, httpx.ErrValidation
	}
	top := ranked[0]
	a := &model.Assignment{
		TenantID:     tenantID,
		BookingID:    bookingID,
		CrewMemberID: top.Crew.ID,
	}
	return svc.Assign(ctx, a)
}

// recordMatch stamps the match decision on crew assignments. Manual
// picks get score 0 with an explicit reason — the record always says
// how the chauffeur was chosen.
func (svc *DispatchService) recordMatch(ctx context.Context, a *model.Assignment) {
	if a.CrewMemberID == "" {
		return
	}
	b, err := svc.s.Store.Bookings().Get(ctx, a.TenantID, a.BookingID)
	if err != nil {
		return
	}
	r, err := svc.s.Store.Requests().Get(ctx, a.TenantID, b.RequestID)
	if err != nil {
		return
	}
	c, err := svc.crewMember(ctx, a.TenantID, a.CrewMemberID)
	if err != nil {
		return
	}
	linksList, err := svc.s.Store.ChauffeurLinks().ListForClient(ctx, a.TenantID, r.ClientID)
	if err != nil {
		return
	}
	links := map[string]string{}
	for _, l := range linksList {
		links[l.CrewMemberID] = l.Kind
	}
	if links[a.CrewMemberID] == model.LinkBlocked {
		a.MatchScore = -1
		a.MatchReasons = []string{"manual override — rider has this chauffeur blocked"}
		return
	}
	rp, err := svc.s.Clients.RideProfile(ctx, a.TenantID, r.ClientID)
	if err != nil {
		rp = nil
	}
	score, reasons := svc.MatchScore(ctx, a.TenantID, r.ClientID, c, links, rp)
	a.MatchScore = score
	if len(reasons) == 0 {
		reasons = []string{"manual pick — no specific match signals"}
	}
	a.MatchReasons = reasons
}

// crewMember finds one active-or-not crew member by id (ListCrew is the
// only read path the store exposes).
func (svc *DispatchService) crewMember(ctx context.Context, tenantID, id string) (*model.CrewMember, error) {
	crew, err := svc.s.Store.Dispatch().ListCrew(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for i := range crew {
		if crew[i].ID == id {
			return &crew[i], nil
		}
	}
	return nil, httpx.ErrNotFound
}

// SetChauffeurLink records / clears a rider's favorite or blocked mark.
func (svc *DispatchService) SetChauffeurLink(ctx context.Context, tenantID, clientID, crewID, kind string) (*model.ChauffeurLink, error) {
	if kind == "" || kind == "none" {
		if err := svc.s.Store.ChauffeurLinks().Clear(ctx, tenantID, clientID, crewID); err != nil {
			return nil, err
		}
		return &model.ChauffeurLink{TenantID: tenantID, ClientID: clientID, CrewMemberID: crewID, Kind: ""}, nil
	}
	if !model.ValidLinkKind(kind) {
		return nil, httpx.ErrValidation
	}
	if _, err := svc.crewMember(ctx, tenantID, crewID); err != nil {
		return nil, err
	}
	l := &model.ChauffeurLink{
		TenantID: tenantID, ClientID: clientID, CrewMemberID: crewID,
		Kind: kind, CreatedAt: time.Now().UTC(),
	}
	if err := svc.s.Store.ChauffeurLinks().Set(ctx, l); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "client", clientID, "chauffeur.link_set",
		map[string]any{"crew_member_id": crewID, "kind": kind})
	return l, nil
}

// ChauffeurLinks lists a rider's favorite/blocked marks (client 360).
func (svc *DispatchService) ChauffeurLinks(ctx context.Context, tenantID, clientID string) ([]model.ChauffeurLink, error) {
	return svc.s.Store.ChauffeurLinks().ListForClient(ctx, tenantID, clientID)
}
