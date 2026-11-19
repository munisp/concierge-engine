package server

// Phase 55: industry packs — curated, campaignable service bundles with
// their own public landing pages (/site/{slug}/packs/{slug}) and
// one-click vertical-segment campaign wiring (POST /v1/packs/{slug}/launch).
//
// Honesty rules:
//   - A pack page exists only when the pack is ACTIVE and at least one of
//     its services resolves against the live catalog (dangling/inactive
//     ids drop out; nothing bookable = 404, never an empty shell page).
//   - Prices and descriptions on the page come from the catalog rows
//     themselves — a pack is a curation, not a parallel price list.
//   - The sitemap lists pack pages under the same resolvability gate.
//   - Launch derives its segment from the pack services' REAL verticals;
//     a multi-vertical pack says so instead of pretending a single
//     audience.

import (
	"net/http"
	"strings"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (h *handlers) registerPhase55(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/packs/{slug}/launch", h.packLaunch)
}

// resolvePack finds the active pack and its live catalog services.
func resolvePack(t *model.Tenant, services []model.CatalogService, slug string) (*model.IndustryPack, []model.CatalogService) {
	var pack *model.IndustryPack
	for i := range t.SiteConfig.Packs {
		if t.SiteConfig.Packs[i].Slug == slug && t.SiteConfig.Packs[i].Active {
			pack = &t.SiteConfig.Packs[i]
			break
		}
	}
	if pack == nil {
		return nil, nil
	}
	byID := map[string]model.CatalogService{}
	for _, s := range services {
		byID[s.ID] = s
	}
	var resolved []model.CatalogService
	for _, id := range pack.ServiceIDs {
		if s, ok := byID[id]; ok {
			resolved = append(resolved, s)
		}
	}
	return pack, resolved
}

// packVerticals returns the distinct verticals across a pack's services.
func packVerticals(services []model.CatalogService) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range services {
		if s.Vertical != "" && !seen[s.Vertical] {
			seen[s.Vertical] = true
			out = append(out, s.Vertical)
		}
	}
	return out
}

// packPitch resolves the pack's copy for the page locale with the
// default-locale fallback; empty when the pack carries no pitch.
func packPitch(pack *model.IndustryPack, t *model.Tenant, locale string) string {
	if v := strings.TrimSpace(pack.Pitch[locale]); v != "" {
		return v
	}
	return strings.TrimSpace(pack.Pitch[defaultLocale(t)])
}

func (h *handlers) sitePack(w http.ResponseWriter, r *http.Request) {
	t, services, _, err := h.siteData(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	locale, ok := requestLocale(r, t)
	if !ok {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	slug := r.PathValue("packSlug")
	pack, resolved := resolvePack(t, services, slug)
	if pack == nil || len(resolved) == 0 {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	rawBase := h.siteBase(r, t.Slug)
	base := localeBase(rawBase, t, locale)
	pagePath := "/packs/" + slug

	desc := packPitch(pack, t, locale)
	if desc == "" {
		// Composed from real service names — never invented copy.
		names := make([]string, 0, len(resolved))
		for _, s := range resolved {
			names = append(names, localizedServiceName(s, locale))
		}
		desc = strings.Join(names, ", ") + " — " + t.Name
	}

	p := h.basePage(r, t, locale, "/og.png")
	p.Title = pack.Name + " | " + t.Name
	p.Description = desc
	p.Canonical = base + pagePath
	p.HrefLangs = h.hreflangCluster(t, rawBase, pagePath)
	p.Heading = pack.Name
	p.SubHead = pack.Name
	p.Services = serviceCards(t, resolved, base, rawBase, locale,
		func(id string) string { return h.liveMediaURL(r.Context(), t.ID, rawBase, id) })
	// Attribution spine: the CTA stamps the pack as the campaign so the
	// funnel + economics (Phase 35/40) tie bookings back to the pack.
	p.CTA, p.CTALabel = h.captureURL(t.ID, "pack-"+slug), p.D.BookYourConcierge
	renderSite(w, r, p)
}

// ------------------------------------------------------------- launch

type packLaunchInput struct {
	Channel    string              `json:"channel,omitempty"`
	Subject    string              `json:"subject,omitempty"`
	Body       string              `json:"body,omitempty"`
	TemplateID string              `json:"template_id,omitempty"`
	Segment    *model.SegmentRules `json:"segment,omitempty"`
}

// packLaunch fires the pack as a broadcast campaign. Campaign name =
// "pack-{slug}" (the broadcast validator forbids ':'): funnel, spend and
// ROAS for the pack then flow through the Phase 35/40 spine unchanged.
func (h *handlers) packLaunch(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantOf(r)
	slug := r.PathValue("slug")
	var in packLaunchInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	t, err := h.svc.Tenants.Get(r.Context(), tenantID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	catalog, err := h.svc.Store.Catalog().List(r.Context(), tenantID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	pack, resolved := resolvePack(t, catalog, slug)
	if pack == nil {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	// Default segment: single-vertical pack → proven-demand vertical rule;
	// multi-vertical pack → no vertical rule (the notice says why).
	segment := model.SegmentRules{}
	notice := ""
	if in.Segment != nil {
		segment = *in.Segment
	} else if vs := packVerticals(resolved); len(vs) == 1 {
		segment.Vertical = vs[0]
	} else if len(vs) > 1 {
		notice = "pack spans " + strings.Join(vs, ", ") + " — audience defaulted to all reachable clients; pass a segment to narrow"
	}
	res, err := h.svc.Campaigns.Broadcast(r.Context(), tenantID, domain.BroadcastInput{
		Campaign: "pack-" + slug, Purpose: model.OutreachMarketing,
		Body: in.Body, TemplateID: in.TemplateID, Segment: segment,
		Channel: in.Channel, Subject: in.Subject,
	})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"result":   res,
		"campaign": "pack-" + slug,
		"segment":  segment,
		"notice":   notice,
		"pack_url": "/site/" + t.Slug + "/packs/" + slug,
	})
}
