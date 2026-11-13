// Partner network (Phase 2): vetted supplier directory, quality scoring and
// fill-rate metrics. Partner fulfillment reuses the dispatch assignment path
// (Assignment.PartnerID) — the network adds vetting, scoring and settlement.
package domain

import (
	"context"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type PartnerService struct{ s *Services }

var partnerStatuses = map[string]bool{"PENDING": true, "VETTED": true, "SUSPENDED": true}

var payoutRails = map[string]bool{"MOJALOOP": true, "MANUAL": true}

func (svc *PartnerService) Create(ctx context.Context, p *model.Partner) (*model.Partner, error) {
	if p.Name == "" {
		return nil, httpx.ErrValidation
	}
	if p.ID == "" {
		p.ID = NewID()
	}
	if p.Status == "" {
		p.Status = "PENDING" // vetting is an explicit human step
	}
	if !partnerStatuses[p.Status] {
		return nil, httpx.ErrValidation
	}
	if p.PayoutRail == "" {
		p.PayoutRail = "MANUAL"
	}
	if !payoutRails[p.PayoutRail] {
		return nil, httpx.ErrValidation
	}
	if p.QualityScore == 0 {
		p.QualityScore = 1.0
	}
	p.CreatedAt = svc.s.Now()
	if err := svc.s.Store.Partners().Create(ctx, p); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, p.TenantID, "partner", p.ID, "partner.registered",
		map[string]any{"verticals": p.Verticals, "markets": p.Markets})
	return p, nil
}

func (svc *PartnerService) List(ctx context.Context, tenantID string) ([]model.Partner, error) {
	return svc.s.Store.Partners().List(ctx, tenantID)
}

func (svc *PartnerService) Get(ctx context.Context, tenantID, id string) (*model.Partner, error) {
	return svc.s.Store.Partners().Get(ctx, tenantID, id)
}

// SetStatus transitions vetting status; VETTED partners become routable for
// dispatch and payable via payouts.
func (svc *PartnerService) SetStatus(ctx context.Context, tenantID, id, status string) (*model.Partner, error) {
	status = strings.ToUpper(status)
	if !partnerStatuses[status] {
		return nil, httpx.ErrValidation
	}
	p, err := svc.s.Store.Partners().Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	p.Status = status
	if err := svc.s.Store.Partners().Update(ctx, p); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "partner", id, "partner.status_changed", map[string]any{"status": status})
	return p, nil
}

// PartnerMetrics: fill rate and quality for the network KPI (Spec section 4:
// "partner fill rate" is a north-star KPI).
type PartnerMetrics struct {
	TotalAssignments   int     `json:"total_assignments"`
	PartnerAssignments int     `json:"partner_assignments"`
	FillRate           float64 `json:"fill_rate"` // partner-filled share of all assignments
	VettedPartners     int     `json:"vetted_partners"`
	AvgQualityScore    float64 `json:"avg_quality_score"`
}

func (svc *PartnerService) Metrics(ctx context.Context, tenantID string) (*PartnerMetrics, error) {
	assignments, err := svc.s.Store.Dispatch().ListAssignments(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	partners, err := svc.s.Store.Partners().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	m := &PartnerMetrics{TotalAssignments: len(assignments)}
	for _, a := range assignments {
		if a.PartnerID != "" {
			m.PartnerAssignments++
		}
	}
	if m.TotalAssignments > 0 {
		m.FillRate = float64(m.PartnerAssignments) / float64(m.TotalAssignments)
	}
	var qualitySum float64
	for _, p := range partners {
		if p.Status == "VETTED" {
			m.VettedPartners++
			qualitySum += p.QualityScore
		}
	}
	if m.VettedPartners > 0 {
		m.AvgQualityScore = qualitySum / float64(m.VettedPartners)
	}
	return m, nil
}
