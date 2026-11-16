package domain

// Phase 46: data retention & minimization engine (GDPR Art. 5(1)(e) —
// storage limitation). Until now the platform kept everything forever:
// geo stamps, message bodies, attribution events. That fails the one GDPR
// pillar we had not closed, and location history is the most sensitive
// data we hold.
//
// Per-tenant posture lives on Tenant.RetentionConfig; zero values resolve
// to the platform defaults below so a tenant that never thinks about
// retention still gets a defensible posture. Financial records (quotes,
// bookings, payments, ledger postings), the consent evidence ledger, and
// the audit log are EXEMPT — accounting retention and the accountability
// principle outrank minimization for those categories.

import (
	"context"
	"fmt"
)

// Platform retention defaults (days).
const (
	RetentionDefaultGeoStampsDays     = 90
	RetentionDefaultMessageBodiesDays = 730
	RetentionDefaultAttributionDays   = 730
)

// RetentionScrubMarker replaces a purged message body. It is deliberately
// stable and provenance-labeled: a scrubbed timeline reads as a retention
// action, not a deletion bug, and idempotency keys off it.
const RetentionScrubMarker = "[content removed by data retention policy]"

type RetentionService struct{ s *Services }

// RetentionResult is one sweep's counts, returned to the operator endpoint
// and mirrored into the audit log.
type RetentionResult struct {
	GeoStampsPurged       int  `json:"geo_stamps_purged"`
	MessageBodiesScrubbed int  `json:"message_bodies_scrubbed"`
	AttributionPurged     int  `json:"attribution_purged"`
	Disabled              bool `json:"disabled"`
}

// resolveDays applies the per-category semantics: 0 = platform default,
// negative = never purge this category, positive = that many days.
func resolveDays(configured, platformDefault int) int {
	if configured == 0 {
		return platformDefault
	}
	return configured
}

// Run executes one retention sweep for a tenant and audit-logs the counts.
// Honoring Disabled and per-category negative values makes the sweep safe
// to run hourly — it is also idempotent, so the manual operator endpoint
// can fire it on demand.
func (svc *RetentionService) Run(ctx context.Context, tenantID string) (*RetentionResult, error) {
	t, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	cfg := t.RetentionConfig
	res := &RetentionResult{Disabled: cfg.Disabled}
	if cfg.Disabled {
		return res, nil
	}
	now := svc.s.Now()

	if days := resolveDays(cfg.GeoStampsDays, RetentionDefaultGeoStampsDays); days > 0 {
		n, err := svc.s.Store.Dispatch().PurgeGeoStampsBefore(ctx, tenantID, now.AddDate(0, 0, -days))
		if err != nil {
			return nil, fmt.Errorf("purge geostamps: %w", err)
		}
		res.GeoStampsPurged = n
	}

	if days := resolveDays(cfg.MessageBodiesDays, RetentionDefaultMessageBodiesDays); days > 0 {
		n, err := svc.s.Store.Conversations().ScrubMessageBodiesBefore(ctx, tenantID,
			now.AddDate(0, 0, -days), RetentionScrubMarker)
		if err != nil {
			return nil, fmt.Errorf("scrub message bodies: %w", err)
		}
		res.MessageBodiesScrubbed = n
	}

	if days := resolveDays(cfg.AttributionDays, RetentionDefaultAttributionDays); days > 0 {
		n, err := svc.s.Store.Attribution().PurgeBefore(ctx, tenantID, now.AddDate(0, 0, -days))
		if err != nil {
			return nil, fmt.Errorf("purge attribution events: %w", err)
		}
		res.AttributionPurged = n
	}

	svc.s.Audit.Log(ctx, tenantID, "system:retention", "retention.sweep", "tenant", tenantID,
		map[string]any{
			"geo_stamps_purged":       res.GeoStampsPurged,
			"message_bodies_scrubbed": res.MessageBodiesScrubbed,
			"attribution_purged":      res.AttributionPurged,
		})
	return res, nil
}

// scrubClientMessages is the Art. 17 erasure companion: an erased client's
// conversation content goes immediately, without waiting for the retention
// window. Advisory fail-open — erasure itself has already committed.
func (svc *RetentionService) scrubClientMessages(ctx context.Context, tenantID, clientID string) {
	if _, err := svc.s.Store.Conversations().ScrubMessageBodiesForClient(ctx, tenantID, clientID, RetentionScrubMarker); err != nil {
		svc.s.Events.Emit(ctx, tenantID, "retention", clientID, "retention.scrub_failed",
			map[string]any{"client_id": clientID})
	}
}
