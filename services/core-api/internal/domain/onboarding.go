package domain

// Tenant onboarding journey (Phase 27): the answer to "how does a new
// tenant get everything up and running?" as a derived, always-current
// checklist instead of a stored one.
//
// Design principle: step STATUSES ARE DERIVED FROM REAL RESOURCES, never
// stored — a checklist that persists its own state drifts from reality the
// first time someone edits the catalog directly. The only persisted bits
// are the playbook track (new | existing) and deliberately dismissed steps
// (OnboardingConfig.Skipped). Everything else is recomputed from the
// tenant's actual catalog, areas, brand, payment config, roster, promos
// and bookings — so the journey is honest by construction.
//
// Two playbooks:
//
//	new      — greenfield: build catalog, areas and site from scratch
//	           (the Phase 3 instant-on seeds a starter stack).
//	existing — brownfield: the business already runs elsewhere; the same
//	           steps but the actions are import/connect (POST
//	           /v1/import/catalog, /v1/import/clients, embed widget) rather
//	           than create. Verification is identical either way — imported
//	           services count exactly like hand-entered ones.

import (
	"context"
	"fmt"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type OnboardingStep struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // done | pending | skipped
	Detail string `json:"detail"` // what to do / current evidence
	Action string `json:"action"` // console path that completes the step
}

type OnboardingReport struct {
	Track       string           `json:"track"` // new | existing
	Steps       []OnboardingStep `json:"steps"`
	Done        int              `json:"done"`
	Total       int              `json:"total"` // non-skipped steps
	ProgressPct int              `json:"progress_pct"`
	NextStep    string           `json:"next_step,omitempty"` // first pending step id
	Complete    bool             `json:"complete"`
}

