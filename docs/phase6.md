# Phase 6 — operator-approved case replies, geo stack, Sedona spatial layer, toolchain persistence

User-directed wave: the next-wave candidates from the Phase 5 report,
plus toolchain survival across sandbox wipes, PostGIS + libre-geo +
Apache Sedona integration, and a value assessment of three external
projects (oxirs, terrio, MiniCPM-OCR — verdicts in
`docs/integration-assessment.md`).

## (a) Case-drafted outbound replies with operator approval

The intake loop opened cases; this wave closes the outbound half so an
operator can answer a case without leaving the console — with the human
approval gate kept absolute.

- `POST /v1/ai/draft` on the concierge-agent: sync `ModelRouter.draft()`
  with a VIP-concierge system prompt and the case context; engine-labeled
  (`ollama/<model>`), degrades to `{"draft": "", "engine": "rules"}` when
  the daemon is down.
- `CaseService.DraftReply` (domain): builds context from title, kind,
  priority, state, summary and the last 5 notes, asks the assist client,
  advisory-empty on failure. Never auto-sends.
- `CaseService.SendReply` (domain): validates body + operator, appends the
  reply as a `HUMAN_STAFF` message on the linked conversation (CRM spine
  stays the timeline of record), posts to the WhatsApp adapter
  (`CE_WHATSAPP_ADAPTER_URL`, advisory when unset), records an operator
  note and a `case.reply_sent` audit + event.
- Routes: `GET /v1/cases/{id}/draft-reply`,
  `POST /v1/cases/{id}/send-reply`.
- Console Cases page: "Draft reply" fills an editable textarea (engine
  notice shown); "Approve & send" posts the operator-edited body and shows
  the send result. The model proposes, the human disposes.
- Intake observability: skip path now emits `case.intake_skipped`;
  `CaseMetrics` gains `by_kind`, `by_priority`, and `channel_intake`
  (per-channel prefix, e.g. `wa:`) breakdowns, rendered as a second KPI
  strip in the console.

Tests: `TestCaseDraftAndSendReply` (fake draft server + fake WA adapter;
asserts contact/body payload, HUMAN_STAFF timeline message, operator
note), `TestCaseSendReplyWithoutAdapter`, `TestCaseMetricsBreakdowns`;
agent-side `test_draft_endpoint_rules_and_model`,
`test_draft_against_fake_daemon` (redaction asserted).

## (b) Toolchain persistence across sandbox wipes

The build sandbox wipes `/tmp` and `~/.local` on recycle; `/mnt/agents`
persists but has no exec bits. Phase 6 makes restores a one-command
operation:

- `/mnt/agents/persist/toolchains/` holds `go-toolchain.tar.gz`, the Rust
  toolchain split into 100 MB parts (the mount rejects ~218 MB single
  files), and `pywheels/` (pytest, pytest-asyncio, httpx, fastapi,
  uvicorn, pydantic).
- `scripts/bootstrap-toolchain.sh` reassembles and restores all three in
  seconds (reassemble parts → untar to `/tmp`; `pip --no-index
  --find-links`), or downloads fresh when the archives are absent, and
  prints the exact `export` lines including
  `CARGO_TARGET_DIR=/tmp/rust-target` (mandatory: test binaries cannot
  execute from `/mnt`).
- Verified: Rust suite green (fmt clean, clippy clean, 4 tests) from the
  restored toolchain with the `/tmp` target dir.

## (c) PostGIS + libre geo + service areas

Full geo stack behind the same live-first, honest-degrade discipline:

- Model: `ServiceArea` (named polygon, `[lat,lng]` rings);
  `Partner` gains optional `lat/lng`.
- Store: `GeoStore` interface; in-memory `memGeo` (tenant-scoped keys);
  Postgres `pgGeo` behind the `pg` build tag using `database/sql`
  tenant-Tx — polygons stored as PostGIS `geography(Polygon,4326)` via
  `ST_GeogFromText`, read back with `ST_AsText` (closing vertex dropped).
- Migration `0006_geo.sql`: `CREATE EXTENSION IF NOT EXISTS postgis`,
  `service_areas` with GIST index and forced RLS tenant isolation,
  `partners` gains `lat`/`lng`.
- Domain `GeoService`: area CRUD with ring validation (>=3 vertices,
  coordinate bounds, `geo.area_created` event); ray-casting
  point-in-polygon (`Contains`); haversine nearest-partner ranking
  (VETTED partners only, unlocated sort last); Nominatim-compatible
  geocoder (`CE_GEOCODE_URL` / `CE_GEOCODE_API_KEY`) with a built-in
  gazetteer fallback (LOS/Lagos, DXB/Dubai, LHR/London, JFK, CDG, IST,
  NCE, Monaco, RUH) — unknown places return `ErrNotFound`, never a guess.
- Routes (`server/phase6.go`): `POST/GET /v1/geo/areas`,
  `GET /v1/geo/check?lat&lng` (serviceability), `GET /v1/geo/geocode?q=`,
  `GET /v1/geo/nearest-partners?lat&lng&limit`.

Tests: `TestGeoServiceAreas` (VI polygon containment, outside point,
validation rejections), `TestGeoCrossTenantIsolation`,
`TestGeocodeGazetteerFallback`, `TestNearestPartners` (near before far,
PENDING excluded, unlocated last).

## (d) Apache Sedona spatial analytics

`analytics/lakehouse/spatial.py` joins GPS ping events
(`chauffeur.location` / `gps.ping`) against tenant service areas into a
zone-ping-count gold table:

- Preferred engine: Apache Sedona (`SedonaContext`, spatial SQL
  `ST_Contains` join, LEFT JOIN so empty zones still report 0), labeled
  `engine: "apache-sedona"`.
- Fallback: stdlib ray-casting with Go-parity semantics, labeled
  `engine: "stdlib-raycast"`; pings outside every area land in the
  `outside` zone.
- `build_zone_pings` degrades to the fallback on `ImportError` or any
  Spark start failure — the batch never fails because an optional engine
  is absent. Output lands at `spatial/zone_pings.json`.

Tests: 3 new in `analytics/tests/test_spatial.py`, including tenant
isolation (t2 pings never match t1 areas). Sedona moves the deferred
register's Spark row from PARTIAL to "interface live, engine deferred":
the spatial SQL contract is now real and tested, swapping in the actual
engine is an ops decision, not a code change.

## (e) External-project value assessment

Full write-up in `docs/integration-assessment.md`:

- **oxirs (RDF/SPARQL/DID)** — DEFER. Trigger: a government or regulated
  tenant demanding verifiable credentials; then `oxirs-did` narrowly
  first, not the whole stack.
- **terrio (terrain/3D geospatial viz)** — DEFER. Trigger: first
  non-GeoJSON service-area import or an ops-floor 3D map requirement;
  wrap behind the `GeoService` interface so the rest of the platform
  never notices.
- **MiniCPM-OCR (1.3B OCR)** — Phase 7 pick-up candidate: WhatsApp media
  (booking confirmations, invoices, ID docs) → OCR → the Phase 5 intake
  loop, reusing the idempotency and policy that already exist. Gate on a
  CPU-latency eval before wiring it into the synchronous message path.

## Verification

All suites green locally before push: Go (gofmt/vet clean; domain,
server, tenantctx tests incl. 4 geo + 3 reply/metrics), concierge-agent
26/26, whatsapp-adapter 13/13, analytics 13/13, console `npm ci && npm
run build` clean.
