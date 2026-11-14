package domain

// Brownfield import (Phase 27): the "existing customer already has some of
// the needed features" track. A concierge business that already runs on
// spreadsheets, a booking tool or a WhatsApp number should never re-type
// anything — they import what exists and the platform integrates it.
//
// Imports are:
//   - idempotent — re-running the same file creates nothing twice (catalog
//     dedupes by name, clients by phone), so a failed upload is safe to
//     retry and a monthly re-sync is safe to schedule;
//   - honest about consent — imported clients arrive with marketing
//     consent FALSE (you cannot import consent you never collected), an
//     "imported" marker, and a first-touch attribution channel "import"
//     so they never count as platform acquisitions (no acquisition fee on
//     a book of business the tenant already had);
//   - additive — import never deletes or overwrites existing records.

import (
	"context"
	"strconv"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type ImportCatalogItem struct {
	Vertical     string `json:"vertical"`
	Name         string `json:"name"`
	BaseMinor    int64  `json:"base_minor"`
	Currency     string `json:"currency,omitempty"` // default: tenant pricing currency
	PerKMMinor   int64  `json:"per_km_minor,omitempty"`
	PerHourMinor int64  `json:"per_hour_minor,omitempty"`
	AutoQuotable bool   `json:"auto_quotable"`
}

type ImportResult struct {
	Created int      `json:"created"`
	Skipped int      `json:"skipped"` // duplicates (name / phone match)
	Errors  []string `json:"errors,omitempty"`
}

// ImportCatalog bulk-imports an existing service menu. Duplicate names
// (case-insensitive) are skipped, not duplicated.
func (svc *Services) ImportCatalog(ctx context.Context, tenantID string, items []ImportCatalogItem) (*ImportResult, error) {
	if len(items) == 0 {
		return nil, httpx.ErrValidation
	}
	t, err := svc.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	existing, err := svc.Store.Catalog().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, s := range existing {
		seen[strings.ToLower(s.Name)] = true
	}
	res := &ImportResult{}
	for i, in := range items {
		in.Name = strings.TrimSpace(in.Name)
		key := strings.ToLower(in.Name)
		if in.Name == "" || in.Vertical == "" {
			res.Errors = append(res.Errors, errRow(i, "name and vertical are required"))
			continue
		}
		if seen[key] {
			res.Skipped++
			continue
		}
		cur := strings.ToUpper(strings.TrimSpace(in.Currency))
		if cur == "" {
			cur = t.PricingRules.Currency
		}
		if _, err := svc.Catalog.Add(ctx, &model.CatalogService{
			TenantID: tenantID, Vertical: in.Vertical, Name: in.Name,
			BaseMinor: in.BaseMinor, Currency: cur,
			PerKMMinor: in.PerKMMinor, PerHourMinor: in.PerHourMinor,
			AutoQuotable: in.AutoQuotable,
		}); err != nil {
			res.Errors = append(res.Errors, errRow(i, err.Error()))
			continue
		}
		seen[key] = true
		res.Created++
	}
	if res.Created > 0 {
		svc.Events.Emit(ctx, tenantID, "tenant", tenantID, "import.catalog",
			map[string]any{"created": res.Created, "skipped": res.Skipped})
	}
	return res, nil
}

type ImportClientItem struct {
	FullName string `json:"full_name,omitempty"`
	Phone    string `json:"phone"`
	Email    string `json:"email,omitempty"`
	Locale   string `json:"locale,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// ImportClients bulk-imports an existing client book. Dedupe key is the
// phone number (the platform's canonical contact anchor). Imported clients
// get marketing consent OFF — consent cannot be imported, only collected —
// and an attribution first touch of channel "import", which is deliberately
// NOT an acquisition channel: the platform never takes an acquisition fee
// on clients the tenant brought with them.
func (svc *Services) ImportClients(ctx context.Context, tenantID string, items []ImportClientItem) (*ImportResult, error) {
	if len(items) == 0 {
		return nil, httpx.ErrValidation
	}
	res := &ImportResult{}
	for i, in := range items {
		in.Phone = strings.TrimSpace(in.Phone)
		if in.Phone == "" {
			res.Errors = append(res.Errors, errRow(i, "phone is required (the dedupe anchor)"))
			continue
		}
		if _, err := svc.Store.Clients().FindByPhone(ctx, tenantID, in.Phone); err == nil {
			res.Skipped++
			continue
		}
		consent := ConsentDefaults(in.Phone)
		consent["marketing"] = false // cannot import consent — collect on first contact
		consent["imported"] = true
		c := &model.Client{
			ID: NewID(), TenantID: tenantID,
			FullName: strings.TrimSpace(in.FullName), Phone: in.Phone,
			Email: strings.TrimSpace(in.Email), Locale: in.Locale, Timezone: in.Timezone,
			KYCStatus: "none", Consent: consent, CreatedAt: svc.Now(),
		}
		if err := svc.Store.Clients().Upsert(ctx, c); err != nil {
			res.Errors = append(res.Errors, errRow(i, err.Error()))
			continue
		}
		svc.Events.Track(ctx, &model.AttributionEvent{
			TenantID: tenantID, Kind: "imported", Channel: "import",
			Campaign: "import:client_book", ClientID: c.ID,
		})
		res.Created++
	}
	if res.Created > 0 {
		svc.Events.Emit(ctx, tenantID, "tenant", tenantID, "import.clients",
			map[string]any{"created": res.Created, "skipped": res.Skipped})
	}
	return res, nil
}

func errRow(i int, msg string) string {
	return "row " + strconv.Itoa(i+1) + ": " + msg
}
