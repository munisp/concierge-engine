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

**Phase 18 — delivered (see `docs/phase18.md`).** Referrer ledger rewards +
pg CI coverage extension. The Phase-14 referral spine now pays out: the
referrer accrues credit on the referee's first booking (5% of booking value
by default, tenant-overridable via `PricingRules.ReferralRewardPct`;
idempotent per source booking; self-referrals excluded). Accrued credit is
applied to the referrer's own payment links (`reward_applied_minor` — the
intent keeps the full obligation, the PSP charges the reduced amount),
redeemed oldest-first at capture with a `PLATFORM_FEES → CLIENT_CLEARING`
compensation posting, and returned to the balance on refund — a refund
never burns referral credit. Migration 0013 (`client_rewards` + RLS).
pg CI gate extended to rewards (incl. cross-tenant RLS), dispatch,
payments, handoffs and zero-`expires_at` quotes. 5 domain + 5 pg tests.

**Phase 19 — delivered (see `docs/phase19.md`).** Reward sharing program.
Two-sided referral credit per tenant (`reward_program` jsonb, patched via
PUT /v1/tenant): referrer earns `referrer_pct` of the referee's first
booking (legacy `ReferralRewardPct` fallback, then 5%), the referee earns a
`referee_pct` welcome credit on the same booking; per-accrual cap
(`max_reward_minor`), optional expiry (`expiry_days`, 0 = never — credit is
never silently burned), kill-switch (`disabled`). Expiry is lazy-swept to
EXPIRED on balance/redeem/list access; lapsed credit never redeems but
stays visible in history. `GET /v1/clients/{id}/rewards` returns the
expiry-aware balance + full history; console growth board gains a rewards
lookup (balance + history table). Redemption, compensation posting and
refund safety unchanged from Phase 18. Migration 0014. 5 domain + 2 pg
tests.

**Phase 20 — delivered (see `docs/phase20.md`).** Promo campaigns + growth
config + reward visibility. Tenants mint fixed-credit promo codes
(`POST /v1/promos`, console growth board); clients redeem once each
(`POST /v1/promos/redeem`, case-insensitive, structural idempotency via
partial UNIQUE on promo_code); credit lands in the same `client_rewards`
ledger as referral rewards (kind `promo_credit`, NULL source booking) and
redeems against payment links with the Phase 18 mechanics. Campaigns carry
redemption caps and validity windows (credit expires with the campaign).
Per-tenant `outreach_config.marketing_cap_7d` overrides the platform
frequency cap. Capture page: promo input + "reward credit applied — you pay
X" from the intent's actual `reward_applied_minor`; WhatsApp agent payment
links carry reward fields so replies state the applied credit honestly.
Migration 0015. 5 domain + 2 pg tests.

**Phase 21 — delivered (see `docs/phase21.md`).** Tenant-managed outreach
templates (`POST/GET/DELETE /v1/outreach/templates`, upsert by name,
server-side `{{client_name}}`/`{{tenant_name}}` rendering with the rendered
text recorded for provenance, marketing templates refused on the service
lane); per-tenant operator rosters (`operator_config.alert_phones` override
`CE_OPERATOR_PHONE` for SLA failure alerts, empty = fallback); WhatsApp
message reactions normalized on Meta + Infobip into conversation-timeline
provenance lines that skip intake and the agent turn; webhook dual-secret
rotation window (24h default, deliveries signed with both secrets during the
window, latent pg secret-persistence bug fixed); event schema versioning
(envelope `schema_version` + versioned catalog). Migration 0016. 5 domain +
4 pg + 5 adapter tests.

**Phase 22 — delivered (see `docs/phase22.md`).** Scheduling v1:
assignments carry service windows resolved from the booking
(`pickup_at` + `hours`, tenant `dispatch_config` fallback), with conflict
gating (409 naming the conflicting assignment), end-padded turnaround
buffers (default 30m), terminal statuses freeing resources, and
`GET /v1/dispatch/availability` for busy/free windows per fleet resource.
Correspondence threading fixed: `Open` is get-or-create on the newest
non-CLOSED thread per (client, channel), so WhatsApp history no longer
fragments into one conversation per message and the funnel counts threads,
not messages. Cross-channel: `GET /v1/clients/{id}/conversations` plus a
web "Message your concierge" box on the capture page sharing the same
threaded model as WhatsApp. Migration 0017. 5 domain + 3 pg tests.

