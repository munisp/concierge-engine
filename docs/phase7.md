# Phase 7 — WhatsApp media OCR intake, Sedona engine validated

User-directed wave: pick up the Phase 7 candidates from the Phase 6
report — MiniCPM-OCR media intake, Sedona engine activation check, and
the qwen3:8b GPU eval status.

## (a) WhatsApp media → OCR → intake loop

Media messages now flow through the same pipeline as text: webhook →
media download → OCR → conversation + agent turn + case intake.

- New module `whatsapp_adapter/ocr.py`:
  - `fetch_meta_media` — Meta two-step download (media id → temporary URL
    → bytes), Bearer-authenticated via `CE_META_ACCESS_TOKEN`.
  - `ocr_image` — provider chain, engine-labeled, never raises:
    `CE_OCR_URL` HTTP service (MiniCPM-OCR in production) → local
    tesseract (dev/pilot default) → honest `none`. Non-image mimes are
    explicitly unsupported rather than silently mis-OCR'd.
- `WebhookMessage` gains `media_id` / `media_mime`; the Meta webhook
  handler routes `image` and `document` messages (caption + media handle)
  instead of skipping them.
- OCR text enters the flow inline, labeled in the text itself —
  `[media OCR via tesseract]: ...` — so conversation history, case
  intake, and the audit trail all keep engine provenance. Intake
  idempotency is unchanged (`wa:<message-id>`); webhook redelivery of a
  media message still cannot double-open a case.
- Advisory at every step: media download or OCR failure degrades to
  caption-only (or an explicit `[media received — no text extracted]`
  placeholder) and the agent reply still goes out.
- Eval gate: `docs/eval-ocr-latency.md` — tesseract measures 0.14–0.74 s
  on sandbox CPU; MiniCPM-OCR 1.3B is not sandbox-hostable (same resource
  reality as qwen3:8b) and activates by setting `CE_OCR_URL`, no code
  change.

Tests: 7 new (`tests/test_ocr.py`) — real tesseract extraction, HTTP
provider preference + labeling, fallback on service failure, non-image
honesty, end-to-end media→intake with provenance assertion, advisory
failure flow, Meta webhook image routing. Suite: 20/20.

## (b) Sedona engine: validated, still opt-in

Running the deferred engine against the Phase 6 interface surfaced and
fixed a real bug the fallback had been masking: `SedonaContext.builder()
.getOrCreate()` alone never registers the Sedona catalog, so `ST_Contains`
was unresolvable. The sedona path now uses `SedonaContext.create(...)`
plus `CE_SEDONA_JAR` / `CE_SPARK_MASTER` config knobs.

Validated in-sandbox (Java 17, Spark 3.5.3, sedona 1.9.1 shaded jar):
cold start + query 9.2 s, and the `ST_Contains` join produces rows
**byte-identical to the stdlib ray-cast engine — including tenant
isolation** (a t2 ping inside t1's polygon lands in `t2|outside`, never
`t1|VI`). A skip-gated parity test (`test_zone_pings_sedona_parity`) runs
this wherever `CE_SEDONA_JAR` is set; CI keeps the deterministic stdlib
path via `CE_SPATIAL_ENGINE=fallback`.

Register status: interface live (Phase 6) → engine validated (Phase 7).
Cluster activation remains tied to the >=3 tenants / >=50k trips trigger.

## (c) qwen3:8b GPU eval — outstanding, unchanged blocker

No GPU exists in this environment and none was provisioned; the 4B model
already OOM-recycled this sandbox (documented in
`docs/eval-qwen3-ladder.md`). This stays an explicit pre-condition for
any autonomy-dial change — it is a hardware task, not a code task.

## Verification

- whatsapp-adapter: 20/20 (7 new OCR tests)
- analytics: 13 passed + 1 sedona opt-in skip; with `CE_SEDONA_JAR` set,
  14/14 including the real Spark parity run
- concierge-agent: 26/26 (untouched this wave)
- Go suites and console build: untouched this wave, last verified green
  at Phase 6
