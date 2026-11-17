package server

// Phase 49: Apache OpenDAL storage surfaces.
//
// GET /v1/storage/engine reports the media storage engine honestly:
// "opendal" (+ scheme from the adapter's own provenance endpoint) when the
// blob-adapter is wired, otherwise the legacy inline store. A configured
// but unreachable adapter says so — the label never claims health it
// didn't check.
//
// POST /v1/media/backfill migrates legacy inline (bytea) media rows to
// object storage. Idempotent; advisory counts.

import (
	"errors"
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/store"
)

func (h *handlers) registerPhase49(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/storage/engine", h.storageEngine)
	mux.HandleFunc("POST /v1/media/backfill", h.backfillMedia)
}

func (h *handlers) storageEngine(w http.ResponseWriter, r *http.Request) {
	blobs := store.BlobClient(h.svc.Store)
	if blobs == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"engine": "legacy-inline",
			"detail": h.svc.StorageEngine, // "postgres-bytea" or "memory"
			"legacy": true,
		})
		return
	}
	info, err := blobs.Engine(r.Context())
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"engine":    "opendal",
			"detail":    "apache-opendal (adapter unreachable: " + err.Error() + ")",
			"legacy":    false,
			"reachable": false,
		})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"engine":    "opendal",
		"detail":    info.Engine + "/" + info.Scheme,
		"legacy":    false,
		"reachable": true,
	})
}

func (h *handlers) backfillMedia(w http.ResponseWriter, r *http.Request) {
	migrated, skipped, err := store.BackfillMedia(r.Context(), h.svc.Store, tenantOf(r))
	if err != nil {
		if errors.Is(err, store.ErrBlobNotConfigured) {
			httpx.WriteError(w, httpx.ErrValidation)
			return
		}
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"migrated": migrated,
		"skipped":  skipped,
	})
}