**Phase 23 — delivered (see `docs/phase23.md`).** Voice concierge channel:
`ai/voice-adapter` (FastAPI) puts real-time calls on the platform —
LiveKit as the self-hostable WebRTC/SIP media plane, Pipecat as the
VAD/turn-taking/STT→TTS pipeline over its LiveKit transport, and the
existing concierge-agent as the LLM stage (`/v1/turns`), so policy gating,
tool tiers, budget caps and shadow mode apply to voice unchanged. Every
spoken turn is a provenance line on the client's VOICE thread (Phase 22
threading: repeat calls continue one conversation), per-turn advisory case
intake idempotent at `voice:{session}:{turn}`, barge-in markers, signed
LiveKit webhooks (HS256 + body-sha256 binding, stdlib-verified), session
API with tenant scoping and hangup. Config-gated (`CE_VOICE_ENABLED` +
provider keys); `/health` and 503s list exactly what's missing; live media
cutover steps in `docs/deferred-register.md`. 33 tests, hermetic (no audio
hardware required).

**Phase 24 — delivered (see `docs/phase24.md`).** Operational control
surface: `PUT /v1/tenant` switched to selective patch semantics (pointer
fields — a partial body no longer zeroes the config objects it omits;
empty body is a no-op, closing a wipe-by-omission footgun); console
`/settings` page editing every tenant config object (operator roster,
dispatch windows, tax engine, payment rails, reward program, outreach cap,
quote SLA) one safe card at a time; voice channel gains operator takeover
(`POST /v1/voice/sessions/{id}/takeover` — room-scoped staff token +
SYSTEM provenance line, 409 on ended sessions) and a tenant-scoped
sessions board (`GET /v1/voice/sessions?active=1`). 1 server + 4 voice
tests.

**Phase 25 — delivered (see `docs/phase25.md`).** Client journey tracking
& lifecycle follow-ups. Passengers get an Uber-like monitoring link:
`track/`-prefixed HMAC tokens (a separate family — driver `ParseToken`
rejects them and vice versa, so a monitoring link can never drive the
trip), `GET /v1/track/{token}` (tenant-middleware exempt, like the
chauffeur routes), a console `/track` page with a status stepper and
5s-polled live position that says "position pending" until the
chauffeur's first real stamp, and a "Client link" mint button on the
dispatch board. Tenants get all-year engagement without manual
remembering: a lifecycle engine (`domain/followups.go`) sends post-trip
thank-you-and-one-preference messages (service lane — no marketing
consent needed) 24h after trip completion and win-backs after 90 days
quiet (marketing lane — consent-gated, non-consenting clients recorded
held, never contacted), all through the Phase 14 outreach engine with
per-assignment / per-client-per-ISO-week idempotency so the hourly
sweeper (and `POST /v1/followups/run`, plus the Settings lifecycle card
with kill-switch and cadences) never double-fires. 5 domain + 2 server +
3 pg tests.

**Phase 26 — delivered (see `docs/phase26.md`).** New-client acquisition
monetization: the 10%-of-transactions model made honest and automatic.
Acquisition fee billing assesses `fee_pct` (default 10%) at payment
settlement, only on a client's **first** completed booking and only when
the first-touch channel is one of the tenant's acquisition channels
(default seo/referral/outreach/ads/partner; `ref:` campaigns classify as
referral) — posting `TENANT_REVENUE → PLATFORM_ACQUISITION_FEES`
idempotently, with refunds clawing back the exact original posting (never
recomputed). `GET /v1/billing/acquisition-fees` produces an evidence-grade
invoice: attributed GMV per line, gross/clawback/net. The public SEO site
(`/site/{slug}/` + per-service and per-area pages, SSR html/template,
JSON-LD LocalBusiness/Offer, sitemap.xml + robots.txt) gives every tenant
a crawlable acquisition surface whose CTAs feed the Phase 14 attribution
spine (`?tenant=` capture handoff stamped `channel=seo`). The 90-day
cohort tracker (`GET /v1/acquisition/cohort`) counts honestly-scoped new
clients against the goal with pacing, projection, per-channel split and a
Monday 09:00 UTC WhatsApp pacing alert to the operator roster; console
`/growth` shows the cohort card + fee invoice, `/settings` edits the
acquisition config (kill-switch, fee %, channels, goal). Migration 0019.
4 domain + 2 server + 1 pg tests.

