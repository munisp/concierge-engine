package domain

// Phase 52: engagement-driven send-time optimization (Mautic feature-borrow
// A2). The Phase 35 optimizer inferred a client's best send hour from their
// BOOKING pattern — a proxy. The attribution spine now carries the real
// signal: when the client actually opens, clicks and replies. This file
// upgrades the optimizer to prefer that engagement evidence and fall back
// to the booking pattern only when there isn't enough of it — every
// deferral records which evidence drove it in the send's detail.
//
// Honesty rules: only the client's OWN engagement timestamps are used,
// never a global "best time to email" statistic; below the minimum sample
// the engine says so and defers to the weaker signal rather than
// fabricating confidence.

import (
	"context"
	"time"
)

// StoMinEngagements is the minimum number of engagement events before the
// modal engagement hour is trusted. Below it the booking-pattern fallback
// (Phase 35) applies; with no bookings either, the send is not deferred.
const StoMinEngagements = 3

// stoEngagementKinds are the attribution events that prove the client was
// reading/tapping at that moment: ESP-tracked opens and clicks (Phase
// 39/41) and inbound replies on any channel (Phase 42).
var stoEngagementKinds = map[string]bool{
	"email_opened":   true,
	"email_clicked":  true,
	"client_replied": true,
}

// engagementHour returns the next occurrence (at least one hour out) of the
// client's modal engagement hour plus the sample size behind it. Zero time
// = insufficient evidence.
func (svc *OutreachService) engagementHour(ctx context.Context, tenantID, clientID string, now time.Time) (time.Time, int) {
	events, err := svc.s.Store.Attribution().List(ctx, tenantID, clientID, 1000)
	if err != nil {
		return time.Time{}, 0
	}
	var hours [24]int
	total := 0
	for _, ev := range events {
		if !stoEngagementKinds[ev.Kind] || ev.At.IsZero() {
			continue
		}
		hours[ev.At.Hour()]++
		total++
	}
	if total < StoMinEngagements {
		return time.Time{}, total
	}
	best, bestN := -1, 0
	for h, n := range hours {
		if n > bestN {
			best, bestN = h, n
		}
	}
	if best < 0 {
		return time.Time{}, total
	}
	d := time.Date(now.Year(), now.Month(), now.Day(), best, 0, 0, 0, now.Location())
	if !d.After(now.Add(time.Hour)) {
		d = d.Add(24 * time.Hour)
	}
	return d, total
}
