package domain

import (
	"context"
	"fmt"
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
	svc.alertLinkedCase(ctx, tenantID, rc)
	return rc, nil
}

// alertLinkedCase (Phase 12): a receipt for a SendReply-linked channel
// message is evidence about a case the moment it lands — a failed delivery
// means the client never got an approved reply while the SLA clock runs,
// and a read is proof the client saw it. Both annotate the case; a failure
// also pulls a waiting case back to in-progress (you cannot wait on a
// client who never received the message) and escalates. Advisory: an
// alerting failure never rejects the receipt — the evidence is stored.
func (svc *ReceiptsService) alertLinkedCase(ctx context.Context, tenantID string, rc *model.MessageReceipt) {
	if rc.Status != "failed" && rc.Status != "read" {
		return // sent/delivered are stored evidence, not alerts
	}
	caseID, err := svc.s.Store.Receipts().LinkedCase(ctx, tenantID, rc.ExternalRef)
	if err != nil || caseID == "" {
		return
	}
	c, err := svc.s.Store.Cases().Get(ctx, tenantID, caseID)
	if err != nil {
		return
	}
	if rc.Status == "read" {
		_, _ = svc.s.Cases.AddNote(ctx, tenantID, caseID, "channel:"+rc.Channel,
			fmt.Sprintf("client read the reply (ref %s) at %s", rc.ExternalRef, rc.At.Format(time.RFC3339)))
		svc.s.Events.Emit(ctx, tenantID, "case", caseID, "case.reply_read",
			map[string]any{"external_ref": rc.ExternalRef, "at": rc.At})
		return
	}
	// failed: the approved reply never arrived.
	_, _ = svc.s.Cases.AddNote(ctx, tenantID, caseID, "channel:"+rc.Channel,
		fmt.Sprintf("DELIVERY FAILED for approved reply (ref %s) — the client never received it; resend advised", rc.ExternalRef))
	changed := false
	if c.State == model.CaseWaitingClient {
		c.State = model.CaseInProgress
		changed = true
	}
	if !c.Escalated {
		c.Escalated = true // a failed reply under SLA is an escalation by definition
		changed = true
	}
	if changed {
		c.UpdatedAt = svc.s.Now()
		if err := svc.s.Store.Cases().Update(ctx, c); err != nil {
			return
		}
	}
	svc.s.Events.Emit(ctx, tenantID, "case", caseID, "case.reply_failed",
		map[string]any{"external_ref": rc.ExternalRef, "state": c.State})
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

// ---------------------------------------------------------- Phase 12 SLA board

// Link records external_ref -> case (called by SendReply when the channel
// returns a message id). Idempotent: first link wins.
func (svc *ReceiptsService) Link(ctx context.Context, tenantID, externalRef, caseID string) error {
	if tenantID == "" || externalRef == "" || caseID == "" {
		return httpx.ErrValidation
	}
	return svc.s.Store.Receipts().LinkReceipt(ctx, tenantID, externalRef, caseID)
}

// FailureRow is one failed delivery on the SLA board, with the case it
// belongs to ("" when the ref was never linked — e.g. template sends).
type FailureRow struct {
	Receipt model.MessageReceipt `json:"receipt"`
	CaseID  string               `json:"case_id,omitempty"`
}

// Failures: recent failed deliveries, newest first, correlated to cases.
func (svc *ReceiptsService) Failures(ctx context.Context, tenantID string, limit int) ([]FailureRow, error) {
	rcs, err := svc.s.Store.Receipts().ReceiptsByStatus(ctx, tenantID, "failed", limit)
	if err != nil {
		return nil, err
	}
	out := []FailureRow{}
	for _, rc := range rcs {
		caseID, err := svc.s.Store.Receipts().LinkedCase(ctx, tenantID, rc.ExternalRef)
		if err != nil {
			caseID = "" // advisory: a broken link never hides the failure
		}
		out = append(out, FailureRow{Receipt: rc, CaseID: caseID})
	}
	return out, nil
}

// CaseReceiptTimeline is the full delivery evidence for one case: every
// linked channel message id with its ordered receipt lifecycle.
type CaseReceiptTimeline struct {
	CaseID   string                            `json:"case_id"`
	Messages map[string][]model.MessageReceipt `json:"messages"` // external_ref -> lifecycle
}

// ForCase: all receipts for every channel message linked to a case.
func (svc *ReceiptsService) ForCase(ctx context.Context, tenantID, caseID string) (*CaseReceiptTimeline, error) {
	if caseID == "" {
		return nil, httpx.ErrValidation
	}
	refs, err := svc.s.Store.Receipts().RefsForCase(ctx, tenantID, caseID)
	if err != nil {
		return nil, err
	}
	out := &CaseReceiptTimeline{CaseID: caseID, Messages: map[string][]model.MessageReceipt{}}
	for _, ref := range refs {
		rcs, err := svc.s.Store.Receipts().Receipts(ctx, tenantID, ref)
		if err != nil {
			continue // advisory: one broken ref doesn't void the timeline
		}
		out.Messages[ref] = rcs
	}
	return out, nil
}
