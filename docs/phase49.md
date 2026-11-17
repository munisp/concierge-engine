# Phase 49 — Apache OpenDAL as the main storage engine

## Decision

OpenDAL is now the platform's object-storage engine for binary payloads
(tenant media first: site heroes, service cards, gallery). OpenDAL is
Rust-native — its reference implementation is the Rust crate — so the
engine runs as a sidecar, `crates/blob-adapter` (axum × `opendal` 0.59),
and the Go core-api reaches it over HTTP, exactly like the mojaloop /
email / voice adapters.

**Why not the Go bindings:** the official opendal-go bindings are cgo over
`libopendal_c` — they would force a Rust shared-library build into the Go
CI job, `LD_LIBRARY_PATH` into every test process, and cgo into a binary
that is currently pure-Go static. The sidecar keeps the Go build hermetic,
keeps OpenDAL on its native toolchain, and matches the platform's
established adapter architecture. The HTTP hop is the inter-process
boundary; OpenDAL is genuinely the storage engine.

## blob-adapter (Rust)

- Operator built from env: `CE_BLOB_SCHEME` (fs default) + every
  `OPENDAL_*` variable passed through lowercased (`OPENDAL_ROOT`,
  `OPENDAL_BUCKET`, `OPENDAL_ENDPOINT`, ...). Unknown schemes / bad config
  fail at startup, not at first request.
- `PUT/GET/HEAD/DELETE /v1/blobs/{key}` — `X-Tenant-ID` required; object
  path is always `{tenant}/{key}`; key grammar is strict (no `..`, no
  empty segments, `[A-Za-z0-9._-]` segments, ≤512 chars) so the tenant
  prefix is a hard boundary. Content type is stored as object metadata and
  served back. Delete is idempotent. 16 MB body cap.
- `GET /v1/engine` → `{engine: "apache-opendal", scheme}` — provenance.
- `GET /healthz`.
- Tests: memory + fs round-trips (fs proven writing real files to disk),
  tenant isolation, key grammar, engine endpoint.

## core-api (Go)

- `internal/blobstore`: adapter client (Put/Get/Delete/Engine; 404 →
  `httpx.ErrNotFound`; 2s timeout on the engine probe).
- `store.WithBlobMedia(st, client)` decorates ANY Store — one wrap point
  in `main.go` when `CE_BLOB_ADAPTER_URL` is set. Every media call site
  (upload, public serving, console raw, delete, studio export) is
  unchanged.
- Rows keep metadata + `storage_key` (`media/{id}`) + `storage_engine`
  ("opendal"); bytes leave the row (migration 0035 adds the columns and
  backfills `size_bytes`).
- **Fail-closed writes**: an adapter error fails the upload. Falling back
  to bytea silently would be an unlabeled durability downgrade.
- **Read fallback**: rows without `storage_key` serve their legacy inline
  bytes — zero-downtime migration, no flag day.
- **Delete**: object first, then row (a blob failure leaves the row
  pointing at an existing object; replay safe because adapter delete is
  idempotent).
- `POST /v1/media/backfill`: migrates legacy rows (adapter Put →
  `MarkExternal` flips the row atomically). Idempotent; returns
  `{migrated, skipped}`; 422 when object storage is not configured.
- `GET /v1/storage/engine`: honest provenance — `legacy-inline`
  (postgres-bytea | memory) or `opendal` with the scheme fetched live from
  the adapter; an unreachable adapter is reported as such.
- Config: `CE_BLOB_ADAPTER_URL`.

## Console

Settings → Storage engine card: live engine provenance, plus a one-click
legacy→object backfill when OpenDAL is active.

## Ops

- `infra/k8s/rust.yaml`: blob-adapter Deployment + Service + PVC (fs
  backend; replicas pinned to 1 on local disk — scale freely on
  s3/gcs/azblob). `core-api.yaml` gains `CE_BLOB_ADAPTER_URL`.
- CI rust job covers the crate (`fmt --check`, `clippy -D warnings`,
  workspace tests) with no workflow changes.

## Honest limits

- Core↔adapter traffic is plain HTTP on the internal network (same
  posture as the other adapters); put mTLS at the mesh layer if needed.
- fs backend is single-node (PVC); production should select s3/gcs/azblob
  via env — no code change.
- Only tenant_media flows through OpenDAL today; receipts/exports remain
  generated-on-the-fly (nothing persisted to migrate).
