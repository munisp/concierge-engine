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
	Group  string `json:"group"` // essentials | operations | presence | growth
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
	svcWithImg := 0
	for _, s := range services {
		if s.Active {
			activeSvc++
			if s.ImageID != "" {
				svcWithImg++
			}
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
	// Phase 32: the journey now covers the FULL business setup, not just
	// go-to-market — fleet, drivers, pricing posture, tax, dispatch tuning,
	// site design, imagery, socials, templates, lifecycle. Everything is
	// still derived from real resources on every read.
	fleet, err := svc.Store.Dispatch().ListFleet(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	crew, err := svc.Store.Dispatch().ListCrew(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	templates, err := svc.Store.Templates().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	media, err := svc.Store.Media().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	activeFleet := 0
	for _, f := range fleet {
		if f.Active {
			activeFleet++
		}
	}
	activeCrew := 0
	for _, c := range crew {
		if c.Active {
			activeCrew++
		}
	}
	liveRails := svc.Payments.LiveRails(t.PaymentConfig)

	_, hasTagline := t.Brand["tagline"].(string)
	waNumber, _ := t.Brand["whatsapp_number"].(string)
	sandboxWA, _ := t.Brand["sandbox_whatsapp"].(bool)

	step := func(id, group, title string, done bool, detail, action string) OnboardingStep {
		st := "pending"
		if done {
			st = "done"
		}
		for _, sk := range t.OnboardingConfig.Skipped {
			if sk == id && !done {
				st = "skipped"
			}
		}
		return OnboardingStep{ID: id, Group: group, Title: title, Status: st, Detail: detail, Action: action}
	}

	catalogDetail := fmt.Sprintf("%d active services on your catalog.", activeSvc)
	if brownfield && activeSvc == 0 {
		catalogDetail = "Import your existing service menu (console wizard or POST /v1/import/catalog) — no re-typing."
	}
	areasDetail := fmt.Sprintf("%d service areas defined.", len(areas))
	if brownfield && len(areas) == 0 {
		areasDetail = "Draw the zones you already serve — geo serviceability gating applies from the first area."
	}
	// Payments (Phase 32 honesty fix): a rail counts only when the platform
	// holds live PSP credentials AND the tenant hasn't disabled it — flag
	// state alone used to mark this step done on sandbox-only instances.
	payDetail := "Payments route by currency (Stripe EU/US/UK/CH rails, Paystack Africa)."
	switch {
	case t.PricingRules.Currency == "":
		payDetail = "Set your pricing currency so quotes and checkout can be issued."
	case t.PaymentConfig.DisableStripe && t.PaymentConfig.DisablePaystack:
		payDetail = "BOTH payment rails are disabled — no client can pay. Re-enable at least one."
	case len(liveRails) == 0:
		payDetail = "No live payment rail on this platform instance (no PSP keys configured) — payment links run in SANDBOX mode. Platform operations sets CE_STRIPE_KEY / CE_PAYSTACK_KEY to go live."
	default:
		payDetail = fmt.Sprintf("Live rails: %s — platform-managed payments; settlement posts to your ledger automatically.", strings.Join(liveRails, " + "))
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

	pricingDetail := fmt.Sprintf("Deposit %.0f%%, quote TTL %ds, platform fee %.1f%%.",
		t.PricingRules.DepositPct*100, t.PricingRules.QuoteTTLSeconds, t.PricingRules.PlatformFeePct*100)
	if t.PricingRules.DepositPct <= 0 {
		pricingDetail = "No deposit configured — bookings confirm with zero commitment and no-shows cost you fleet hours. Set deposit_pct (Settings → pricing rules)."
	}
	taxDetail := "Tax engine explicitly set."
	if t.TaxConfig.Engine == "" {
		taxDetail = "No tax posture chosen — pick none (not registered), flat (fixed VAT/GST rate) or stripe-tax (live calculation) in Settings. Quotes issue without a tax line until you do."
	}
	fleetDetail := fmt.Sprintf("%d active fleet resources.", activeFleet)
	if brownfield && activeFleet == 0 {
		fleetDetail = "Add the vehicles/vessels you already run (Dispatch page or POST /v1/dispatch/fleet) — dispatch can't assign what it can't see."
	}
	crewDetail := fmt.Sprintf("%d active drivers/crew.", activeCrew)
	if brownfield && activeCrew == 0 {
		crewDetail = "Add your drivers (Dispatch page or POST /v1/dispatch/crew) — certificates tracked for compliance."
	}
	dispatchDetail := fmt.Sprintf("Default trip duration %dm, turnaround buffer %dm.",
		t.DispatchConfig.DefaultDurationMinutes, t.DispatchConfig.TurnaroundMinutes)
	if t.DispatchConfig.DefaultDurationMinutes == 0 {
		dispatchDetail = "Scheduling falls back to a 120m default window. Set your real default duration + turnaround buffer (Settings → dispatch) so availability gating matches your operation."
	}
	designDetail := "A design preset is applied."
	if t.SiteConfig.Theme == nil || t.SiteConfig.Theme.Preset == "" {
		designDetail = "Your site renders the default look — pick a preset and layout in the Studio (takes a minute, lands instantly)."
	}
	imageryDetail := fmt.Sprintf("%d media assets uploaded.", len(media))
	if t.SiteConfig.HeroImageID == "" {
		imageryDetail = "No hero image — the hero falls back to a flat gradient. Upload real photography (or generate it) in the Studio; it is the single biggest trust signal on the page."
	}
	// Phase 33: per-service card imagery is its own step — the hero alone
	// used to mark "imagery" done while every service card rendered a
	// monogram tile.
	svcImgDetail := fmt.Sprintf("%d of %d active services have a card image.", svcWithImg, activeSvc)
	if activeSvc == 0 {
		svcImgDetail = "Add services first — card images are attached per service on the Catalog page."
	}
	socialDetail := fmt.Sprintf("%d social profiles linked (feeds link-in-bio + search-entity graph).", len(t.SiteConfig.SocialLinks))
	if len(t.SiteConfig.SocialLinks) == 0 {
		socialDetail = "Link your Instagram/Facebook/TikTok profiles (Settings → public site) — they populate your link-in-bio page and the search knowledge graph."
	}
	domainDetail := "Custom domain connected."
	if len(t.SiteConfig.CustomDomains) == 0 {
		domainDetail = "Site serves on the platform URL. CNAME your own domain to the platform host and list it in Settings → public site to serve everything under your brand."
	}
	templatesDetail := fmt.Sprintf("%d message templates saved.", len(templates))
	if len(templates) == 0 {
		templatesDetail = "No message templates — your team re-typing the same replies? Save confirmation, pickup and follow-up templates on the Growth page."
	}
	lifecycleDetail := "Follow-up cadences tuned."
	if t.LifecycleConfig.PostTripHours == 0 && t.LifecycleConfig.WinBackDays == 0 && t.LifecycleConfig.LeadNudgeDays == 0 {
		lifecycleDetail = "Running on defaults (post-trip 24h, win-back 90d, lead nudge 7d). Review and tune in Settings → lifecycle — this is your repeat-booking engine."
	}

	steps := []OnboardingStep{
		// — essentials: the business cannot trade without these —
		step("profile", "essentials", "Brand profile", hasTagline,
			map[bool]string{true: "Tagline and description set — they drive the SEO site and proposals.",
				false: "Set a tagline/description (Settings → brand) — it drives your public site and quotes."}[hasTagline],
			"/settings"),
		step("catalog", "essentials", map[bool]string{true: "Import service catalog", false: "Create service catalog"}[brownfield],
			activeSvc > 0, catalogDetail, "/catalog"),
		step("areas", "essentials", "Service areas", len(areas) > 0, areasDetail, "/geo"),
		step("payments", "essentials", "Payments",
			t.PricingRules.Currency != "" && len(liveRails) > 0, payDetail, "/settings"),
		step("pricing", "essentials", "Pricing posture", t.PricingRules.DepositPct > 0, pricingDetail, "/settings"),
		step("tax", "essentials", "Tax posture", t.TaxConfig.Engine != "", taxDetail, "/settings"),
		// — operations: the machinery that fulfils a booking —
		step("whatsapp", "operations", "WhatsApp channel", waNumber != "" || sandboxWA, waDetail, "/onboarding"),
		step("team", "operations", "Operator roster", len(t.OperatorConfig.AlertPhones) > 0,
			fmt.Sprintf("%d operator phones receive SLA and acquisition alerts.", len(t.OperatorConfig.AlertPhones)),
			"/settings"),
		step("fleet", "operations", "Fleet", activeFleet > 0, fleetDetail, "/dispatch"),
		step("drivers", "operations", "Drivers & crew", activeCrew > 0, crewDetail, "/dispatch"),
		step("dispatch_cfg", "operations", "Dispatch tuning", t.DispatchConfig.DefaultDurationMinutes > 0, dispatchDetail, "/settings"),
		// — presence: what a client sees before they ever talk to you —
		step("website", "presence", map[bool]string{true: "Website integration", false: "Public website"}[brownfield],
			true, siteDetail, "/onboarding"), // live by construction — the step is "know it exists"
		step("site_design", "presence", "Site design", t.SiteConfig.Theme != nil && t.SiteConfig.Theme.Preset != "", designDetail, "/studio"),
		step("imagery", "presence", "Photography & hero", t.SiteConfig.HeroImageID != "", imageryDetail, "/studio"),
		step("service_imagery", "presence", "Service card images", activeSvc > 0 && svcWithImg == activeSvc, svcImgDetail, "/catalog"),
		step("socials", "presence", "Social profiles", len(t.SiteConfig.SocialLinks) > 0, socialDetail, "/settings"),
		step("custom_domain", "presence", "Custom domain", len(t.SiteConfig.CustomDomains) > 0, domainDetail, "/settings"),
		// — growth: what turns one booking into the next ten —
		step("templates", "growth", "Message templates", len(templates) > 0, templatesDetail, "/growth"),
		step("lifecycle", "growth", "Lifecycle follow-ups",
			t.LifecycleConfig.PostTripHours > 0 || t.LifecycleConfig.WinBackDays > 0 || t.LifecycleConfig.LeadNudgeDays > 0,
			lifecycleDetail, "/settings"),
		step("first_campaign", "growth", "First acquisition campaign", len(promos) > 0 || !t.RewardProgram.Disabled,
			fmt.Sprintf("%d promo campaigns; referral program %s.", len(promos),
				map[bool]string{true: "off", false: "on"}[t.RewardProgram.Disabled]),
			"/growth"),
		step("first_booking", "growth", "First booking", len(bookings) > 0,
			fmt.Sprintf("%d bookings on record.", len(bookings)), "/dispatch"),
		step("go_live", "growth", "90-day acquisition goal", t.AcquisitionConfig.CohortStartedAt != "" || len(bookings) > 0,
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
		"pricing": true, "tax": true,
		"whatsapp": true, "team": true, "fleet": true, "drivers": true, "dispatch_cfg": true,
		"website": true, "site_design": true, "imagery": true, "service_imagery": true, "socials": true, "custom_domain": true,
		"templates": true, "lifecycle": true, "first_campaign": true, "first_booking": true, "go_live": true}
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