**Phase 27 — delivered (see `docs/phase27.md`).** Tenant onboarding as a
derived journey, plus ten acquisition innovations. Onboarding step
statuses are **computed from real resources on every read** (catalog,
areas, brand, payments, WhatsApp, roster, promos, bookings, cohort) —
never stored, so the checklist cannot drift from reality; the only
persisted state is track + skipped steps. Two playbooks: **new** tenants
get greenfield provisioning (Phase 3 instant-on seeds), **existing**
tenants get the same steps with import/connect actions —
`POST /v1/import/catalog` and `/v1/import/clients` are idempotent
(name/phone dedupe), and imported clients are consent-clean
(marketing=false, `imported` marker, first-touch channel `import`,
deliberately outside the acquisition taxonomy — no 10% fee on a
pre-existing book of business). Ten acquisition innovations, all
terminating in the Phase 14 attribution spine: QR booking codes (PNG,
channel=qr), link-in-bio page (`/site/{slug}/links`, channel=social),
WhatsApp click-to-chat deep links per service, post-trip reviews with
consent-gated JSON-LD aggregateRating rich snippets, gift vouchers
(forced single-redemption, `gift:` attribution), referral boost windows,
dormant-lead nudges (ISO-week idempotent lifecycle rule), embeddable
booking widget (`/embed/{slug}`, channel=widget), competitor
switch-offer codes (operator alert on redeem), and partner co-marketing
links with per-partner funnel metrics. Console: `/onboarding` setup
journey page, review form on the client tracking page, lifecycle
lead-nudge + reward boost settings, promo gift/switch flags. Migration
0020. 8 domain + 4 server + 3 pg tests.

**Phase 28 — delivered (see `docs/phase28.md`).** SEO optimization wave —
the ten levers that decide whether the acquisition surface actually ranks
and converts. Custom domains: `site_config.custom_domains` + host-rewrite
middleware serve the entire site (pages, sitemap, robots, OG cards) on the
tenant's own domain so link authority compounds on their brand, not the
platform's (header-spoof-guarded; unknown hosts fall through). Thin
content fixed with guide sections assembled strictly from real data
(services, prices, areas, payment rails — never invented copy). Structured
data expanded to a per-page `@graph`: LocalBusiness with catalog-derived
`priceRange`, coverage-centroid `geo`, `sameAs` socials, consented
`Review` nodes; Service + Offer + `areaServed` + FAQPage + BreadcrumbList
on service pages. Dynamic branded OG images (`/og.png`, pure-Go x/image
rendering, deterministic, long-cached) turn every shared link into a
rich card. Per-page unique titles/descriptions; FAQ sections from real
catalog attributes; ETag + 304 + cache headers; sitemap `lastmod` +
platform-level sitemap index; hreflang/locale; Search Console
verification meta; public partner directory (VETTED only) with
partner-attributed booking links. Console Settings gains the Public site
& SEO card. Migration 0021 (`tenants.site_config`, `GetByDomain` jsonb
containment). 5 server + 1 pg tests.

