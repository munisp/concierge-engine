// Journey notifications (Phase 35, O12): the client hears about every
// meaningful trip transition the moment it happens — service lane (these
// ARE the service), idempotent per (assignment, status), advisory (a
// notification failure never blocks a status transition, and the dispatch
// event spine already carries the durable record).
package domain

import (
	"context"

	"concierge-engine/services/core-api/internal/model"
)

// journeyCopy maps a trip status to the client-facing message. Statuses
// absent from the map stay silent — IN_TRIP adds nothing over
// PASSENGER_ON_BOARD, and NO_SHOW is an operator problem, not a client
// message.
var journeyCopy = map[string]string{
	"EN_ROUTE_TO_PICKUP": "Your chauffeur is on the way.",
	"ARRIVED":            "Your chauffeur has arrived at the pickup point.",
	"PASSENGER_ON_BOARD": "You're on board — have a great trip.",
	"COMPLETED_TRIP":     "Thank you for riding with us — we hope the trip was seamless.",
	"CANCELLED_TRIP":     "Your trip has been cancelled. Reply here and we'll make it right.",
}

// journeyNotify sends the client the transition's message. Called from
// DispatchService.UpdateStatus after the store write succeeded.
func (svc *Services) journeyNotify(ctx context.Context, tenantID string, a *model.Assignment, status string) {
	copy, ok := journeyCopy[status]
	if !ok {
		return
	}
	b, err := svc.Store.Bookings().Get(ctx, tenantID, a.BookingID)
	if err != nil {
		return
	}
	sr, err := svc.Store.Requests().Get(ctx, tenantID, b.RequestID)
	if err != nil || sr.ClientID == "" {
		return
	}
	_, _ = svc.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: sr.ClientID, Purpose: model.OutreachService, Body: copy,
		IdempotencyKey: "journey:" + a.ID + ":" + status,
	})
}
