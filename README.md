# Concierge Engine

The open-source-first operating system for premium travel & lifestyle services —
a multi-tenant, API-first platform covering the full concierge journey:
**acquisition → conversational registration → curation → payment → fulfillment**.

Design partner: Taylor Travel Management Group (Amsterdam HQ; NL · DE · FR · IT · UK · UAE).

## Architecture

Polyglot by design (per Master Spec §10):

| Layer | Technology | Role |
|---|---|---|
| Go | `services/core-api` | Domain services: tenancy, catalog, booking & quote engine, dispatch, payments API. Modular monolith, extraction-ready. |
| Rust | `crates/policy-engine` | Deterministic TIER-0..3 guardrail engine for every AI action and reply. |
| Rust | `crates/ledger-adapter` | TigerBeetle double-entry ledger adapter — idempotent postings, split settlement. |
| Rust | `crates/ingestion-parser` | High-throughput conversation/webhook ingestion parser. |
| Python | `ai/concierge-agent` | AI concierge orchestrator: tool-calling, memory, reply guardrail pipeline, human handoff. |
| Python | `ai/whatsapp-adapter` | Channel-port implementation for WhatsApp Business Platform (BSP-swappable). |
| TypeScript | `web/console` | Tenant web: SEO/booking surfaces, concierge console, dispatch board, admin. |
| Infra | `infra/` | K8s + Dapr, APISIX + openappsec edge, Kafka, Temporal, Keycloak, Postgres RLS, TigerBeetle. |

## Key design decisions

- **ServiceRequest is the atomic demand object** — a basket of service items across
  verticals (chauffeur, meet & greet, helicopter, jet, security, events, yacht) with
  one price, one payment, one timeline.
- **Event backbone from day one** — outbox pattern → Kafka; audit, webhooks,
  analytics and AI training data fall out for free.
- **TigerBeetle holds money truth; Postgres holds business state** — reconciled by
  Temporal workflows. Mojaloop interop is Phase 2+ (adapter interface is already
  PSP/Mojaloop-agnostic).
- **The LLM never has the final word** on money, identity, or outbound text:
  every proposed action passes the Rust policy engine (TIER-0..TIER-3), every reply
  passes the 5-stage output pipeline (schema → policy → tone → factuality → privacy).
- **WhatsApp is an adapter, not a foundation** — channel-port interface; BSP
  (Infobip/Twilio) is tenant-level configuration.

## Repository layout

```
contracts/    Shared protobuf + JSON-schema contracts (gRPC/HTTP across languages)
db/           Postgres migrations (RLS-first, tenant-scoped)
services/     Go domain services (modular monolith)
crates/       Rust critical paths (policy, ledger, ingestion)
ai/           Python AI concierge + channel adapters
web/          TypeScript web surfaces
infra/        K8s, Dapr, APISIX, Keycloak, observability
docs/         Architecture notes, runbooks, phase plan
```

## Quick start (local dev)

```bash
make dev-infra     # postgres, redis, kafka, temporal, keycloak, tigerbeetle
make migrate       # apply db/migrations
make run-core      # Go modular monolith (in-memory stores by default; CE_STORE=pg for Postgres)
make run-policy    # Rust policy engine  (requires cargo)
make run-ledger    # Rust TigerBeetle adapter
make run-ai        # Python AI concierge
make run-wa        # WhatsApp adapter
make run-web       # Vite dev server for web/console
```

## Phase status

**Phase 1 (MVP) — delivered.** Tenant core + admin, white-label web + WhatsApp
capture, quote & booking engine (ground vertical), dispatch board + chauffeur
app v1 (token-authed trip PWA), Stripe-style payments on TigerBeetle, SEO
foundation (static tenant×vertical landing pages), AI concierge v0–v1 with
guardrails + HITL, K8s/Dapr/APISIX/Keycloak/Kafka/Temporal foundations,
VIP field-level encryption + sensitive-read audit log (§13).

**Phase 2 — delivered (see `docs/phase2.md`).** Mojaloop FSPIOP adapter +
cross-border partner payout saga with compensation, partner network with
fill-rate KPIs, corporate accounts + consolidated invoicing, charter supply
port (Avinode-ready, mock fallback), lakehouse bronze/silver (`analytics/`),
AI v2 (PII redaction before inference, shadow mode, judged evals).

**Hardening wave.** Temporal payout-saga binding (`workers/` — durable
quote/execute/await/compensate with signal + poll backstop, tested on the
time-skipping server), live Stripe Checkout Sessions + HMAC-signed webhook
ingress (sandbox fallback without keys), Meta Cloud API direct adapter with
X-Hub-Signature-256 webhook verification and tenant routing by phone_number_id.

**Phase 3 — delivered (see `docs/phase3.md`).** Public API + webhooks
marketplace (`cek_` keys with scope enforcement; HMAC-signed at-least-once
deliveries), cross-tenant referral network with ledger-settled commissions,
GDS/hotels via Amadeus NDC (live-first, honest mock degrade, attach-rate
tracking), ML v0 gold layer (LTV/churn/pricing heuristics with traceable
rules), new-entrant instant-on, UAE residency, SOC 2 control mapping
(`docs/soc2-control-map.md`).

