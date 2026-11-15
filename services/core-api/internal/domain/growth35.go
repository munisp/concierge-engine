// Growth surfaces (Phase 35): the acquisition-side innovations that are
// pure computation over the existing ledgers.
//
//	A5  referral leaderboard — ranks clients by referral_reward accruals
//	A8  first-booking credit — fixed welcome credit on a client's first
//	    confirmed booking (AcquisitionConfig.FirstBookingCreditMinor)
//	A10 social proof — real, counted proof points for the public site
//	    (never rounded, never fabricated; zero counts stay hidden)
package domain

import (
	"context"
	"sort"

	"concierge-engine/services/core-api/internal/model"
)

// ------------------------------------------------------------- A5: leaderboard

// ReferralRank is one row of the referral leaderboard. Contact details are
// deliberately absent — the console renders this to operators, the public
// API never exposes it.
type ReferralRank struct {
	ClientID     string `json:"client_id"`
	Name         string `json:"name"`
	Code         string `json:"code"` // the client's shareable referral code
	Conversions  int    `json:"conversions"`
	RewardsMinor int64  `json:"rewards_minor"`
	Currency     string `json:"currency,omitempty"`
}

// ReferralLeaderboard ranks clients by referral_reward accruals — the
// ledger is the source of truth (a conversion that didn't pay out doesn't
// count). Clients with zero conversions are excluded.
func (svc *Services) ReferralLeaderboard(ctx context.Context, tenantID string, limit int) ([]ReferralRank, error) {
	clients, err := svc.Store.Clients().List(ctx, tenantID, 5000)
	if err != nil {
		return nil, err
	}
	var ranks []ReferralRank
	for _, c := range clients {
		rewards, err := svc.Store.Rewards().List(ctx, tenantID, c.ID)
		if err != nil {
			continue
		}
		r := ReferralRank{ClientID: c.ID, Name: c.FullName, Code: model.ClientReferralCode(c.ID)}
		for _, rw := range rewards {
			if rw.Kind != "referral_reward" || rw.State == model.RewardExpired {
				continue
			}
			r.Conversions++
			r.RewardsMinor += rw.AmountMinor
			r.Currency = rw.Currency
		}
		if r.Conversions > 0 {
			ranks = append(ranks, r)
		}
	}
	sort.Slice(ranks, func(i, j int) bool {
		if ranks[i].Conversions != ranks[j].Conversions {
			return ranks[i].Conversions > ranks[j].Conversions
		}
		return ranks[i].RewardsMinor > ranks[j].RewardsMinor
	})
	if limit > 0 && len(ranks) > limit {
		ranks = ranks[:limit]
	}
	if ranks == nil {
		ranks = []ReferralRank{}
	}
	return ranks, nil
}

// ------------------------------------------------------- A8: first-booking credit

// firstBookingCredit posts the tenant's configured welcome credit when the
// booking that just confirmed is the client's FIRST. Fixed-amount (not a
// percentage), idempotent per (tenant, client, source booking) through the
// rewards ledger. Advisory: a credit failure never blocks a confirmation.
func (svc *Services) firstBookingCredit(ctx context.Context, tenantID, clientID string, b *model.Booking, currency string) {
	if clientID == "" {
		return
	}
	t, err := svc.Tenants.Get(ctx, tenantID)
	if err != nil || t.AcquisitionConfig.FirstBookingCreditMinor <= 0 {
		return
	}
	stats, err := svc.clientBookingStats(ctx, tenantID)
	if err != nil {
		return
	}
	if stats[clientID].Bookings != 1 {
		return // not their first — or the join missed; never double-credit
	}
	reward := &model.ClientReward{
		ID: NewID(), TenantID: tenantID, ClientID: clientID,
		SourceBookingID: b.ID, Kind: "first_booking",
		AmountMinor: t.AcquisitionConfig.FirstBookingCreditMinor, Currency: currency,
		State: model.RewardAccrued, CreatedAt: svc.Now(),
	}
	if err := svc.Store.Rewards().Accrue(ctx, reward); err != nil {
		return // replayed confirmation — already credited
	}
	svc.Events.Emit(ctx, tenantID, "client", clientID, "acquisition.first_booking_credit",
		map[string]any{"amount_minor": reward.AmountMinor, "currency": currency, "booking_id": b.ID})
}

// ------------------------------------------------------------- A10: social proof

// SocialProof counts the proof points the public site may claim. Every
// number is a real count over the ledger — the site renders a stat only
// when it is non-zero (a zero is not a bragging point, and hiding it is
// the honest move).
type SocialProof struct {
	ClientsServed  int `json:"clients_served"`  // clients with >= 1 booking
	TripsCompleted int `json:"trips_completed"` // assignments at COMPLETED_TRIP
	BookingsTotal  int `json:"bookings_total"`
}

func (svc *Services) SocialProof(ctx context.Context, tenantID string) (*SocialProof, error) {
	stats, err := svc.clientBookingStats(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	p := &SocialProof{}
	for _, st := range stats {
		if st.Bookings > 0 {
			p.ClientsServed++
		}
		p.BookingsTotal += st.Bookings
	}
	if assignments, err := svc.Store.Dispatch().ListAssignments(ctx, tenantID); err == nil {
		for _, a := range assignments {
			if a.Status == "COMPLETED_TRIP" {
				p.TripsCompleted++
			}
		}
	}
	return p, nil
}
