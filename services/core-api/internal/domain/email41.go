package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// Phase 41 — email list hygiene: hard-bounce suppression + engagement
// sunsetting.
//
// Suppression: once the ESP reports a hard bounce (or a complaint, or an
// operator blocks an address manually), the address lands on the tenant's
// suppression list and the outreach verdict holds every future email to it —
// service lane included, because a dead address delivers nothing on either
// lane. The Phase 40 guard pauses a tenant whose reputation is burning; the
// suppression list removes the single biggest bounce driver (re-mailing
// known-dead addresses) so the guard stays inert. DELETE is the operator
// recovery path for addresses that come back to life.
//
// Sunsetting: a client who has been sent EmailSunsetMinSends or more
// marketing emails without EVER opening or clicking one is "never engaged"
// and the verdict holds further marketing email — chronic non-openers are
// tomorrow's spam complaints, and every send to them dilutes the
// engagement metrics mailbox providers use to score the tenant. A single
// open or click (attribution, any age) resets the sunset permanently.
// Service-lane email is exempt: invoices and reminders are owed regardless
// of marketing engagement.

// EmailSunsetMinSends is the all-time marketing-email send count at which a
// client with zero recorded opens/clicks sunsets out of the marketing lane.
const EmailSunsetMinSends = 8

type SuppressInput struct {
	Email     string `json:"email"`
	Reason    string `json:"reason"` // hard_bounce | complaint | manual
	SourceRef string `json:"source_ref,omitempty"`
}

// Suppress adds an address to the tenant suppression list. Replays (same
// normalized address) return the existing row with created=false so ESP
// webhooks that retry are idempotent.
func (svc *OutreachService) Suppress(ctx context.Context, tenantID string, in SuppressInput) (model.EmailSuppression, bool, error) {
	email, err := normalizeSuppressionEmail(in.Email)
	if err != nil {
		return model.EmailSuppression{}, false, err
	}
	switch in.Reason {
	case model.SuppressHardBounce, model.SuppressComplaint, model.SuppressManual:
	default:
		return model.EmailSuppression{}, false, fmt.Errorf("%w: reason must be hard_bounce, complaint or manual", httpx.ErrValidation)
	}
	row := model.EmailSuppression{
		TenantID:  tenantID,
		Email:     email,
		Reason:    in.Reason,
		SourceRef: strings.TrimSpace(in.SourceRef),
	}
	if err := svc.s.Store.EmailSuppressions().Add(ctx, &row); err != nil {
		if errors.Is(err, httpx.ErrConflict) {
			for _, existing := range mustSuppressions(ctx, svc, tenantID) {
				if existing.Email == email {
					return existing, false, nil
				}
			}
		}
		return model.EmailSuppression{}, false, err
	}
	svc.s.Audit.Log(ctx, tenantID, "api", "email.suppressed", "email_suppression", row.ID,
		map[string]any{"email": email, "reason": row.Reason, "source_ref": row.SourceRef})
	return row, true, nil
}

// Unsuppress removes an address from the list (operator recovery).
func (svc *OutreachService) Unsuppress(ctx context.Context, tenantID, email string) error {
	norm, err := normalizeSuppressionEmail(email)
	if err != nil {
		return err
	}
	if err := svc.s.Store.EmailSuppressions().Delete(ctx, tenantID, norm); err != nil {
		return err
	}
	svc.s.Audit.Log(ctx, tenantID, "api", "email.unsuppressed", "email_suppression", norm, nil)
	return nil
}

func (svc *OutreachService) Suppressions(ctx context.Context, tenantID string, limit int) ([]model.EmailSuppression, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	return svc.s.Store.EmailSuppressions().List(ctx, tenantID, limit)
}

// sunsetApplies reports whether a never-engaged client has hit the marketing
// sunset threshold. Advisory: store failures fail open (returns false).
func (svc *OutreachService) sunsetApplies(ctx context.Context, tenantID, clientID string) bool {
	if clientID == "" {
		return false
	}
	sent, err := svc.s.Store.Outreach().SendsForClient(ctx, tenantID, clientID, time.Time{})
	if err != nil {
		return false
	}
	sentCount := 0
	for _, s := range sent {
		if s.Channel == model.OutreachChannelEmail && s.Purpose == model.OutreachMarketing && s.Status == "sent" {
			sentCount++
		}
	}
	if sentCount < EmailSunsetMinSends {
		return false
	}
	events, err := svc.s.Store.Attribution().List(ctx, tenantID, clientID, 500)
	if err != nil {
		return false
	}
	for _, ev := range events {
		if ev.Kind == "email_opened" || ev.Kind == "email_clicked" {
			return false // any engagement ever resets the sunset
		}
	}
	return true
}

func normalizeSuppressionEmail(email string) (string, error) {
	norm := strings.ToLower(strings.TrimSpace(email))
	if norm == "" || !strings.Contains(norm, "@") {
		return "", fmt.Errorf("%w: email must contain @", httpx.ErrValidation)
	}
	return norm, nil
}

func mustSuppressions(ctx context.Context, svc *OutreachService, tenantID string) []model.EmailSuppression {
	rows, err := svc.s.Store.EmailSuppressions().List(ctx, tenantID, 500)
	if err != nil {
		return nil
	}
	return rows
}