**Phase 4 — delivered (see `docs/phase4.md`).** Self-hosted inference via
Ollama (llama.cpp) serving Qwen with PII redaction before inference and a
deterministic rule floor; next-generation case management on top of the CRM
(AI-triaged cases with priority SLA clocks, state machine, tasks, timeline,
engine-provenance metrics), console Cases page.

**Phase 5 — delivered (see `docs/phase5.md`).** WhatsApp → case intake loop
(`POST /v1/cases/intake` policy: incidents/VIP/P1-P2 open cases idempotently,
routine messages stay conversational); gold-layer ML v1 — fitted retention
curve for LTV, empirical-hazard survival model for churn, SPC pricing
signals (stdlib, deterministic, model-labeled rows); real-model eval ladder
(`docs/eval-qwen3-ladder.md`) with the `CE_OLLAMA_TIMEOUT` knob and thinking
suppression for structured tasks.

**Phase 6 — delivered (see `docs/phase6.md`).** Operator-approved case
replies (AI draft → editable console textarea → approve & send over
WhatsApp, HUMAN_STAFF timeline + audit, intake metrics breakdowns); full
geo stack — PostGIS `geography(Polygon,4326)` service areas with RLS,
ray-cast serviceability checks, haversine nearest-partner ranking,
Nominatim-compatible geocoding with gazetteer fallback; Sedona spatial
analytics (`ST_Contains` zone-ping gold table, stdlib fallback);
one-command toolchain persistence across sandbox wipes
(`scripts/bootstrap-toolchain.sh`); external-project assessment
(oxirs/terrio deferred with triggers, MiniCPM-OCR queued as a Phase 7
candidate — `docs/integration-assessment.md`).

**Phase 7 — delivered (see `docs/phase7.md`).** WhatsApp media → OCR →
intake loop (Meta image/document messages download + OCR via engine-labeled
provider chain: `CE_OCR_URL` MiniCPM-OCR service → local tesseract →
honest none; OCR text feeds conversation/agent/case-intake with full
provenance, idempotency unchanged, failure advisory); Sedona engine
validated against the Phase 6 spatial interface — `SedonaContext.create`
registration fix, real Spark `ST_Contains` join byte-identical to the
stdlib fallback including tenant isolation, opt-in parity test via
`CE_SEDONA_JAR`; OCR latency eval in `docs/eval-ocr-latency.md`
(tesseract 0.14–0.74 s on CPU; MiniCPM-OCR activates by config once
hardware exists).

**Phase 8 — delivered (see `docs/phase8.md`).** WhatsApp voice notes →
transcription → intake loop (audio messages download + transcribe via
engine-labeled provider chain: `CE_TRANSCRIBE_URL` whisper.cpp service →
local faster-whisper tiny → honest none; persisted 75MB model snapshot
survives sandbox wipes); latency eval `docs/eval-voice-latency.md`
(11s of speech in 0.6–0.7s CPU, exact on the JFK fixture, ogg/opus
validated); media block routes audio→transcription, images→OCR, same
provenance/idempotency/advisory discipline.

**Phase 9 — delivered (see `docs/phase9.md`).** Video notes → ffmpeg
keyframe sampling (1 fps, `CE_VIDEO_MAX_FRAMES`) → Phase 7 OCR chain →
intake, real ffmpeg-rendered fixture gate in CI; interactive replies
(list/button taps) routed into intake as typed-equivalent text with option
ids kept for provenance — the webhook now handles every message type the
platform itself can send.

**Phase 10 — delivered (see `docs/phase10.md`).** Location messages →
dropped pins fold into intake text with an advisory serviceability verdict
from the geo service (`→ serviceable: yes (area)`; geo failure never
invents a verdict); Infobip inbound parity — `POST /webhooks/whatsapp/
infobip` normalizes text/media/location, media fetches are provider-
agnostic (`fetch_media`: direct URL + App-key auth for BSPs, Meta two-step
for ids), so all Phase 7–9 extraction works on BSP media unchanged.

**Phase 11 — delivered (see `docs/phase11.md`).** Delivery/read receipts:
Meta `statuses[]` and Infobip status callbacks are normalized and recorded
as idempotent, tenant-isolated SLA evidence (`POST/GET
/v1/channels/receipts`, counts endpoint); `SendReply` now captures the
provider `wamid` so outbound replies join inbound status callbacks.
Contacts/vCard intake on both channels. Console geo page (`/geo`):
service-area CRUD, serviceability tester, nearest partners. Latent fixes:
geo verdicts no longer degrade against real core responses (areas are
strings, not dicts), and the `pg` build tag compiles for the first time
(pgx module, `platformTx` receiver, missing `Geo()` accessor).

