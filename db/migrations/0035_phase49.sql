-- Phase 49: Apache OpenDAL object storage. tenant_media rows can now
-- point at an object in the blob-adapter (OpenDAL: fs/s3/gcs/azblob/...)
-- instead of carrying inline bytea. A row with storage_key set has its
-- bytes in object storage; data is then empty and size_bytes carries the
-- length. Legacy rows (storage_key = '') keep their inline bytes — the
-- serving path falls back to them, and POST /v1/media/backfill migrates
-- them (sets storage_key/engine, records size, clears data).
ALTER TABLE tenant_media ADD COLUMN IF NOT EXISTS storage_key text NOT NULL DEFAULT '';
ALTER TABLE tenant_media ADD COLUMN IF NOT EXISTS storage_engine text NOT NULL DEFAULT '';
ALTER TABLE tenant_media ADD COLUMN IF NOT EXISTS size_bytes integer NOT NULL DEFAULT 0;
UPDATE tenant_media SET size_bytes = octet_length(data) WHERE size_bytes = 0;