**Phase 29 — delivered (see `docs/phase29.md`).** Multi-locale site engine,
premium redesign, AI concierge surfacing — reference tenant DDPortugal
(Lisbon luxury tours). `site_config.locales` (first = default, canonical
unprefixed URLs; alternates at `/site/{slug}/{locale}/...`, unconfigured
prefixes 404) with a full hreflang cluster + per-locale canonicals and
sitemap trees. Per-resource i18n (`name_i18n`/`desc_i18n` jsonb on catalog
services and service areas, `brand_i18n` on tenants — migration 0022);
chrome dictionaries for en/pt-PT/de/fr/es; guide + FAQ prose fully
translated en + pt-PT, honest English-body degrade elsewhere (no
machine-translated marketing copy). Template rebuilt to a top-player
layout: hero with rating chip + dual CTA, monogram tour-card grid,
conditional trust bar (claims render only when true), sticky mobile CTA,
partner footer — pure SSR/CSS, no external assets. Every page carries a
WhatsApp chat CTA wired to the AI concierge pipeline (intake →
policy-gated agent turns → RAG similar-case recall → rule floor → human
handoff → wamid receipts + SLA evidence). OG cards ascii-fold diacritics.
`scripts/seed-ddportugal.sh` seeds the bilingual brownfield demo.
Single-dispatcher routing under `/site/{slug}/` resolves locale prefixes
manually (ServeMux wildcard/literal overlap). 4 server + 1 pg tests.

**Phase 30 — delivered (see `docs/phase30.md`).** Site Studio + tenant
insights. Open-source builder research (WordPress GPL / Ghost MIT /
Webstudio-Silex-VvvebJs AGPL — rejected for SaaS embedding / GrapesJS
BSD-3 / Puck MIT) concluded the studio belongs as a **data layer on the
existing SSR engine**, not an embedded canvas: five theme presets +
custom tokens on `site_config.theme` (no migration; CSS custom-property
override, live on next request), business-case content generation
(concierge-agent with deterministic template fallback, provenance-labeled,
draft-only until applied, all-or-nothing service-ID validation), and five
deployment shapes as copy-paste artifacts — hosted, custom domain, static
zip export for split-hosting (internal links relativized, booking CTAs
stay absolute to the platform), CMS iframe embed, and a generated
WordPress `[concierge_book]` shortcode plugin. Tenant insights:
`GET /v1/analytics/overview` (bookings, GMV vs collected revenue, service
mix, channels, fleet utilization, driver ratings, reviews — straight from
OLTP stores, honesty notes for the 10h/day utilization heuristic) plus
console `/studio` and `/insights` dashboards. 5 server + 4 domain + 2 pg
tests.

Deferred components and un-defer triggers: `docs/deferred-register.md`.
**Phase 31 — delivered (see `docs/phase31.md`).** Media pipeline + premium
site template v2. Tenants upload site imagery (`POST /v1/media`, JSON
base64, raster-only with content sniffing, 8 MB cap, tenant-scoped bytea
with RLS) served publicly at `/site/{slug}/media/{id}` with immutable
cache headers; catalog services carry `image_id` (validated at write —
dangling ids rejected) and `site_config.hero_image_id` sets the landing
hero. The SSR template is rebuilt: sticky glass nav, full-bleed hero with
gradient overlay (theme-gradient fallback), stats strip (services / areas
/ rating), 16:9 image service cards with monogram fallback, how-it-works,
a dark platform-features band (instant booking, WhatsApp concierge, live
tracking, secure payments — payment claims still vanish when no rail is
enabled — and vetted fleet) with the inline `/embed/{slug}` booking
widget, consented review quotes, area pills, FAQ and CTA band. Static
export now bundles referenced media files and rewrites URLs to the flat
layout. New chrome strings ship full en + pt-PT, English backfill
elsewhere (honest degrade). 3 server + 2 pg tests.

