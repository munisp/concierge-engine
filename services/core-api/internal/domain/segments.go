// Segment engine (Phase 35, O2): declarative client segments evaluated
// against the live ledger — clients, their bookings (joined through the
// request anchor), and consent flags. Every present rule must match (AND).
// Segments feed broadcast campaigns (O1) and the console preview counter,
// so the audience an operator sees is exactly the audience that receives.
package domain

import (
	"context"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// clientStats is the booking-derived half of segment evaluation.
type clientStats struct {
	Bookings     int
	LastBooking  time.Time
	FirstBooking time.Time
}

// SegmentClients returns the clients matching the rules. Erased clients
// (GDPR/CCPA) are never in a segment — they must not be contactable even
// by accident of rule construction.
func (svc *Services) SegmentClients(ctx context.Context, tenantID string, r model.SegmentRules) ([]model.Client, error) {
	clients, err := svc.Store.Clients().List(ctx, tenantID, 5000)
	if err != nil {
		return nil, err
	}
	stats, err := svc.clientBookingStats(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	now := svc.Now()
	var out []model.Client
	for _, c := range clients {
		if c.Erased() {
			continue
		}
		if matchSegment(c, stats[c.ID], r, now) {
			out = append(out, c)
		}
	}
	return out, nil
}

// clientBookingStats joins bookings to clients through the request anchor
// (memoized per request — the N+1 pattern bounded by memo).
func (svc *Services) clientBookingStats(ctx context.Context, tenantID string) (map[string]clientStats, error) {
	bookings, err := svc.Store.Bookings().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	reqClient := map[string]string{}
	out := map[string]clientStats{}
	for _, b := range bookings {
		cid, ok := reqClient[b.RequestID]
		if !ok {
			sr, err := svc.Store.Requests().Get(ctx, tenantID, b.RequestID)
			if err != nil {
				continue
			}
			cid = sr.ClientID
			reqClient[b.RequestID] = cid
		}
		st := out[cid]
		st.Bookings++
		if b.CreatedAt.After(st.LastBooking) {
			st.LastBooking = b.CreatedAt
		}
		if st.FirstBooking.IsZero() || b.CreatedAt.Before(st.FirstBooking) {
			st.FirstBooking = b.CreatedAt
		}
		out[cid] = st
	}
	return out, nil
}

func matchSegment(c model.Client, st clientStats, r model.SegmentRules, now time.Time) bool {
	if r.Locale != "" && c.Locale != r.Locale {
		return false
	}
	if r.LoyaltyTier != "" && c.LoyaltyTier != r.LoyaltyTier {
		return false
	}
	if r.OrgID != "" && c.OrgID != r.OrgID {
		return false
	}
	if r.VIPFlag != "" {
		found := false
		for _, f := range c.VIPFlags {
			if f == r.VIPFlag {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if r.MarketingConsent != nil && (c.Consent["marketing"] == true) != *r.MarketingConsent {
		return false
	}
	if r.HasBookings != nil && (st.Bookings > 0) != *r.HasBookings {
		return false
	}
	if r.MinBookings > 0 && st.Bookings < r.MinBookings {
		return false
	}
	if r.NoBookingSinceDays > 0 {
		cutoff := now.Add(-time.Duration(r.NoBookingSinceDays) * 24 * time.Hour)
		if st.Bookings > 0 && st.LastBooking.After(cutoff) {
			return false // active recently
		}
	}
	if r.CreatedBeforeDays > 0 && c.CreatedAt.After(now.Add(-time.Duration(r.CreatedBeforeDays)*24*time.Hour)) {
		return false
	}
	return true
}
