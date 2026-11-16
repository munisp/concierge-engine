package store

// Phase 46: data retention & minimization (GDPR Art. 5(1)(e)) — in-memory
// implementations of the purge/scrub primitives.

import (
	"context"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// PurgeGeoStampsBefore permanently removes the tenant's geo stamps recorded
// before `before`. Location history is the most sensitive data we hold.
func (s *memDispatch) PurgeGeoStampsBefore(_ context.Context, tenantID string, before time.Time) (int, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	kept := s.m.geostamps[:0]
	removed := 0
	for _, g := range s.m.geostamps {
		if g.TenantID == tenantID && g.At.Before(before) {
			removed++
			continue
		}
		kept = append(kept, g)
	}
	// Zero the tail so purged stamps aren't retained by the backing array.
	for i := len(kept); i < len(s.m.geostamps); i++ {
		s.m.geostamps[i] = model.GeoStamp{}
	}
	s.m.geostamps = kept
	return removed, nil
}

// ScrubMessageBodiesBefore overwrites message bodies with `marker` in the
// tenant's CLOSED conversations for messages sent before `before`. Message
// rows survive; only the content goes. Already-scrubbed bodies are not
// re-counted (idempotent).
func (s *memConversations) ScrubMessageBodiesBefore(_ context.Context, tenantID string, before time.Time, marker string) (int, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	scrubbed := 0
	for convID, msgs := range s.m.messages {
		c, ok := s.m.conversations[convID]
		if !ok || c.TenantID != tenantID || c.State != "CLOSED" {
			continue
		}
		for i := range msgs {
			if msgs[i].SentAt.Before(before) && msgs[i].Body != "" && msgs[i].Body != marker {
				msgs[i].Body = marker
				scrubbed++
			}
		}
		s.m.messages[convID] = msgs
	}
	return scrubbed, nil
}

// ScrubMessageBodiesForClient overwrites every message body in the client's
// conversations regardless of conversation state — the GDPR Art. 17 erasure
// companion invoked from EraseWithEvidence.
func (s *memConversations) ScrubMessageBodiesForClient(_ context.Context, tenantID, clientID, marker string) (int, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	scrubbed := 0
	for convID, msgs := range s.m.messages {
		c, ok := s.m.conversations[convID]
		if !ok || c.TenantID != tenantID || c.ClientID != clientID {
			continue
		}
		for i := range msgs {
			if msgs[i].Body != "" && msgs[i].Body != marker {
				msgs[i].Body = marker
				scrubbed++
			}
		}
		s.m.messages[convID] = msgs
	}
	return scrubbed, nil
}

// PurgeBefore permanently deletes the tenant's attribution events recorded
// before `before`.
func (s *memAttribution) PurgeBefore(_ context.Context, tenantID string, before time.Time) (int, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	kept := s.m.attribution[:0]
	removed := 0
	for _, e := range s.m.attribution {
		if e.TenantID == tenantID && e.At.Before(before) {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	for i := len(kept); i < len(s.m.attribution); i++ {
		s.m.attribution[i] = model.AttributionEvent{}
	}
	s.m.attribution = kept
	return removed, nil
}