**Phase 32 — delivered (see `docs/phase32.md`).** End-to-end onboarding +
console gap closure + design layer. **Payment honesty:** the site's
payment claims (trust chip, platform band, FAQ answers) now gate on
`LiveRails` — a rail is live only when the platform holds the PSP key AND
the tenant hasn't disabled it; with no keys the FAQ honestly says the
concierge arranges payment directly, and the onboarding payments step
reports SANDBOX mode. **Onboarding** grows to 21 derived steps in four
groups (essentials / operations / presence / growth): fleet, drivers,
pricing, tax, dispatch tuning, site design, imagery, socials, custom
domain, templates, lifecycle, campaigns, first booking — every step
carries the console path that completes it, rendered as grouped "Open →"
links. **Console gaps closed:** new Catalog page (create/edit/price/
deactivate services, card images from the media library — backed by the
extended PATCH-shaped `PUT /v1/catalog/{id}` and a new active-agnostic
`CatalogStore.Get`, fixing the can't-reactivate-a-deactivated-service bug
the new tests caught), Dispatch fleet/crew add forms, Studio media
library (upload, thumbnails via tenant-scoped `GET /v1/media/{id}/raw`,
set-hero, gallery membership, delete with reference cleanup), and the
Onboarding page's grouped steps. **Design layer:** `site_config.design`
splits layout from theme tokens — `classic` (default) or `editorial`
(oversized serif display, asymmetric hero, alternating service rows, pure
CSS re-flow of the same DOM); `hidden_sections` toggles any of the nine
landing sections; `gallery_ids` (≤8) renders a gallery strip, dangling
ids skipped at render. `ValidateSiteConfig` now runs on BOTH write paths
(the `PUT /v1/tenant` wholesale patch previously bypassed theme
validation). Puck (MIT) evaluated as a future visual site builder —
documented spike, recommendation: stay curated. 5 server + 2 pg tests.

**Phase 33 — delivered (see `docs/phase33.md`).** Imagery honesty across
the onboarding/catalog/media triangle. New derived `service_imagery`
onboarding step (22 total): every ACTIVE service must carry a card image
— coverage reported honestly ("1 of 2…"), empty catalogs guided to
services first, inactive services don't block. `DELETE /v1/media/{id}`
now scrubs every reference BEFORE deleting (catalog `image_id` on active
AND inactive services via the new `CatalogStore.ListAll`, site hero,
gallery) — a scrub failure leaves media and references consistent instead
of dangling. Render-time defense for legacy rows: every public-site media
reference resolves through `liveMediaURL`, so a dangling id falls back to
the gradient/monogram and never reaches the HTML. Console: "no card
image" badge on the Catalog page; Studio media delete simplified to
del + reload. 4 server + 1 pg tests.

**Phase 34 — delivered (see `docs/phase34.md`).** Taylor Travel Management
reference demo (`scripts/seed-taylor.sh`): flagship tenant shaped after
the real taylortravelmanagement.com business — 9 service lines split
honestly between instant-quotable (airport transfer, hourly chauffeur,
meet & greet, intercity — base + per-km/per-hour priced in seconds) and
on-request (event transport, helicopter, private jet, yacht, close
protection — manual quote, no fabricated prices); NL VAT, EUR 30%
deposit, en+de locales, Schiphol office WhatsApp entry, Amsterdam/Schiphol
+ Dubai service areas, and 10 AI-generated editorial assets wired through
the media API (hero + 9 card images + gallery), which flips the Phase 33
`service_imagery` onboarding step to done. Static export published as a
shareable preview (booking CTAs → WhatsApp, embed → static panel).

**Phase 35 — delivered (see `docs/phase35.md`).** Customer acquisition &
outreach: 30 innovations. Acquisition — airport SEO pages gated on real
geo coverage, city-pair route pages priced from the live catalog, an
anonymous instant-estimate endpoint, shareable HMAC quote links with a
no-store public page, referral leaderboard, per-channel capture link
builder, Referer auto-attribution, first-booking credit, corporate
self-serve signup, honest social proof, review deep links, campaign
windows that retag every CTA, embed theming, a gift voucher storefront
that 404s without stock, and sitemap coverage that never lists a 404.
Outreach — segment broadcasts with a preview that IS the audience, a
rule-based segment engine, drip sequences derived from the sends ledger
(no state table), deterministic template A/B, STOP/START consent
keywords at intake, scheduled sends with delivery-time consent
re-validation, quiet hours with per-client preferred send time, quote
abandonment nudges carrying the share link, tiered win-back, anniversary
and re-permission campaigns, review requests, journey notifications on
every trip transition, monthly corporate summaries, and a per-campaign
funnel. Console Growth page gains four cards (links, leaderboard,
broadcast, funnel) and the composer schedules sends. 17 domain + 11
server + 1 pg tests, all suites green.

