package domain

// Phase 30: Site Studio — the design layer for the public acquisition site.
//
// Research verdict (recorded in docs/phase30.md): the open-source builder
// landscape splits into full CMSs (WordPress GPL, Ghost MIT — separate
// stacks, not embeddable), self-hosted builders with copyleft obligations
// (Webstudio/Silex/VvvebJs, AGPL — unusable inside a commercial SaaS), and
// embeddable editor frameworks (GrapesJS BSD-3, Puck MIT). We deliberately
// build the studio as a DATA layer on our own SSR template engine rather
// than embedding a drag-drop canvas: the site's content is already
// data-driven (catalog, areas, i18n, reviews), so "design" = theme tokens
// + generated copy, and Puck (MIT, React) is the evaluated candidate for a
// free-form canvas if tenants outgrow presets.

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// StudioService generates and applies site design + content.
type StudioService struct {
	s      *Services
	assist *CaseAssistClient // concierge-agent; empty base = template fallback
}

func NewStudioService(s *Services, assist *CaseAssistClient) *StudioService {
	return &StudioService{s: s, assist: assist}
}

// StudioPreset is a named, curated theme bundle.
type StudioPreset struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Primary     string `json:"primary"`
	Accent      string `json:"accent"`
	Font        string `json:"font"`
	Corners     string `json:"corners"`
}

// studioPresets is the curated gallery. "noir-gold" is the Phase 29
// default look, so existing tenants render identically until they pick
// another preset.
var studioPresets = []StudioPreset{
	{"noir-gold", "Luxury default — dark navy hero, gold accents, serif display.", "#0e1626", "#c9a227", "serif", "rounded"},
	{"azure-modern", "Contemporary — deep blue hero, bright blue accents, clean sans.", "#0b2447", "#19a7ce", "sans", "rounded"},
	{"slate-minimal", "Minimal — near-black on white, restrained slate, sans.", "#111827", "#374151", "sans", "sharp"},
	{"emerald-classic", "Classic heritage — deep green, warm gold, serif.", "#10281f", "#b98a2f", "serif", "rounded"},
	{"terracotta-warm", "Warm hospitality — burnt umber hero, terracotta accents, serif.", "#3b1f16", "#c96f4a", "serif", "rounded"},
}

func StudioPresets() []StudioPreset { return studioPresets }

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ValidateTheme normalizes and validates a theme. Unknown preset names,
// malformed colors and unknown enum values are rejected (ErrValidation) —
// the site must never render broken CSS from stored state.
func ValidateTheme(t *model.SiteTheme) error {
	if t == nil {
		return nil
	}
	preset := strings.ToLower(strings.TrimSpace(t.Preset))
	if preset != "" && preset != "custom" {
		found := false
		for _, p := range studioPresets {
			if p.Name == preset {
				found = true
				break
			}
		}
		if !found {
			return httpx.ErrValidation
		}
	}
	t.Preset = preset
	for _, c := range []string{t.Primary, t.Accent} {
		if c != "" && !hexColor.MatchString(c) {
			return httpx.ErrValidation
		}
	}
	t.Primary, t.Accent = strings.ToLower(t.Primary), strings.ToLower(t.Accent)
	if t.Font != "" && t.Font != "serif" && t.Font != "sans" {
		return httpx.ErrValidation
	}
	if t.Corners != "" && t.Corners != "rounded" && t.Corners != "sharp" {
		return httpx.ErrValidation
	}
	return nil
}

// SiteDesigns enumerates the landing LAYOUT families the site engine can
// render (Phase 32). Empty = classic.
var SiteDesigns = map[string]bool{"": true, "classic": true, "editorial": true}

// SiteSections enumerates the landing sections a tenant may hide via
// site_config.hidden_sections (Phase 32). Visibility only — ordering stays
// engine-owned so the conversion narrative can't be scrambled.
var SiteSections = map[string]bool{
	"trust": true, "stats": true, "services": true, "steps": true,
	"platform": true, "gallery": true, "reviews": true, "areas": true, "cta": true,
}

// MaxGalleryImages caps the landing gallery strip (Phase 32).
const MaxGalleryImages = 8

// SiteContentBlocks (Phase 54): the closed vocabulary of marketer-editable
// copy slots — Mautic "content blocks" borrowed onto the SSR engine.
// Curated slots, never freeform HTML: the page structure stays
// engine-owned (the Phase 32 curated-beats-freeform rule).
var SiteContentBlocks = map[string]bool{
	"hero_tagline": true, // hero paragraph — display only, meta/OG stay engine-derived
	"cta_label":    true, // primary landing CTA button
	"footer_note":  true, // footer tagline line
	"announcement": true, // slim top bar; empty = not rendered at all
}

// MaxContentBlockLen keeps a block a headline, not a page (280 chars).
const MaxContentBlockLen = 280

var blockLocale = regexp.MustCompile(`^[a-z]{2}(-[A-Z]{2})?$`)

