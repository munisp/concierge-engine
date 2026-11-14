package domain

import (
	"context"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// ReceiptsService (Phase 11): channel delivery/read receipts as SLA
// evidence. Status callbacks arrive from the channel adapters (Meta
// statuses[], Infobip delivery reports) keyed by the channel's message id
// — the same external_ref SendReply records when it sends.

var receiptStatuses = map[string]bool{
	"sent": true, "delivered": true, "read": true, "failed": true,
}

type ReceiptsService struct{ s *Services }

func NewReceiptsService(s *Services) *ReceiptsService { return &ReceiptsService{s} }

// Record a receipt. Idempotent per (external_ref, status) at the store
// level, so webhook redelivery is free. Unknown statuses are rejected —
// the audit trail only carries vocabulary we understand.
func (svc *ReceiptsService) Record(ctx context.Context, tenantID, channel, externalRef, status string, at time.Time) (*model.MessageReceipt, error) {
	if tenantID == "" || externalRef == "" || !receiptStatuses[status] {
		return nil, httpx.ErrValidation
	}
	if channel == "" {
		channel = "whatsapp"
	}
	if at.IsZero() {
		at = svc.s.Now()
	}
	rc := &model.MessageReceipt{
		ID: NewID(), TenantID: tenantID, Channel: channel,
		ExternalRef: externalRef, Status: status, At: at, CreatedAt: svc.s.Now(),
	}
	if err := svc.s.Store.Receipts().AddReceipt(ctx, rc); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "channel", externalRef, "channel.receipt",
		map[string]any{"channel": rc.Channel, "status": status})
	return rc, nil
}

// For: the receipt timeline for one channel message id.
func (svc *ReceiptsService) For(ctx context.Context, tenantID, externalRef string) ([]model.MessageReceipt, error) {
	if externalRef == "" {
		return nil, httpx.ErrValidation
	}
	return svc.s.Store.Receipts().Receipts(ctx, tenantID, externalRef)
}

func (svc *ReceiptsService) Counts(ctx context.Context, tenantID string) (map[string]int, error) {
	return svc.s.Store.Receipts().ReceiptCounts(ctx, tenantID)
}