**Phase 36 — delivered (see `docs/phase36.md`).** Lead generation engine:
first-class prospects in four kinds (person, corporate, partner, event)
with a pipeline (NEW → CONTACTED → QUALIFIED → CONVERTED / LOST),
deterministic 0–100 scoring with human-readable reasons, and conversion
into the platform's real records (client link, CorporateAccount + OrgID
mirror, PENDING partner). A lead is not a shadow client — capture
resolves the messaging-identity client through ResolveV2, so all nurture
(consent gates, caps, quiet hours, sequences, STOP/START) flows through
the outreach engine unchanged. Repeat inquiries dedup onto open leads
(backfill + re-score); keyed replays conflict to a quiet 200. Public
capture at `/site/{slug}/leads` carries utm/referer attribution and a
honeypot. Speed-to-lead SLA pages the operator roster on breached NEW
leads, once per lead. Segments gain `lead_kind`; sequences gain declarative
segment audiences. Console Leads page: pipeline board, filters, capture
form, convert/transition actions. 13 domain + 7 server + 3 pg tests
(pg verified against a local Postgres+PostGIS with RLS-active role), all
suites green.

## Phase 37 — Greenfield bootstrap: bulk lead import + board/feed console

Zero-customer tenants bootstrap the pipeline from a spreadsheet or CRM
export: `POST /v1/leads/import` batches up to 500 rows through the Capture
path itself — same dedup/backfill, scoring, ResolveV2 client link,
grant-only consent mirror and lead_captured attribution (source=import) —
with advisory per-row reject reasons and replay-safe keyed re-imports.
Imported leads are immediately segment-visible (`lead_kind`) and
broadcast-able. Console: Leads gains a kanban Board view (default) beside
the table plus a CSV/JSON import panel with receipts; Growth's recent
outreach sends render as a phone-style bubble feed (sent/held/scheduled/
failed chips) with the dense table one toggle away. Capture validation
messages are now descriptive (sentinel-preserving). 4 domain + 2 server
tests, all suites green.

## Phase 38 — Lead provenance: lawful contact basis for acquired lists

Bought and partner-sourced data enters the pipeline without pretending to
be organic: leads carry `list_source` (vendor/partner) and
`contact_basis` (self_reported | partner_consent | b2b_legitimate_interest,
migration 0026). Capture validates the basis and refuses marketing consent
on a B2B basis (422); imports default undeclared rows to
b2b_legitimate_interest and strip claimed consent into receipt `notices`
(the row still imports — human-outreach-only until the prospect opts in).
Consent earned on a repeat touch dedups into the existing lead, grants the
messaging identity (grant-only) and re-scores upward while provenance is
preserved. The public site surface asserts self_reported itself.
`SegmentRules.contact_basis` joins through open leads (broadcast preview +
composer selector); the Leads board/table show basis and list source. The
loop stays: acquire early → prioritize by score → nurture automatically →
convert to durable accounts → attribute everything → fund the next event
from evidence. 4 domain + 3 server + 1 pg tests, all suites green.

## Phase 39 — Email outreach channel

Bought data is email-first: the platform can now lawfully *use* the lists
Phase 38 made lawful to hold. Outreach gains a second channel —
`channel: email` on sends and broadcasts (subject required, recipient is
the client's email, `subject` persisted through the scheduled-send sweep,
migration 0027). Frequency caps are per channel; the b2b-introduction lane
permits at most two marketing emails to a client reachable only via an
open b2b-sourced lead, then closes until consent is earned on a reply.
Every marketing email's recorded body carries a mandated unsubscribe footer
with a one-click `unsub/` HMAC link (public, idempotent, flips the shared
consent spine; tampered or cross-family tokens refused). The new
`ai/email-adapter` service builds the compliance MIME (List-Unsubscribe +
one-click headers), delivers via SMTP or degrades honestly to a sandbox
outbox, serves open/click tracking on HMAC-tokened public endpoints,
forwards ESP events (a complaint flips marketing consent off), and bridges
inbound email into the same resolve → conversation → intake → agent loop
as WhatsApp. 6 domain + 2 server + 1 pg + 13 adapter tests, all suites
green.

## Phase 40 — Campaign economics + email deliverability guardrail

