package server

import (
	"net/http"
	"strconv"

	"concierge-engine/services/core-api/internal/httpx"
)

// Phase 6 geo routes: service areas, serviceability, geocoding, proximity.
func (h *handlers) registerPhase6(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/geo/areas", h.createGeoArea)
	mux.HandleFunc("GET /v1/geo/areas", h.listGeoAreas)
	mux.HandleFunc("GET /v1/geo/check", h.geoCheck)
	mux.HandleFunc("GET /v1/geo/geocode", h.geocode)
	mux.HandleFunc("GET /v1/geo/nearest-partners", h.nearestPartners)
}

func (h *handlers) createGeoArea(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string       `json:"name"`
		Polygon [][2]float64 `json:"polygon"` // [lat,lng] ring
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	a, err := h.svc.Geo.CreateArea(r.Context(), tenantOf(r), body.Name, body.Polygon)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, a)
}

func (h *handlers) listGeoAreas(w http.ResponseWriter, r *http.Request) {
	areas, err := h.svc.Geo.Areas(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, areas)
}

// geoCheck: serviceability — which of the tenant's areas contain the point.
func (h *handlers) geoCheck(w http.ResponseWriter, r *http.Request) {
	lat, err1 := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	lng, err2 := strconv.ParseFloat(r.URL.Query().Get("lng"), 64)
	if err1 != nil || err2 != nil {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	hits, err := h.svc.Geo.Contains(r.Context(), tenantOf(r), lat, lng)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"serviceable": len(hits) > 0, "areas": hits})
}

func (h *handlers) geocode(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Geo.Geocode(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *handlers) nearestPartners(w http.ResponseWriter, r *http.Request) {
	lat, err1 := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	lng, err2 := strconv.ParseFloat(r.URL.Query().Get("lng"), 64)
	if err1 != nil || err2 != nil {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := h.svc.Geo.NearestPartners(r.Context(), tenantOf(r), lat, lng, limit)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
