// Conversation service (M2): one thread per client per trip across channels;
// every message is an event on the trip timeline.
package domain

import (
	"context"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type ConversationService struct{ s *Services }

func (svc *ConversationService) Open(ctx context.Context, tenantID, clientID, channel, externalRef string) (*model.Conversation, error) {
	if channel == "" {
		return nil, httpx.ErrValidation
	}
	c := &model.Conversation{
		ID: NewID(), TenantID: tenantID, ClientID: clientID, Channel: channel,
		State: "NEW", ExternalRef: externalRef, CreatedAt: svc.s.Now(),
	}
	if err := svc.s.Store.Conversations().CreateConversation(ctx, c); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "conversation", c.ID, "conversation.started",
		map[string]any{"channel": channel})
	svc.s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "conversation_started", Channel: channel, ClientID: clientID,
	})
	return c, nil
}

// AppendMessage adds a message to the thread. Outbound messages must carry a
// policy verdict id — they passed the reply guardrail pipeline (AI spec §4.3).
func (svc *ConversationService) AppendMessage(ctx context.Context, m *model.Message) (*model.Message, error) {
	if m.ConversationID == "" || m.Body == "" || m.Author == "" {
		return nil, httpx.ErrValidation
	}
	if m.ID == "" {
		m.ID = NewID()
	}
	m.SentAt = svc.s.Now()
	if err := svc.s.Store.Conversations().AppendMessage(ctx, m); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, m.TenantID, "conversation", m.ConversationID, "message.appended",
		map[string]any{"author": m.Author})
	return m, nil
}

func (svc *ConversationService) Timeline(ctx context.Context, tenantID, conversationID string) ([]model.Message, error) {
	return svc.s.Store.Conversations().Timeline(ctx, tenantID, conversationID)
}

func (svc *ConversationService) SetState(ctx context.Context, tenantID, id, state string) error {
	return svc.s.Store.Conversations().SetState(ctx, tenantID, id, state)
}
