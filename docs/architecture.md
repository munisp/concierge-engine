# Architecture notes

## Topology (Master Spec §9)

```
edge (CDN, openappsec, APISIX: OIDC, tenant resolution, rate limits)
  -> services (Go core-api modular monolith; Rust policy-engine + ledger-adapter;
               Python concierge-agent + whatsapp-adapter)
  -> data (Postgres RLS, Redis, object storage, OpenSearch)
  -> events (Kafka outbox backbone; Temporal for sagas)
```

## Key decisions

1. **Modular monolith first** — `services/core-api` hosts tenancy, catalog,
   booking & quote engine, dispatch, payments and conversations in one binary
   with package-level boundaries. Extraction order on evidence: payments,
   conversations, dispatch real-time.
2. **Outbox from day one** — every mutation emits a domain event
   (`outbox` table -> forwarder -> Kafka). Audit, webhooks, analytics and AI
   training data fall out for free.
3. **Money truth in TigerBeetle** — Postgres holds business state; the Rust
   `ledger-adapter` posts idempotent double-entry transfers. Split settlement
   (platform fee / tenant net / partner payable) is computed from the booking
   ledger at capture time. Mojaloop interop is Phase 2+; the adapter interface
   is already PSP/Mojaloop-agnostic.
4. **The LLM never has the final word** — the Rust `policy-engine` classifies
   every proposed action TIER-0..3; TIER-3 (refunds, waivers, charge execution)
   is human-only. Outbound replies pass the 5-stage pipeline: schema -> policy
   -> tone -> factuality (numeric claims verbatim from tool outputs) -> privacy.
5. **WhatsApp is an adapter** — `whatsapp-adapter` implements the channel port;
   BSP (Infobip/Twilio/Meta-direct) is tenant-level config.

## Language strategy (normative, Master Spec §10)

- **Go** — domain services, APISIX plugins, high-concurrency API backends.
- **Rust** — policy engine, ledger adapter, conversation-ingestion parser.
- **Python** — AI orchestration, channel adapters, eval harness.
- **TypeScript** — web surfaces (capture, concierge console, dispatch, admin).
- Shared contracts in `contracts/` (protobuf + JSON schema).

## Real-time dispatch

Phase 1 uses SSE from `core-api` (`GET /v1/dispatch/stream`) for the dispatch
board feed; WebSockets terminate at the edge when the board needs bidirectional
high-frequency updates (see deferred register: Fluvio telemetry streams).
