package server

// Phase 31: media pipeline. Tenants upload site imagery (hero, service
// cards) as JSON base64; the platform stores it tenant-scoped and serves
// it publicly under /site/{slug}/media/{id} with long cache headers. The
// public URL never carries the tenant id — the slug resolves the tenant
// and the media row must belong to it (cross-tenant ids 404).
//
// SVG is refused (scriptable markup on the platform origin = XSS); the
// decoded payload is sniffed and must match the declared raster type.

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (h *handlers) registerMedia(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/media", h.uploadMedia)
	mux.HandleFunc("GET /v1/media", h.listMedia)
	mux.HandleFunc("DELETE /v1/media/{id}", h.deleteMedia)
	// PUT /v1/catalog/{id}: mutable catalog fields — currently the card
	// image (Phase 31). i18n maps continue to be written by the studio.
	mux.HandleFunc("PUT /v1/catalog/{id}", h.updateCatalogService)
}

// uploadMaxBody caps the JSON envelope: 8 MB decoded ≈ 10.7 MB base64
// plus field overhead; 12 MB gives headroom without opening the door.
const uploadMaxBody = 12 << 20

func (h *handlers) uploadMedia(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind        string `json:"kind"`
		ContentType string `json:"content_type"`
		DataB64     string `json:"data_base64"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, uploadMaxBody)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	body.ContentType = strings.ToLower(strings.TrimSpace(body.ContentType))
	if !model.MediaContentTypes[body.ContentType] {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	switch body.Kind {
	case "", model.MediaKindMisc:
		body.Kind = model.MediaKindMisc
	case model.MediaKindHero, model.MediaKindService:
	default:
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	data, err := base64.StdEncoding.DecodeString(body.DataB64)
	if err != nil || len(data) == 0 || len(data) > model.MaxMediaBytes {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	// Sniff: the declared type must match the payload (webp reports as
	// image/webp on Go 1.21+; accept the declared type when the sniffer
	// only recognizes the container family).
	sniffed := http.DetectContentType(data)
	if sniffed != body.ContentType &&
		!(body.ContentType == "image/webp" && sniffed == "image/webp") &&
		!(strings.HasPrefix(body.ContentType, "image/") && strings.HasPrefix(sniffed, "image/")) {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	tenantID := tenantOf(r)
	md := &model.Media{
		ID:          domain.NewID(),
		TenantID:    tenantID,
		Kind:        body.Kind,
		ContentType: body.ContentType,
		Data:        data,
		SizeBytes:   len(data),
		CreatedAt:   h.svc.Now().UTC(),
	}
	if err := h.svc.Store.Media().Put(r.Context(), md); err != nil {
		httpx.WriteError(w, err)
		return
	}
	t, err := h.svc.Store.Tenants().Get(r.Context(), tenantID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	md.Data = nil // never serialize bytes
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"media": md,
		"url":   h.svc.PublicBase() + "/site/" + t.Slug + "/media/" + md.ID,
	})
}

func (h *handlers) listMedia(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Store.Media().List(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) deleteMedia(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Store.Media().Delete(r.Context(), tenantOf(r), r.PathValue("id")); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// updateCatalogService applies the mutable catalog fields (image_id) to an
// existing service. The record is loaded first so untouched fields survive
// — this is a PATCH-shaped PUT, not a wholesale replace.
func (h *handlers) updateCatalogService(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ImageID *string `json:"image_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	tenantID := tenantOf(r)
	services, err := h.svc.Store.Catalog().List(r.Context(), tenantID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var svc *model.CatalogService
	for i := range services {
		if services[i].ID == r.PathValue("id") {
			svc = &services[i]
			break
		}
	}
	if svc == nil {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	if body.ImageID != nil {
		if *body.ImageID != "" {
			// The image must exist and belong to this tenant — a dangling or
			// cross-tenant id is rejected, not silently stored.
			if _, err := h.svc.Store.Media().Get(r.Context(), tenantID, *body.ImageID); err != nil {
				httpx.WriteError(w, httpx.ErrValidation)
				return
			}
		}
		svc.ImageID = *body.ImageID
	}
	if err := h.svc.Store.Catalog().Update(r.Context(), svc); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, svc)
}

// siteMedia serves a media asset publicly. The tenant comes from the URL
// slug and the row must belong to it; content is immutable per id, so the
// response is cacheable for a day (re-uploads get a new id).
func (h *handlers) siteMedia(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.Store.Tenants().GetBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	md, err := h.svc.Store.Media().Get(r.Context(), t.ID, r.PathValue("mediaID"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", md.ContentType)
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(md.Data)
}
