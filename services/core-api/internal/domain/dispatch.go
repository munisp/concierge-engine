// Dispatch service (M4): assignments, live trip status, geostamps and the
// real-time board feed. Real-time fan-out uses SSE (the board subscribes to
// /v1/dispatch/stream); WebSockets terminate at the edge gateway.
package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

var tripStatusFlow = map[string][]string{
	"ASSIGNED":           {"EN_ROUTE_TO_PICKUP", "CANCELLED_TRIP"},
	"EN_ROUTE_TO_PICKUP": {"ARRIVED", "CANCELLED_TRIP"},
	"ARRIVED":            {"PASSENGER_ON_BOARD", "NO_SHOW", "CANCELLED_TRIP"},
	"PASSENGER_ON_BOARD": {"IN_TRIP"},
	"IN_TRIP":            {"COMPLETED_TRIP"},
}

type DispatchService struct {
	s   *Services
	hub *SSEHub
}

func NewDispatchService(s *Services) *DispatchService {
	return &DispatchService{s: s, hub: NewSSEHub()}
}

func (svc *DispatchService) Hub() *SSEHub { return svc.hub }

func (svc *DispatchService) AddFleetResource(ctx context.Context, f *model.FleetResource) (*model.FleetResource, error) {
	if f.Kind == "" || f.Label == "" {
		return nil, httpx.ErrValidation
	}
	if f.ID == "" {
		f.ID = NewID()
	}
	if f.QualityScore == 0 {
		f.QualityScore = 1.0
	}
	f.Active = true
	if err := svc.s.Store.Dispatch().AddFleetResource(ctx, f); err != nil {
		return nil, err
	}
	return f, nil
}

