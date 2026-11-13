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

Phase 1 (MVP, months 0–6) scope per `concierge-engine-phase1-plan.xlsx`: tenant core +
admin, white-label web + WhatsApp capture, quote & booking engine (ground vertical),
dispatch board + chauffeur app v1, Stripe payments on TigerBeetle, SEO foundation,
AI concierge v0–v1, K8s/Dapr/APISIX/Keycloak/Kafka/Temporal foundations.
Deferred components and un-defer triggers are tracked in `docs/deferred-register.md`.
