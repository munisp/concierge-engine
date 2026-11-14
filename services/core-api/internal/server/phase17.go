package server

// Phase 27: tenant onboarding journey, brownfield import, and the ten
// acquisition innovations (QR codes, WhatsApp deep links, partner links,
// reviews, gift/switch promo flags — link-in-bio, widget embed, lead nudge
// and referral boosts live in site.go / followups.go / rewards.go).
//
// The track review endpoint is token-authenticated like the rest of
// /v1/track/ (the HMAC token proves custody of the trip link — no tenant
// header required or trusted).

import (
	"net/http"
	"net/url"
	"strings"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"

	"github.com/skip2/go-qrcode"
)

func (h *handlers) registerPhase17(mux *http.ServeMux) {
	// onboarding journey
	mux.HandleFunc("GET /v1/onboarding", h.getOnboarding)
	mux.HandleFunc("PUT /v1/onboarding", h.putOnboarding)
	// brownfield import
	mux.HandleFunc("POST /v1/import/catalog", h.importCatalog)
	mux.HandleFunc("POST /v1/import/clients", h.importClients)
	// acquisition surfaces
	mux.HandleFunc("GET /v1/marketing/qr", h.marketingQR)
	mux.HandleFunc("GET /v1/marketing/wa-links", h.marketingWALinks)
	mux.HandleFunc("GET /v1/partners/{id}/acquisition", h.partnerAcquisition)
	mux.HandleFunc("GET /v1/reviews", h.listReviews)
	mux.HandleFunc("GET /v1/reviews/summary", h.reviewSummary)
	// token-authenticated client surfaces
	mux.HandleFunc("POST /v1/track/{token}/review", h.submitTrackReview)
}

// ------------------------------------------------------------- onboarding

func (h *handlers) getOnboarding(w http.ResponseWriter, r *http.Request) {
	rep, err := h.svc.Onboarding(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
}

func (h *handlers) putOnboarding(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Track  string   `json:"track"`
		Skip   []string `json:"skip"`
		Unskip []string `json:"unskip"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	t, err := h.svc.UpdateOnboarding(r.Context(), tenantOf(r), body.Track, body.Skip, body.Unskip)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

// ------------------------------------------------------------- brownfield import

func (h *handlers) importCatalog(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []domain.ImportCatalogItem `json:"items"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	res, err := h.svc.ImportCatalog(r.Context(), tenantOf(r), body.Items)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *handlers) importClients(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []domain.ImportClientItem `json:"items"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	res, err := h.svc.ImportClients(r.Context(), tenantOf(r), body.Items)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// ------------------------------------------------------------- QR codes

// marketingQR renders a printable PNG QR code whose payload is a capture
// URL stamped channel=qr — bookings from scans attribute to the qr channel
// and count toward the acquisition cohort (and fee) like SEO arrivals.
//
//	target=landing            → /capture (generic)
//	target=svc:{serviceID}    → /capture with the service's campaign
//	target=area:{areaID}      → /capture with the area's campaign
func (h *handlers) marketingQR(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantOf(r)
	target := r.URL.Query().Get("target")
	if target == "" {
		target = "landing"
	}
	var campaign string
	switch {
	case target == "landing":
		campaign = "qr-landing"
	case strings.HasPrefix(target, "svc:"):
		campaign = "qr-svc-" + strings.TrimPrefix(target, "svc:")
	case strings.HasPrefix(target, "area:"):
		campaign = "qr-area-" + strings.TrimPrefix(target, "area:")
	default:
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	link := h.svc.ConsoleBase() + "/capture?tenant=" + tenantID +
		"&channel=qr&utm_source=print&utm_medium=qr&utm_campaign=" + url.QueryEscape(campaign)
	png, err := qrcode.Encode(link, qrcode.Medium, 320)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Disposition", `inline; filename="qr-`+url.QueryEscape(campaign)+`.png"`)
	_, _ = w.Write(png)
}

// ------------------------------------------------------------- WhatsApp click-to-chat

// marketingWALinks builds per-service wa.me click-to-chat deep links with
// a prefilled intent message. Requires the tenant's business number in
// brand.whatsapp_number (onboarding step "whatsapp"); without it the
// endpoint returns the setup instruction instead of fabricating links.
func (h *handlers) marketingWALinks(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.Store.Tenants().Get(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	number, _ := t.Brand["whatsapp_number"].(string)
	number = strings.TrimLeft(strings.TrimSpace(number), "+")
	if number == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"configured": false,
			"setup":      "Set brand.whatsapp_number (E.164) via PUT /v1/tenant to activate click-to-chat links.",
		})
		return
	}
	services, err := h.svc.Store.Catalog().List(r.Context(), t.ID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	type waLink struct {
		ServiceID   string `json:"service_id,omitempty"`
		ServiceName string `json:"service_name"`
		URL         string `json:"url"`
	}
	out := []waLink{}
	for _, s := range services {
		if !s.Active {
			continue
		}
		msg := url.QueryEscape("Hello " + t.Name + " — I'd like to book: " + s.Name)
		out = append(out, waLink{ServiceID: s.ID, ServiceName: s.Name,
			URL: "https://wa.me/" + number + "?text=" + msg})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"configured": true, "number": number, "links": out})
}

// ------------------------------------------------------------- partner co-marketing

func (h *handlers) partnerAcquisition(w http.ResponseWriter, r *http.Request) {
	pa, err := h.svc.PartnerAcquisition(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, pa)
}

// ------------------------------------------------------------- reviews

func (h *handlers) listReviews(w http.ResponseWriter, r *http.Request) {
	revs, err := h.svc.ListReviews(r.Context(), tenantOf(r), 200)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, revs)
}

func (h *handlers) reviewSummary(w http.ResponseWriter, r *http.Request) {
	sum, err := h.svc.ReviewSummary(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sum)
}

// submitTrackReview lets the passenger rate a completed trip from the
// track page. The track token authenticates the passenger for that exact
// assignment; the client anchor is resolved assignment → booking →
// request, so a forged rating cannot attach to someone else's trip.
func (h *handlers) submitTrackReview(w http.ResponseWriter, r *http.Request) {
	tenantID, assignmentID, err := h.svc.Trips.ParseClientToken(r.PathValue("token"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var body struct {
		Rating         int    `json:"rating"`
		Text           string `json:"text"`
		PublishConsent bool   `json:"publish_consent"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	ctx := r.Context()
	// Resolve the client anchor for this assignment.
	assignments, err := h.svc.Store.Dispatch().ListAssignments(ctx, tenantID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	clientID := ""
	for _, a := range assignments {
		if a.ID != assignmentID {
			continue
		}
		bookings, err := h.svc.Store.Bookings().List(ctx, tenantID)
		if err != nil {
			break
		}
		for _, b := range bookings {
			if b.ID != a.BookingID {
				continue
			}
			if sr, err := h.svc.Store.Requests().Get(ctx, tenantID, b.RequestID); err == nil {
				clientID = sr.ClientID
			}
		}
	}
	if clientID == "" {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	rev, err := h.svc.SubmitReview(ctx, tenantID, clientID, assignmentID, body.Rating, body.Text, body.PublishConsent)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, rev)
}