func (svc *DispatchService) AddCrewMember(ctx context.Context, c *model.CrewMember) (*model.CrewMember, error) {
	if c.FullName == "" {
		return nil, httpx.ErrValidation
	}
	if c.ID == "" {
		c.ID = NewID()
	}
	if c.QualityScore == 0 {
		c.QualityScore = 1.0
	}
	c.Active = true
	if err := svc.s.Store.Dispatch().AddCrewMember(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (svc *DispatchService) ListFleet(ctx context.Context, tenantID string) ([]model.FleetResource, error) {
	return svc.s.Store.Dispatch().ListFleet(ctx, tenantID)
}

func (svc *DispatchService) ListCrew(ctx context.Context, tenantID string) ([]model.CrewMember, error) {
	return svc.s.Store.Dispatch().ListCrew(ctx, tenantID)
}

// Terminal assignment states free the resource for further scheduling.
var terminalAssignmentStatuses = map[string]bool{
	"COMPLETED_TRIP": true, "CANCELLED_TRIP": true, "NO_SHOW": true,
}

// Assign assigns a booking to a fleet resource / crew member (tenant's own
// fleet first; partner routing arrives in Phase 2 via the same path).
//
// Phase 22 scheduling: the assignment carries the service window — resolved
// from the booking's request item (pickup_at + hours, else the tenant's
// default duration) unless set explicitly. A windowed assignment on the
// tenant's own fleet/crew is conflict-gated: any overlap (each side padded
// by the tenant turnaround buffer) with a non-terminal assignment of the
// same resource or crew member is refused with 409 naming the conflict.
// Partner-routed assignments are not gated (the partner owns their fleet's
// schedule). A zero window (schedule unknown) is recorded but not gated —
// honest degrade over inventing availability.
func (svc *DispatchService) Assign(ctx context.Context, a *model.Assignment) (*model.Assignment, error) {
	if a.BookingID == "" {
		return nil, httpx.ErrValidation
	}
	if a.FleetResourceID == "" && a.PartnerID == "" {
		return nil, httpx.ErrValidation
	}
	if a.ID == "" {
		a.ID = NewID()
	}
	a.Status = "ASSIGNED"
	a.CreatedAt = svc.s.Now()
	if a.SLADeadline.IsZero() {
		a.SLADeadline = svc.s.Now().Add(30 * time.Minute)
	}
	svc.resolveWindow(ctx, a)
	if err := svc.gateConflicts(ctx, a); err != nil {
		return nil, err
	}
	if err := svc.s.Store.Dispatch().CreateAssignment(ctx, a); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, a.TenantID, "assignment", a.ID, "chauffeur.assigned",
		map[string]any{"booking_id": a.BookingID, "resource": a.FleetResourceID})
	svc.broadcast(a.TenantID, "assignment", a)
	return a, nil
}

// resolveWindow fills StartAt/EndAt from the booking's request when the
// caller didn't set them: first item's pickup_at, duration = item hours
// (else tenant DispatchConfig.DefaultDurationMinutes, default 120).
func (svc *DispatchService) resolveWindow(ctx context.Context, a *model.Assignment) {
	if !a.StartAt.IsZero() && !a.EndAt.IsZero() {
		return
	}
	b, err := svc.s.Store.Bookings().Get(ctx, a.TenantID, a.BookingID)
	if err != nil {
		return
	}
	r, err := svc.s.Store.Requests().Get(ctx, a.TenantID, b.RequestID)
	if err != nil || len(r.Items) == 0 {
		return
	}
	item := r.Items[0]
	if item.PickupAt <= 0 {
		return
	}
	a.StartAt = time.Unix(item.PickupAt, 0).UTC()
	durMin := item.Hours * 60
	if durMin <= 0 {
		durMin = svc.dispatchConfig(ctx, a.TenantID).DefaultDurationMinutes
		if durMin <= 0 {
			durMin = 120
		}
	}
	a.EndAt = a.StartAt.Add(time.Duration(durMin) * time.Minute)
}

func (svc *DispatchService) dispatchConfig(ctx context.Context, tenantID string) model.DispatchConfig {
	if t, err := svc.s.Store.Tenants().Get(ctx, tenantID); err == nil {
		return t.DispatchConfig
	}
	return model.DispatchConfig{}
}

// gateConflicts refuses double-booking on the tenant's own resources.
func (svc *DispatchService) gateConflicts(ctx context.Context, a *model.Assignment) error {
	if a.StartAt.IsZero() || a.EndAt.IsZero() {
		return nil // schedule unknown — recorded, not gated
	}
	if a.FleetResourceID == "" && a.CrewMemberID == "" {
		return nil // partner-routed: the partner owns their fleet's schedule
	}
	buffer := time.Duration(svc.dispatchConfig(ctx, a.TenantID).TurnaroundMinutes) * time.Minute
	if buffer <= 0 {
		buffer = 30 * time.Minute
	}
	existing, err := svc.s.Store.Dispatch().ListAssignments(ctx, a.TenantID)
	if err != nil {
		return err
	}
	// Turnaround pads the END of each busy window: the next pickup must be
	// at least `buffer` after the previous drop-off (reposition/cleanup).
	start, end := a.StartAt, a.EndAt.Add(buffer)
	for i := range existing {
		e := &existing[i]
		if e.ID == a.ID || terminalAssignmentStatuses[e.Status] ||
			e.StartAt.IsZero() || e.EndAt.IsZero() {
			continue
		}
		sameResource := a.FleetResourceID != "" && e.FleetResourceID == a.FleetResourceID
		sameCrew := a.CrewMemberID != "" && e.CrewMemberID == a.CrewMemberID
		if !sameResource && !sameCrew {
			continue
		}
		eStart, eEnd := e.StartAt, e.EndAt.Add(buffer)
		if start.Before(eEnd) && eStart.Before(end) {
			return fmt.Errorf("%w: scheduling conflict with assignment %s (%s – %s)",
				httpx.ErrConflict, e.ID,
				e.StartAt.Format(time.RFC3339), e.EndAt.Format(time.RFC3339))
		}
	}
	return nil
}

// ResourceAvailability is one fleet resource's busy windows in a range.
type ResourceAvailability struct {
	FleetResourceID string             `json:"fleet_resource_id"`
	Label           string             `json:"label"`
	Busy            []AssignmentWindow `json:"busy"`
	Free            bool               `json:"free"` // no busy window intersecting the range
}

type AssignmentWindow struct {
	AssignmentID string    `json:"assignment_id"`
	BookingID    string    `json:"booking_id"`
	StartAt      time.Time `json:"start_at"`
	EndAt        time.Time `json:"end_at"`
}

// Availability answers "what's free between from and to": every active
// fleet resource with its non-terminal busy windows intersecting the range
// (turnaround-padded). Resources with no intersecting window are Free.
func (svc *DispatchService) Availability(ctx context.Context, tenantID string, from, to time.Time) ([]ResourceAvailability, error) {
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return nil, httpx.ErrValidation
	}
	fleet, err := svc.s.Store.Dispatch().ListFleet(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	assignments, err := svc.s.Store.Dispatch().ListAssignments(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	buffer := time.Duration(svc.dispatchConfig(ctx, tenantID).TurnaroundMinutes) * time.Minute
	if buffer <= 0 {
		buffer = 30 * time.Minute
	}
	out := make([]ResourceAvailability, 0, len(fleet))
	for _, f := range fleet {
		if !f.Active {
			continue
		}
		ra := ResourceAvailability{FleetResourceID: f.ID, Label: f.Label, Free: true}
		for i := range assignments {
			a := &assignments[i]
			if a.FleetResourceID != f.ID || terminalAssignmentStatuses[a.Status] ||
				a.StartAt.IsZero() || a.EndAt.IsZero() {
				continue
			}
			// End-padded busy window, same semantics as the conflict gate.
			if a.StartAt.Before(to) && from.Before(a.EndAt.Add(buffer)) {
				raa := AssignmentWindow{AssignmentID: a.ID, BookingID: a.BookingID,
					StartAt: a.StartAt, EndAt: a.EndAt}
				ra.Busy = append(ra.Busy, raa)
				ra.Free = false
			}
		}
		out = append(out, ra)
	}
	return out, nil
}

// UpdateStatus advances the trip status machine and pushes live status to
// the dispatch board (FR-D6: live trip status events to clients follow via
// the notification templates in the comms module).
func (svc *DispatchService) UpdateStatus(ctx context.Context, tenantID, assignmentID, status string) error {
	list, err := svc.s.Store.Dispatch().ListAssignments(ctx, tenantID)
	if err != nil {
		return err
	}
	var current *model.Assignment
	for i := range list {
		if list[i].ID == assignmentID {
			current = &list[i]
			break
		}
	}
	if current == nil {
		return httpx.ErrNotFound
	}
	allowed := false
	for _, next := range tripStatusFlow[current.Status] {
		if next == status {
			allowed = true
			break
		}
	}
	if !allowed {
		return httpx.ErrConflict
	}
	if err := svc.s.Store.Dispatch().UpdateAssignmentStatus(ctx, tenantID, assignmentID, status); err != nil {
		return err
	}
	svc.s.Events.Emit(ctx, tenantID, "assignment", assignmentID, "trip.status_changed",
		map[string]any{"from": current.Status, "to": status, "booking_id": current.BookingID})
	svc.broadcast(tenantID, "trip_status", map[string]any{
		"assignment_id": assignmentID, "booking_id": current.BookingID, "status": status,
	})
	// Phase 35 (O12): client journey notification — service lane, advisory.
	svc.s.journeyNotify(ctx, tenantID, current, status)
	return nil
}

// GeoStamp records proof-of-service geostamps (FR-D3) and streams them to
// the board.
func (svc *DispatchService) GeoStamp(ctx context.Context, g *model.GeoStamp) (*model.GeoStamp, error) {
	if g.AssignmentID == "" || g.Kind == "" {
		return nil, httpx.ErrValidation
	}
	if g.ID == "" {
		g.ID = NewID()
	}
	if g.At.IsZero() {
		g.At = svc.s.Now()
	}
	if err := svc.s.Store.Dispatch().AddGeoStamp(ctx, g); err != nil {
		return nil, err
	}
	svc.broadcast(g.TenantID, "geostamp", g)
	return g, nil
}

func (svc *DispatchService) ListAssignments(ctx context.Context, tenantID string) ([]model.Assignment, error) {
	return svc.s.Store.Dispatch().ListAssignments(ctx, tenantID)
}

func (svc *DispatchService) broadcast(tenantID, kind string, v any) {
	payload, _ := json.Marshal(v)
	svc.hub.Publish(tenantID, kind, payload)
}

// SSEHub is a tenant-scoped server-sent-events hub for the dispatch board.
type SSEHub struct {
	mu      sync.Mutex
	clients map[string]map[chan sseEvent]struct{} // tenantID -> subscriber set
}

type sseEvent struct {
	kind string
	data []byte
}

func NewSSEHub() *SSEHub {
	return &SSEHub{clients: map[string]map[chan sseEvent]struct{}{}}
}

func (h *SSEHub) Publish(tenantID, kind string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients[tenantID] {
		select {
		case ch <- sseEvent{kind: kind, data: data}:
		default: // drop for slow consumers; board re-syncs on reconnect
		}
	}
}

// ServeHTTP streams events for a tenant until the client disconnects.
func (h *SSEHub) ServeHTTP(w http.ResponseWriter, r *http.Request, tenantID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch := make(chan sseEvent, 64)
	h.mu.Lock()
	if h.clients[tenantID] == nil {
		h.clients[tenantID] = map[chan sseEvent]struct{}{}
	}
	h.clients[tenantID][ch] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients[tenantID], ch)
		h.mu.Unlock()
	}()

	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.kind, ev.data)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
