// Audit service (Master Spec section 13): access audit logs on every
// sensitive read. Append-only, tenant-scoped, best-effort (an audit backend
// hiccup must never break a client-facing read — the entry is emitted to the
// outbox as well so the lakehouse captures it durably).
package domain

import (
	"context"

	"concierge-engine/services/core-api/internal/model"
)

type AuditService struct{ s *Services }

// Log records one sensitive access. Errors are swallowed into an outbox
// event so the primary read path is never blocked by audit persistence.
func (svc *AuditService) Log(ctx context.Context, tenantID, actor, action, resource, resourceID string, detail map[string]any) {
	entry := &model.AuditEntry{
		TenantID: tenantID, Actor: actor, Action: action,
		Resource: resource, ResourceID: resourceID, Detail: detail,
		At: svc.s.Now(),
	}
	if err := svc.s.Store.Audit().Log(ctx, entry); err != nil {
		svc.s.Events.Emit(ctx, tenantID, "audit", resourceID, "audit.write_failed",
			map[string]any{"action": action, "resource": resource})
	}
}

func (svc *AuditService) List(ctx context.Context, tenantID string, limit int) ([]model.AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	return svc.s.Store.Audit().List(ctx, tenantID, limit)
}