The loop's last clause — "fund the next event from evidence" — now has a
cost side. The `campaign_spend` ledger (migration 0028) records what a
campaign cost (idempotent per tenant+key, FORCE RLS), and the campaign
funnel joins it to the attribution spine: spend, attributed value,
cost-per-conversion and ROAS per campaign, with mixed-currency spend
flagged and its ratios withheld rather than blended into a lie. The
economics board (`GET /v1/outreach/campaigns`) unions every campaign the
tenant ran or paid for. On the email lane, ESP bounces and complaints now
land on the attribution spine (`email_bounced`/`email_complained`), and a
tenant-level deliverability guard holds marketing email when the 7-day
bounce+complaint rate exceeds 5% at ≥20 sends — service email exempt,
WhatsApp untouched, self-healing as the bad window ages out; sweep-
delivered scheduled sends count toward the guard's denominator so the
rate is honest. The console gains the economics board, a spend entry form,
funnel economics, a channel selector on single sends, and live bounce-rate
health in the verdict line. 7 domain + 2 server + 2 pg + 13 adapter tests,
all suites green.

## Phase 41 — Email list hygiene: bounce suppression + engagement sunsetting

Phase 40's guard pauses a tenant whose reputation is burning; Phase 41
removes the fuel. The `email_suppressions` list (migration 0029, FORCE RLS)
permanently holds any address the ESP reports hard-bounced or complained —
the adapter's bounce webhook suppresses it automatically — and an operator
can add or remove addresses by hand. Suppression holds BOTH email lanes: a
dead address delivers nothing, and re-mailing one is the biggest bounce
driver. Separately, the engagement sunset holds marketing email to any
client with 8+ all-time marketing sends and zero recorded opens or clicks —
chronic non-openers are tomorrow's complaints, and one open resets the
sunset forever. Service email and WhatsApp are exempt. Console: an
"Email suppressions" card on Growth, and the verdict line flags suppressed
addresses. See docs/phase41.md.

## Phase 42 — WhatsApp list hygiene: unreachable hold + reply-reset sunset

Phase 41 hardened email; Phase 42 brings the primary lane to parity.
WhatsApp receipts whose wamid resolves to an outreach send now feed the
attribution spine (`wa_failed` / `wa_delivered` / `wa_read`), and inbound
client messages track `client_replied` — WA's only true engagement signal.
The marketing verdict holds a client whose number has 3+ failed deliveries
with none confirmed since (Meta's quality rating punishes re-mailing dead
numbers), and sunsets clients with 8+ marketing WA sends and zero replies
ever — one reply resets it forever. Service lane exempt, email untouched,
both rules advisory. Console verdict line flags unreachable numbers. See
docs/phase42.md.

## Phase 43 — post-sunset re-engagement: win-back touch + reopt/ links

The Phase 41/42 hygiene holds have a door back. A sunset-held client whose
consent is still on may receive one marketing touch per 90 days on the held
channel: eligibility derives from the sends ledger itself (the granted touch
carries a Detail marker; failed dispatches never burn the window, scheduled
touches hold it), the due-send sweep can deliver its own touch, and dead
endpoints (suppressed addresses, unreachable numbers) are never exempt.
Verdicts expose `reengagement_touch`. For clients whose consent is off, a
new `reopt/` HMAC token family (mirroring `unsub/`) powers one-tap
re-subscribe links, embedded as a passive footer in service-lane emails
only — the marketing lane stays closed until the client opens it. The flip
emits `client.reopted` and tracks `reengagement_optin` for funnel proof.
Console verdict line flags win-back touches. See docs/phase43.md.

## Phase 44 — consent evidence ledger (GDPR Art. 7 demonstrability)

Every consent flip across the platform — registration defaults, console,
STOP/START keywords, unsub/ links, reopt/ links, lead capture, repeat
touches, ESP complaints, DSAR erasure — now writes an append-only
`consent_events` row (scope, granted, source, detail, when) into a new
RLS-forced table. GDPR Art. 7 says the controller shall be able to
*demonstrate* consent; the `consent` map alone only shows the current
state. The ledger is change-only (replays add no rows, so retries stay
clean without a uniqueness key), registration writes one genesis row per
scope so the starting state is provable for the jurisdiction, and
`GET /v1/clients/{id}/consent-history` + the export's new
`consent_history` field put the trail on the record. Erasure writes
pseudonymous revocation rows that survive deletion. The console 360 view
renders the timeline. See docs/phase44.md.

