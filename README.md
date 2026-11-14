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

Deferred components and un-defer triggers: `docs/deferred-register.md`.
