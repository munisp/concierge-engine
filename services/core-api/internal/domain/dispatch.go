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

// Assign assigns a booking to a fleet resource / crew member (tenant's own
// fleet first; partner routing arrives in Phase 2 via the same path).
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
	if err := svc.s.Store.Dispatch().CreateAssignment(ctx, a); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, a.TenantID, "assignment", a.ID, "chauffeur.assigned",
		map[string]any{"booking_id": a.BookingID, "resource": a.FleetResourceID})
	svc.broadcast(a.TenantID, "assignment", a)
	return a, nil
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