// Onboarding computes the tenant's current journey state.
func (svc *Services) Onboarding(ctx context.Context, tenantID string) (*OnboardingReport, error) {
	t, err := svc.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	track := t.OnboardingConfig.Track
	if track == "" {
		if t.Archetype == "new_entrant" {
			track = "new"
		} else {
			track = "existing"
		}
	}
	brownfield := track == "existing"

	services, err := svc.Store.Catalog().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	activeSvc := 0
	for _, s := range services {
		if s.Active {
			activeSvc++
		}
	}
	areas, err := svc.Store.Geo().Areas(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	bookings, err := svc.Store.Bookings().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	promos, err := svc.Store.Promos().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	_, hasTagline := t.Brand["tagline"].(string)
	waNumber, _ := t.Brand["whatsapp_number"].(string)
	sandboxWA, _ := t.Brand["sandbox_whatsapp"].(bool)

	step := func(id, title string, done bool, detail, action string) OnboardingStep {
		st := "pending"
		if done {
			st = "done"
		}
		for _, sk := range t.OnboardingConfig.Skipped {
			if sk == id && !done {
				st = "skipped"
			}
		}
		return OnboardingStep{ID: id, Title: title, Status: st, Detail: detail, Action: action}
	}

	catalogDetail := fmt.Sprintf("%d active services on your catalog.", activeSvc)
	if brownfield && activeSvc == 0 {
		catalogDetail = "Import your existing service menu (console wizard or POST /v1/import/catalog) — no re-typing."
	}
	areasDetail := fmt.Sprintf("%d service areas defined.", len(areas))
	if brownfield && len(areas) == 0 {
		areasDetail = "Draw the zones you already serve — geo serviceability gating applies from the first area."
	}
	payDetail := "Payments route by currency (Stripe EU/US/UK/CH rails, Paystack Africa)."
	if t.PaymentConfig.DisableStripe && t.PaymentConfig.DisablePaystack {
		payDetail = "BOTH payment rails are disabled — no client can pay. Re-enable at least one."
	} else if t.PricingRules.Currency == "" {
		payDetail = "Set your pricing currency so quotes and checkout can be issued."
	}
	waDetail := "WhatsApp sandbox channel active — map your production number (brand.whatsapp_number) when approved."
	if waNumber != "" {
		waDetail = "WhatsApp business number on file: " + waNumber + " — click-to-chat links are live."
	} else if !sandboxWA {
		waDetail = "No WhatsApp channel yet — the platform sandbox can take messages until your number is approved."
	}
	siteDetail := "Your SEO site is live at /site/" + t.Slug + "/ (landing + per-service + per-area pages, sitemap, JSON-LD)."
	if brownfield {
		siteDetail = "Keep your current website and embed the booking widget (/embed/" + t.Slug +
			") — or use the full SEO site at /site/" + t.Slug + "/. Both feed the same funnel."
	}

	steps := []OnboardingStep{
		step("profile", "Brand profile", hasTagline,
			map[bool]string{true: "Tagline and description set — they drive the SEO site and proposals.",
				false: "Set a tagline/description (Settings → brand) — it drives your public site and quotes."}[hasTagline],
			"/settings"),
		step("catalog", map[bool]string{true: "Import service catalog", false: "Create service catalog"}[brownfield],
			activeSvc > 0, catalogDetail, "/onboarding"),
		step("areas", "Service areas", len(areas) > 0, areasDetail, "/geo"),
		step("payments", "Payments",
			t.PricingRules.Currency != "" && !(t.PaymentConfig.DisableStripe && t.PaymentConfig.DisablePaystack),
			payDetail, "/settings"),
		step("whatsapp", "WhatsApp channel", waNumber != "" || sandboxWA, waDetail, "/onboarding"),
		step("team", "Operator roster", len(t.OperatorConfig.AlertPhones) > 0,
			fmt.Sprintf("%d operator phones receive SLA and acquisition alerts.", len(t.OperatorConfig.AlertPhones)),
			"/settings"),
		step("website", map[bool]string{true: "Website integration", false: "Public website"}[brownfield],
			true, siteDetail, "/onboarding"), // live by construction — the step is "know it exists"
		step("first_campaign", "First acquisition campaign", len(promos) > 0 || !t.RewardProgram.Disabled,
			fmt.Sprintf("%d promo campaigns; referral program %s.", len(promos),
				map[bool]string{true: "off", false: "on"}[t.RewardProgram.Disabled]),
			"/growth"),
		step("first_booking", "First booking", len(bookings) > 0,
			fmt.Sprintf("%d bookings on record.", len(bookings)), "/dispatch"),
		step("go_live", "90-day acquisition goal", t.AcquisitionConfig.CohortStartedAt != "" || len(bookings) > 0,
			"Cohort tracking paces you toward the new-client goal with Monday pacing alerts.",
			"/growth"),
	}

	rep := &OnboardingReport{Track: track, Steps: steps}
	for _, s := range steps {
		if s.Status == "skipped" {
			continue
		}
		rep.Total++
		if s.Status == "done" {
			rep.Done++
		} else if rep.NextStep == "" {
			rep.NextStep = s.ID
		}
	}
	if rep.Total > 0 {
		rep.ProgressPct = rep.Done * 100 / rep.Total
	}
	rep.Complete = rep.Total > 0 && rep.Done == rep.Total
	return rep, nil
}

// UpdateOnboarding sets the playbook track and/or dismisses/reinstates
// steps. Statuses are never written — they re-derive on the next read.
func (svc *Services) UpdateOnboarding(ctx context.Context, tenantID, track string, skip, unskip []string) (*model.Tenant, error) {
	t, err := svc.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if track != "" {
		if track != "new" && track != "existing" {
			return nil, httpx.ErrValidation
		}
		t.OnboardingConfig.Track = track
	}
	known := map[string]bool{"profile": true, "catalog": true, "areas": true, "payments": true,
		"whatsapp": true, "team": true, "website": true, "first_campaign": true, "first_booking": true, "go_live": true}
	for _, id := range skip {
		id = strings.TrimSpace(id)
		if !known[id] {
			return nil, httpx.ErrValidation
		}
		found := false
		for _, s := range t.OnboardingConfig.Skipped {
			if s == id {
				found = true
			}
		}
		if !found {
			t.OnboardingConfig.Skipped = append(t.OnboardingConfig.Skipped, id)
		}
	}
	if len(unskip) > 0 {
		keep := t.OnboardingConfig.Skipped[:0]
		for _, s := range t.OnboardingConfig.Skipped {
			drop := false
			for _, u := range unskip {
				if s == strings.TrimSpace(u) {
					drop = true
				}
			}
			if !drop {
				keep = append(keep, s)
			}
		}
		t.OnboardingConfig.Skipped = keep
	}
	if err := svc.Store.Tenants().UpdateConfig(ctx, t); err != nil {
		return nil, err
	}
	svc.Events.Emit(ctx, tenantID, "tenant", tenantID, "onboarding.updated",
		map[string]any{"track": t.OnboardingConfig.Track, "skipped": t.OnboardingConfig.Skipped})
	return t, nil
}