## Phase 45 — client self-service consent center (GDPR Art. 7(3))

Phase 44 proves consent to a regulator; Phase 45 hands the same ledger to
the client. A `consent/` HMAC token (same discipline as `unsub/`/`reopt/`,
cross-family tokens rejected) opens a public page showing every scope with
its current state and evidence timeline — one-tap toggles, no JavaScript,
works from any email footer. Every flip lands in the ledger with source
`self_service`. The `service` scope is shown locked (booking updates are
the contract, not a preference); unknown scopes are refused; erased
clients get a read-only tombstone view over their surviving evidence.
A downloadable JSON consent receipt (controller, pseudonymous subject,
scopes, full trail) rides on the page, marketing email footers now carry
"Manage your preferences" next to unsubscribe, and the console can mint
the link per client. No migration. See docs/phase45.md.

## Phase 46 — data retention & minimization engine (GDPR Art. 5(1)(e))

Storage limitation: the platform no longer keeps everything forever. A
per-tenant `retention_config` (migration 0032) drives an hourly sweep that
permanently purges geo stamps after 90 days (location breadcrumbs are the
most sensitive data we hold), scrubs message bodies in CLOSED
conversations to a provenance marker after 730 days (row metadata survives
so threads and counts stay honest), and purges attribution events after
730 days — deliberately ending their contribution to engagement hygiene.
Financial records, the consent evidence ledger and the audit log are
exempt: accounting retention and accountability outrank minimization. Per
category, 0 = platform default and a negative value = never purge; a
tenant-level disabled flag opts out. `POST /v1/retention/run` fires an
idempotent sweep on demand from the console Settings card, every sweep
writes a `retention.sweep` audit entry, and GDPR erasure (Phase 44) now
scrubs the client's conversation content immediately — including open
conversations the age sweep leaves alone. See docs/phase46.md.

**Phase 47 — KYC / identity verification lane.** The long-dormant
`Client.KYCStatus` field is now real: a verification case workflow
(PENDING → IN_REVIEW → APPROVED/REJECTED/EXPIRED, one open case per
client+level, idempotent open, absorbing terminal states) with an operator
queue (`/kyc` console page + `/v1/kyc/cases` endpoints). Approvals advance
the client status and never regress it; stale undecided cases expire
hourly (default 30 days). `Tenant.KycConfig` thresholds gate
high-value quote confirms — €10k default for STANDARD, €100k for
ENHANCED — fail-closed, with an automatic operator work item when the
gate fires. Data minimization is structural: the platform stores document
metadata only (type + operator-side reference), never the document;
erasure scrubs those references immediately while the case row survives
as the AML accountability record. The only provider is operator review
("manual") — external KYC vendors attach behind the same case shape when
contracted. See docs/phase47.md.

**Phase 48 — Mojaloop settlement / payout depth.** Four production gaps
closed in the payout lane: adapter callbacks are now authenticated
(`X-Callback-Secret`, constant-time compare, `CE_PAYOUT_CALLBACK_SECRET`
on both core and adapter); quote expiry is stored and enforced — a stale
FX lock is a 409, not a settlement; core-side timeout sweeps (hourly +
`POST /v1/payouts/sweep`) compensate IN_FLIGHT payouts and batches older
than 48h so dropped callbacks never strand partner funds; and settlement
batching nets a partner's same-currency pending payouts into ONE scheme
transfer — one scheme fee instead of N. Member lines settle pro-rata with
the SAME ledger idempotency keys as the single flow, and never carry the
batch transfer id, so callback routing (payout saga first, batch saga on
miss) is unambiguous. `GET /v1/partners/{id}/settlement-statement` is the
per-currency reconciliation surface; the console Partners page gains a
Settlements card. See docs/phase48.md.
