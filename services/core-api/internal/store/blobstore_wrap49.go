package store

// Phase 49: Apache OpenDAL as the media storage engine.
//
// WithBlobMedia decorates ANY Store (memory or postgres) so media bytes go
// to the blob-adapter (the OpenDAL sidecar) while the row keeps metadata +
// storage_key. Wrapping at the aggregate Store boundary means every media
// call site — upload, public site serving, console raw fetch, delete-ref
// scrubbing, studio export — gets object storage with zero changes.
//
// Provenance is per-row: StorageEngine "opendal" = bytes in object
// storage; empty = legacy inline bytea (still served — the read path falls
// back, and BackfillMedia migrates).
//
// Write path is fail-CLOSED: if the adapter errors on Put, the upload
// errors. Silently falling back to bytea would be an unlabeled durability
// downgrade; a failed upload is loud and retriable.

import (
	"context"
	"errors"

	"concierge-engine/services/core-api/internal/blobstore"
	"concierge-engine/services/core-api/internal/model"
)

// MediaObjectKey is the deterministic object key for a media asset. It is
// a pure function of the id, so delete/backfill never need a read first.
func MediaObjectKey(id string) string { return "media/" + id }

// MediaEngineOpendal is the StorageEngine label stamped on object rows.
const MediaEngineOpendal = "opendal"

type blobStoreWrap struct {
	Store
	blobs *blobstore.Client
}

// WithBlobMedia returns st with media storage routed through the
// blob-adapter. Everything except Media() delegates unchanged.
func WithBlobMedia(st Store, blobs *blobstore.Client) Store {
	return blobStoreWrap{Store: st, blobs: blobs}
}

func (w blobStoreWrap) Media() MediaStore {
	return &blobMedia{inner: w.Store.Media(), blobs: w.blobs}
}

// BlobClient exposes the adapter client for the engine/backfill surfaces;
// nil when the store is not decorated (legacy inline mode).
func BlobClient(st Store) *blobstore.Client {
	if w, ok := st.(blobStoreWrap); ok {
		return w.blobs
	}
	return nil
}

// ErrBlobNotConfigured is returned by BackfillMedia when the store is not
// decorated (CE_BLOB_ADAPTER_URL unset) — there is no object storage to
// backfill into.
var ErrBlobNotConfigured = errors.New("object storage not configured (CE_BLOB_ADAPTER_URL unset)")

// BackfillMedia migrates legacy inline rows to object storage. Idempotent:
// rows already carrying a storage_key are skipped. Per-row failures are
// collected and reported — one bad row never blocks the rest.
func BackfillMedia(ctx context.Context, st Store, tenantID string) (migrated, skipped int, err error) {
	m, ok := st.Media().(*blobMedia)
	if !ok {
		return 0, 0, ErrBlobNotConfigured
	}
	list, err := m.inner.List(ctx, tenantID)
	if err != nil {
		return 0, 0, err
	}
	for _, md := range list {
		if md.StorageKey != "" {
			skipped++
			continue
		}
		row, err := m.inner.Get(ctx, tenantID, md.ID)
		if err != nil {
			return migrated, skipped, err
		}
		if len(row.Data) == 0 {
			// No inline bytes and no key: nothing to migrate (defensive —
			// should not occur on a healthy legacy row).
			skipped++
			continue
		}
		key := MediaObjectKey(row.ID)
		if err := m.blobs.Put(ctx, tenantID, key, row.Data, row.ContentType); err != nil {
			return migrated, skipped, err
		}
		if err := m.inner.MarkExternal(ctx, tenantID, row.ID, key, MediaEngineOpendal); err != nil {
			return migrated, skipped, err
		}
		migrated++
	}
	return migrated, skipped, nil
}

type blobMedia struct {
	inner MediaStore
	blobs *blobstore.Client
}

func (w *blobMedia) Put(ctx context.Context, md *model.Media) error {
	key := MediaObjectKey(md.ID)
	if err := w.blobs.Put(ctx, md.TenantID, key, md.Data, md.ContentType); err != nil {
		return err
	}
	md.StorageKey = key
	md.StorageEngine = MediaEngineOpendal
	md.SizeBytes = len(md.Data)
	md.Data = nil // bytes live in the object store now
	return w.inner.Put(ctx, md)
}

func (w *blobMedia) Get(ctx context.Context, tenantID, id string) (*model.Media, error) {
	md, err := w.inner.Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if md.StorageKey == "" {
		return md, nil // legacy inline row
	}
	data, _, err := w.blobs.Get(ctx, tenantID, md.StorageKey)
	if err != nil {
		return nil, err
	}
	md.Data = data
	md.SizeBytes = len(data)
	return md, nil
}

func (w *blobMedia) List(ctx context.Context, tenantID string) ([]model.Media, error) {
	return w.inner.List(ctx, tenantID)
}

// Delete removes the object first, then the row: a blob-delete failure
// leaves the row pointing at a still-existing object (consistent); the
// reverse order could orphan an object silently. Adapter delete is
// idempotent, so replaying a half-failed delete is safe.
func (w *blobMedia) Delete(ctx context.Context, tenantID, id string) error {
	if err := w.blobs.Delete(ctx, tenantID, MediaObjectKey(id)); err != nil {
		return err
	}
	return w.inner.Delete(ctx, tenantID, id)
}

// MarkExternal delegates — the wrapper itself never calls it (backfill
// writes through blobs first, then flips the row via this method).
func (w *blobMedia) MarkExternal(ctx context.Context, tenantID, id, storageKey, engine string) error {
	return w.inner.MarkExternal(ctx, tenantID, id, storageKey, engine)
}
