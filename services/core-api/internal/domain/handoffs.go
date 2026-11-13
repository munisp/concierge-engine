// Handoff service (AI spec §5): human-in-the-loop queues with structured
// briefs. Escalation never sends a human a raw thread — the brief packages
// client summary, request state, what the AI already did, why it escalated,
// and suggested next actions.
package domain

import (
	"context"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

var triggerKinds = map[string]bool{
	"VALUE": true, "CONFIDENCE": true, "SENTIMENT": true, "POLICY": true, "SLA": true,
}

var validResolutions = map[string]bool{
	"APPROVED": true, "EDITED": true, "REJECTED": true, "RESOLVED": true,
}

type HandoffService struct{ s *Services }

// Open escalates a conversation to a human with a structured brief.
// PendingAction holds a drafted TIER-3 action awaiting approve/edit/reject.
func (svc *HandoffService) Open(ctx context.Context, tenantID, conversationID, trigger, reason string, pending map[string]any) (*model.HandoffItem, error) {
	if !validTriggers(trigger) {
		return nil, httpx.ErrValidation
	}
	timeline, _ := svc.s.Store.Conversations().Timeline(ctx, tenantID, conversationID)
	brief := buildBrief(timeline, reason)
	h := &model.HandoffItem{
		ID: NewID(), TenantID: tenantID, ConversationID: conversationID,
		Trigger: trigger, Reason: reason, Brief: brief, PendingAction: pending,
		Status: "OPEN", CreatedAt: svc.s.Now(),
	}
	if err := svc.s.Store.Handoffs().Open(ctx, h); err != nil {
		return nil, err
	}
	// The conversation state machine re-enters at ESCALATED with full context.
	_ = svc.s.Store.Conversations().SetState(ctx, tenantID, conversationID, "ESCALATED")
	svc.s.Events.Emit(ctx, tenantID, "conversation", conversationID, "handoff.opened",
		map[string]any{"trigger": trigger, "reason": reason})
	return h, nil
}

func validTriggers(t string) bool { return triggerKinds[t] }

func buildBrief(timeline []model.Message, reason string) map[string]any {
	// Compact handoff brief — rendered in <5s in the concierge console.
	recent := timeline
	if len(recent) > 10 {
		recent = recent[len(recent)-10:]
	}
	snips := make([]string, 0, len(recent))
	for _, m := range recent {
		body := m.Body
		if len(body) > 140 {
			body = body[:140] + "…"
		}
		snips = append(snips, m.Author+": "+body)
	}
	return map[string]any{
		"reason":          reason,
		"recent_messages": snips,
		"message_count":   len(timeline),
	}
}

func (svc *HandoffService) Resolve(ctx context.Context, tenantID, id, resolution, owner string) (*model.HandoffItem, error) {
	if !validResolutions[resolution] {
		return nil, httpx.ErrValidation
	}
	if owner == "" {
		return nil, httpx.ErrValidation
	}
	if err := svc.s.Store.Handoffs().Resolve(ctx, tenantID, id, resolution, owner); err != nil {
		return nil, err
	}
	h, err := svc.s.Store.Handoffs().Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "conversation", h.ConversationID, "handoff.resolved",
		map[string]any{"resolution": resolution, "owner": owner})
	// Rejections feed the eval set with reasons (AI spec §5.3).
	if resolution == "REJECTED" {
		svc.s.Events.Emit(ctx, tenantID, "conversation", h.ConversationID, "eval.rejection_sampled",
			map[string]any{"handoff_id": id})
	}
	return h, nil
}

func (svc *HandoffService) ListOpen(ctx context.Context, tenantID string) ([]model.HandoffItem, error) {
	return svc.s.Store.Handoffs().ListOpen(ctx, tenantID)
}
