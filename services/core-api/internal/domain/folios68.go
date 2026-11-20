package domain

// Phase 68: partner-portal folio visibility. Phase 57 gave the partner a
// magic-link portal for offers and commissions; Phase 66/67 turned desk
// billing into statements with collection evidence — but the partner
// could only hear about a statement over WhatsApp. Now the portal itself
// shows the partner their own folios: what is accruing (OPEN), what is
// payable (ISSUED, with the due date and days past due derived from the
// tenant's payment terms), and what is paid (SETTLED) — with the
// verbatim reminders ledger, so the partner sees the same collection
// history the operator does. Due/overdue are derived on read, never
// stored, so the portal can never disagree with the operator's aging.

import (
	"context"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// PortalFolio is the partner's own statement as they see it: the frozen
// total and lines, the derived due date and days past due when issued,
// and every reminder the folio has accrued — verbatim.
type PortalFolio struct {
	ID          string                `json:"id"`
	Period      string                `json:"period"`
	Currency    string                `json:"currency"`
	State       string                `json:"state"` // OPEN (accruing) | ISSUED (payable) | SETTLED (paid)
	TotalMinor  int64                 `json:"total_minor"`
	Lines       []model.FolioLine     `json:"lines,omitempty"`
	IssuedAt    *time.Time            `json:"issued_at,omitempty"`
	SettledAt   *time.Time            `json:"settled_at,omitempty"`
	DueAt       *time.Time            `json:"due_at,omitempty"` // ISSUED only — derived, not stored
	OverdueDays int                   `json:"overdue_days"`     // ISSUED only; <= 0 means inside terms
	Reminders   []model.FolioReminder `json:"reminders,omitempty"`
}

// PortalFolios builds the partner's statement list for the portal,
// newest period first (store order). Terms come from the tenant config
// so the due date the partner sees is the same one aging and dunning use.
func (svc *FolioService) PortalFolios(ctx context.Context, tenantID, partnerID string) ([]PortalFolio, error) {
	tenant, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	terms := tenant.PricingRules.FolioTerms()
	folios, err := svc.s.Store.Folios().ListForPartner(ctx, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	now := svc.s.Now()
	out := make([]PortalFolio, 0, len(folios))
	for i := range folios {
		f := &folios[i]
		pf := PortalFolio{
			ID: f.ID, Period: f.Period, Currency: f.Currency, State: f.State,
			TotalMinor: f.TotalMinor, Lines: f.Lines, Reminders: f.Reminders,
		}
		if !f.IssuedAt.IsZero() {
			t := f.IssuedAt
			pf.IssuedAt = &t
		}
		if !f.SettledAt.IsZero() {
			t := f.SettledAt
			pf.SettledAt = &t
		}
		if f.State == model.FolioIssued {
			d := f.DueAt(terms)
			pf.DueAt = &d
			pf.OverdueDays = f.OverdueDays(now, terms)
		}
		out = append(out, pf)
	}
	return out, nil
}