// ValidateContentBlocks enforces the Phase 54 contract: known slots,
// well-formed locale keys, plain text (no markup — the template would
// escape it anyway, and silent literal `<b>` output would confuse
// marketers), length cap, and empty values dropped (clearing a block
// restores the engine copy).
func ValidateContentBlocks(blocks map[string]map[string]string) (map[string]map[string]string, error) {
	if len(blocks) == 0 {
		return nil, nil
	}
	out := map[string]map[string]string{}
	for key, perLocale := range blocks {
		if !SiteContentBlocks[key] {
			return nil, httpx.ErrValidation
		}
		for loc, text := range perLocale {
			if !blockLocale.MatchString(loc) {
				return nil, httpx.ErrValidation
			}
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			if len([]rune(text)) > MaxContentBlockLen || strings.ContainsAny(text, "<>") {
				return nil, httpx.ErrValidation
			}
			if out[key] == nil {
				out[key] = map[string]string{}
			}
			out[key][loc] = text
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// ValidateSiteConfig (Phase 32) validates every design-bearing site_config
// field on EVERY write path. PUT /v1/tenant patches site_config wholesale
// and used to bypass ValidateTheme entirely — an invalid preset posted
// there would have rendered broken CSS until someone noticed.
func ValidateSiteConfig(sc *model.SiteConfig) error {
	if sc == nil {
		return nil
	}
	if err := ValidateTheme(sc.Theme); err != nil {
		return err
	}
	design := strings.ToLower(strings.TrimSpace(sc.Design))
	if !SiteDesigns[design] {
		return httpx.ErrValidation
	}
	sc.Design = design
	seen := map[string]bool{}
	keep := sc.HiddenSections[:0]
	for _, s := range sc.HiddenSections {
		s = strings.ToLower(strings.TrimSpace(s))
		if !SiteSections[s] {
			return httpx.ErrValidation
		}
		if !seen[s] {
			seen[s] = true
			keep = append(keep, s)
		}
	}
	sc.HiddenSections = keep
	if len(sc.GalleryIDs) > MaxGalleryImages {
		return httpx.ErrValidation
	}
	// Phase 54: marketer content blocks — validated on every write path.
	blocks, err := ValidateContentBlocks(sc.ContentBlocks)
	if err != nil {
		return err
	}
	sc.ContentBlocks = blocks
	return nil
}

// ResolveTheme returns the effective token set: preset bundle overlaid
// with explicit token overrides. Nil theme = the default (noir-gold).
func ResolveTheme(t *model.SiteTheme) StudioPreset {
	base := studioPresets[0]
	if t != nil {
		for _, p := range studioPresets {
			if p.Name == t.Preset {
				base = p
				break
			}
		}
		if t.Primary != "" {
			base.Primary = t.Primary
		}
		if t.Accent != "" {
			base.Accent = t.Accent
		}
		if t.Font != "" {
			base.Font = t.Font
		}
		if t.Corners != "" {
			base.Corners = t.Corners
		}
	}
	return base
}

// SetTheme validates and persists a theme inside site_config, preserving
// every other site_config field.
func (svc *StudioService) SetTheme(ctx context.Context, tenantID string, theme *model.SiteTheme) (*model.SiteTheme, error) {
	if err := ValidateTheme(theme); err != nil {
		return nil, err
	}
	t, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	t.SiteConfig.Theme = theme
	if _, err := svc.s.Tenants.UpdateConfig(ctx, t); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "tenant", tenantID, "site.theme_updated", map[string]any{"preset": theme.Preset})
	return theme, nil
}

// ---------------------------------------------------------- content generation

// StudioDraft is the business-case → content pack. Always a DRAFT: nothing
// is written until ApplyDraft. Provenance is labeled — "agent:*" when the
// concierge-agent produced it, "template" for the deterministic fallback.
type StudioDraft struct {
	Locale       string            `json:"locale"`
	Tagline      string            `json:"tagline"`
	Description  string            `json:"description"`
	ServiceDescs map[string]string `json:"service_descs"` // catalog service ID → description
	Engine       string            `json:"engine"`        // provenance, never hidden
}

// GenerateContent drafts tagline/description/per-service copy from a free-
// text business case plus the tenant's REAL catalog (names, prices, areas —
// the generator never invents services). With the concierge-agent
// configured, the agent writes the copy (intent "site-content"); any agent
// failure or empty base falls back to deterministic template prose. Either
// way the engine is labeled on the draft.
func (svc *StudioService) GenerateContent(ctx context.Context, tenantID, businessCase, locale string) (*StudioDraft, error) {
	t, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if locale == "" {
		locales := t.SiteConfig.Locales
		if len(locales) > 0 {
			locale = locales[0]
		} else {
			locale = "en"
		}
	}
	services, err := svc.s.Store.Catalog().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	areas, err := svc.s.Store.Geo().Areas(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// Agent path: one Draft call per artifact, intent "site-content". All
	// context the agent sees is real tenant data + the operator's business
	// case; outputs remain drafts pending explicit apply.
	if svc.assist != nil {
		draft := &StudioDraft{Locale: locale, ServiceDescs: map[string]string{}, Engine: "template"}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Business case: %s\nTenant: %s\nServices: ", businessCase, t.Name)
		for _, s2 := range services {
			fmt.Fprintf(&sb, "%s (%s); ", s2.Name, s2.Vertical)
		}
		fmt.Fprintf(&sb, "\nAreas: ")
		for _, a := range areas {
			fmt.Fprintf(&sb, "%s; ", a.Name)
		}
		ctxStr := sb.String()
		ok := true
		if tagline, engine, err := svc.assist.Draft(ctx, "site-content tagline "+locale, ctxStr); err == nil && tagline != "" {
			draft.Tagline, draft.Engine = strings.TrimSpace(tagline), engine
		} else {
			ok = false
		}
		if desc, _, err := svc.assist.Draft(ctx, "site-content description "+locale, ctxStr); err == nil && desc != "" {
			draft.Description = strings.TrimSpace(desc)
		} else {
			ok = false
		}
		for _, s2 := range services {
			if !s2.Active {
				continue
			}
			if d, _, err := svc.assist.Draft(ctx, "site-content service-description "+locale,
				fmt.Sprintf("%s\nService: %s (%s, %s %.2f)", ctxStr, s2.Name, s2.Vertical, s2.Currency, float64(s2.BaseMinor)/100)); err == nil && d != "" {
				draft.ServiceDescs[s2.ID] = strings.TrimSpace(d)
			}
		}
		if ok && draft.Tagline != "" && draft.Description != "" {
			return draft, nil
		}
		// partial failure → full deterministic fallback (never ship a
		// half-agent/half-template draft — provenance would lie)
	}

	// Deterministic fallback: template prose from real data only.
	draft := &StudioDraft{Locale: locale, ServiceDescs: map[string]string{}, Engine: "template"}
	areaNames := make([]string, 0, len(areas))
	for _, a := range areas {
		areaNames = append(areaNames, a.Name)
	}
	switch {
	case strings.HasPrefix(locale, "pt"):
		draft.Tagline = fmt.Sprintf("%s — serviço premium, organizado de ponta a ponta.", t.Name)
		draft.Description = taglineOrCase(businessCase, t.Name)
	default:
		draft.Tagline = fmt.Sprintf("%s — premium service, arranged end to end.", t.Name)
		draft.Description = taglineOrCase(businessCase, t.Name)
	}
	for _, s2 := range services {
		if !s2.Active {
			continue
		}
		d := s2.Name + " — " + humanizeVertical(s2.Vertical) + " by " + t.Name
		if s2.BaseMinor > 0 {
			d += fmt.Sprintf(", from %s %.2f", s2.Currency, float64(s2.BaseMinor)/100)
		}
		draft.ServiceDescs[s2.ID] = d + "."
	}
	return draft, nil
}

func taglineOrCase(businessCase, name string) string {
	bc := strings.TrimSpace(businessCase)
	if bc == "" {
		return fmt.Sprintf("%s — every booking includes a dedicated concierge, live tracking and WhatsApp confirmation.", name)
	}
	if len(bc) > 300 {
		bc = bc[:300]
	}
	return bc
}

func humanizeVertical(v string) string {
	return strings.ToLower(strings.ReplaceAll(v, "_", " "))
}

// ApplyDraft writes a draft into brand_i18n (tagline/description for the
// locale) and catalog desc_i18n overrides. Validates every service ID
// belongs to the tenant before writing anything (all-or-nothing).
func (svc *StudioService) ApplyDraft(ctx context.Context, tenantID string, d *StudioDraft) error {
	if d == nil || (d.Tagline == "" && d.Description == "" && len(d.ServiceDescs) == 0) {
		return httpx.ErrValidation
	}
	t, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return err
	}
	services, err := svc.s.Store.Catalog().List(ctx, tenantID)
	if err != nil {
		return err
	}
	byID := map[string]*model.CatalogService{}
	for i := range services {
		byID[services[i].ID] = &services[i]
	}
	for id := range d.ServiceDescs {
		if _, ok := byID[id]; !ok {
			return httpx.ErrValidation // foreign service ID — refuse wholesale
		}
	}
	if d.Tagline != "" || d.Description != "" {
		if t.SiteConfig.BrandI18n == nil {
			t.SiteConfig.BrandI18n = map[string]map[string]string{}
		}
		m := t.SiteConfig.BrandI18n[d.Locale]
		if m == nil {
			m = map[string]string{}
		}
		if d.Tagline != "" {
			m["tagline"] = d.Tagline
		}
		if d.Description != "" {
			m["description"] = d.Description
		}
		t.SiteConfig.BrandI18n[d.Locale] = m
		if _, err := svc.s.Tenants.UpdateConfig(ctx, t); err != nil {
			return err
		}
	}
	for id, desc := range d.ServiceDescs {
		svcRow := byID[id]
		if svcRow.DescI18n == nil {
			svcRow.DescI18n = map[string]string{}
		}
		svcRow.DescI18n[d.Locale] = desc
		if err := svc.s.Store.Catalog().Update(ctx, svcRow); err != nil {
			return err
		}
	}
	svc.s.Events.Emit(ctx, tenantID, "tenant", tenantID, "site.content_applied",
		map[string]any{"locale": d.Locale, "engine": d.Engine, "services": len(d.ServiceDescs)})
	return nil
}