**Phase 12 — delivered (see `docs/phase12.md`).** Receipts now alert:
`SendReply` links each provider `wamid` to its case (idempotent
receipt_links, migration 0008); a `failed` receipt on a linked reply pulls
the case out of waiting-on-client, escalates, and notes the evidence — a
`read` receipt annotates the case with proof-of-delivery. Console
`/receipts` board: status counts, failure table correlated to cases,
message lifecycle + per-case evidence lookups. New CI job `pg` runs the
store against real Postgres+PostGIS as a non-superuser role (RLS actually
applies) — which caught and fixed four latent pg bugs: 0007's TEXT→uuid
FK mismatch (migration never applicable), missing `::uuid` casts in the
0007/0008 RLS policies, `""`→uuid casts in `pgCases` writes, and NULL
columns scanned into plain strings.

**Phase 13 — delivered (see `docs/phase13.md`).** `case.reply_failed` now
pushes a WhatsApp alert to the operator (`CE_OPERATOR_PHONE`, advisory) —
SLA breach evidence lands where operators look. Gold layer gains
`channel_sla.json`: per tenant|channel failure rate and median
sent→delivered / sent→read latencies from receipt evidence, replay-safe
(first status observation wins), labeled `receipt-latency/v1` as measured
statistics, not prediction. The live-pg CI gate expands to the Phase 1–3
CRM spine (clients, catalog→request→quote→booking, conversations,
partners, referrals under the bilateral RLS policy), catching three more
latent pg bugs pre-push: partners never roundtripped `lat`/`lng` (geo
ranking would have been blind on pg), and referrals `""`→uuid writes /
NULL scans on `request_id`/`booking_id`.

**Phase 14 — delivered (see `docs/phase14.md`).** Acquisition & outreach
hardening. Proactive outreach is new: `OutreachService` sends through the
channel adapter with marketing consent-gated (`client.Consent["marketing"]`),
frequency-capped (3 per rolling 7 days, service sends exempt), idempotent
per `(tenant, idempotency key)` (replay never re-sends), and wamid-captured
so Phase 11 receipts close the delivery loop; gated/failed sends are
recorded with reasons, never silently dropped. Attribution gains first-touch
inheritance (requests/bookings carry the client's earliest
channel/campaign), a funnel endpoint with conversion rates, and
deterministic client referral codes (`VIP-XXXXXXXX`) with referral
conversion tracking on the referee's first booking (`referral.converted` +
value-carrying `referral_converted` attribution; referrer ledger reward is a
documented deferral). Capture page accepts `ref`/`channel`/`campaign` (and
utm aliases); console gains a Growth page (funnel, referral codes, outreach
composer with live consent/cap verdict, send log). Live-pg CI gate covers
the new outreach store, attribution list/funnel, and referral-code lookup.

**Phase 15 — delivered (see `docs/phase15.md`).** Registration/CRM-360,
international payments, integration hardening. CRM: consent write API (the
surface outreach gating reads), profile enrichment with locale/timezone,
client search, a 360 aggregate view (preferences, requests, bookings, cases,
conversations, first touch, spend, outreach), and transactional duplicate
merge that re-points every client-scoped record. Payments: Paystack adapter
(card/bank transfer/USSD/mobile money for NGN/GHS/KES/ZAR) with HMAC-SHA512
webhook verification, currency-based PSP routing with explicit override,
refund initiation (captured-only, replay-safe, webhook-settled), ISO-4217 +
zero-decimal currency hygiene, a payment-methods discovery endpoint, and
localized Stripe Checkout (client locale + email). Integrations: event
catalog endpoint (every kind documented for subscribers), webhook delivery
replay, secret rotation, synthetic test events. Console gains a Clients 360
page; Integrations gains rotate/test/replay and the live catalog.

**Phase 16 — delivered (see `docs/phase16.md`).** EU/US-first readiness
(launch order corrected: Europe/USA before Nigeria). Payments: per-market
Stripe Checkout rails — EUR (SEPA/iDEAL/Bancontact/giropay/Sofort/Klarna),
USD (ACH/Cash App/Klarna/Link), GBP (Bacs), CHF (TWINT), Nordics (Klarna),
PLN (BLIK/P24) — with SCA/3DS2 enforced by the hosted page; Paystack stays
for the Africa wave. Data rights: GDPR Art. 15 export (one JSON document,
audit-logged) and Art. 17 / CCPA erasure — identity scrubbed to a
pseudonymous tombstone, preference memory deleted, consent revoked, financial
history retained for accounting, every write surface (profile, consent,
preferences, outreach, merge-into) refuses further processing of erased
subjects. Regional consent defaults at registration (EU/EEA/UK/CH opt-in,
US/CA opt-out, unknown conservative) derived from phone calling codes.
Registration captures browser locale/timezone automatically.

**Phase 17 — delivered (see `docs/phase17.md`).** Tax/VAT readiness +
per-tenant payment config. Per-tenant tax engine — none (default), flat
(basis-points rate, inclusive or exclusive per market convention), or live
Stripe Tax with honest flat fallback; tax line on the quote, deposit on the
grand total, engine provenance stored (`tax_engine`) and emitted on
`quote.created`; Stripe Checkout marked tax-inclusive. Tenant payment config
disables PSP rails per tenant in routing and methods discovery. Migration
0012. 7 domain + 1 pg tests.

Deferred components and un-defer triggers: `docs/deferred-register.md`.
