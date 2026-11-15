// Drip sequences (Phase 35, O3): multi-step campaigns declared in the
// tenant's OutreachConfig.Sequences. The defining constraint is ZERO extra
// state: per-client progress is derived from the outreach_sends ledger via
// deterministic idempotency keys (seq:{name}:{step}:{client}), so the
// hourly sweep is replay-safe and a restored backup loses nothing.
//
// Semantics:
//   - Every client is enrolled at their CreatedAt. Step 0 fires
//     AfterHours after enrollment; step N fires AfterHours after step
//     N-1's scheduled fire time (not its delivery time — cadence drift
//     from quiet-hours deferral does not compound).
//   - Step N fires only after step N-1 has a ledger record (any status) —
//     a client who never consented accumulates HELD steps in order, and
//     consenting later does not unleash a backlog bombardment.
//   - Bodies render {{client_name}} / {{tenant_name}} server-side.
//   - All sends run through the outreach engine: consent gates, frequency
//     caps, quiet hours and delivery receipts apply unchanged.
package domain

import (
	"context"
	"fmt"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

type SequenceService struct {
	s *Services
}

type SequenceRunResult struct {
	Sent      int `json:"sent"`
	Held      int `json:"held"`
	Failed    int `json:"failed"`
	Scanned   int `json:"clients_scanned"`
	Sequences int `json:"sequences"`
}

// Run advances every configured sequence for every client of the tenant.
func (svc *SequenceService) Run(ctx context.Context, tenantID string) (*SequenceRunResult, error) {
	t, err := svc.s.Tenants.Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	res := &SequenceRunResult{Sequences: len(t.OutreachConfig.Sequences)}
	if len(t.OutreachConfig.Sequences) == 0 {
		return res, nil
	}
	clients, err := svc.s.Store.Clients().List(ctx, tenantID, 5000)
	if err != nil {
		return res, err
	}
	sends, err := svc.s.Store.Outreach().ListSends(ctx, tenantID, 20000)
	if err != nil {
		return res, err
	}
	done := make(map[string]bool, len(sends))
	for _, o := range sends {
		done[o.IdempotencyKey] = true
	}
	res.Scanned = len(clients)
	now := svc.s.Now()
	for _, seq := range t.OutreachConfig.Sequences {
		if seq.Name == "" || len(seq.Steps) == 0 {
			continue
		}
		purpose := seq.Purpose
		if purpose == "" {
			purpose = model.OutreachMarketing
		}
		for _, c := range clients {
			if c.Erased() {
				continue
			}
			fireAt := c.CreatedAt
			for i, step := range seq.Steps {
				fireAt = fireAt.Add(time.Duration(step.AfterHours) * time.Hour)
				key := fmt.Sprintf("seq:%s:%d:%s", seq.Name, i, c.ID)
				if done[key] {
					continue // step already recorded — any status
				}
				if i > 0 && !done[fmt.Sprintf("seq:%s:%d:%s", seq.Name, i-1, c.ID)] {
					break // sequential: wait for the previous step
				}
				if now.Before(fireAt) {
					break // not due yet; later steps are even further out
				}
				body := renderSequenceBody(step.Body, &c, t.Name)
				o, err := svc.s.Outreach.Send(ctx, tenantID, SendOutreachInput{
					ClientID: c.ID, Purpose: purpose, Body: body,
					IdempotencyKey: key,
				})
				if err != nil {
					break
				}
				done[key] = true
				switch o.Status {
				case "sent", "scheduled":
					res.Sent++
				case "held":
					res.Held++
				case "failed":
					res.Failed++
				}
				// One new step per client per sweep is enough; the next
				// hourly run picks up whatever became due in the meantime.
				break
			}
		}
	}
	return res, nil
}

func renderSequenceBody(body string, c *model.Client, tenantName string) string {
	body = strings.ReplaceAll(body, "{{client_name}}", c.FullName)
	body = strings.ReplaceAll(body, "{{tenant_name}}", tenantName)
	return body
}
