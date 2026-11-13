# Deferred-component register

Source of truth: `concierge-engine-phase1-plan.xlsx` (Deferred Register sheet),
updated at Phase 2 delivery (see `docs/phase2.md`).
No Phase-1 regret: each item has an explicit un-defer trigger.

| Component | Why deferrable | Pick up | Un-defer trigger | Status |
|---|---|---|---|---|
| Fluvio telemetry streams | Kafka carries Phase-1 volume | Phase 2–3 | >500 GPS pings/sec fleet-wide, or Kafka consumer-lag pain, or first multi-city dispatch board needing sub-second updates | DEFERRED |
| Spark / Sedona / Ray / DataFusion | Bronze landing + dashboards suffice | Phase 2–3 | First spatial-SQL product decision; rule of thumb: >=3 tenants or >=50k trips | PARTIAL — bronze/silver batch live in `analytics/lakehouse`; engines stay deferred |
| Mojaloop interop | No instant-scheme corridors in Phase-1 markets | Phase 2 | Instant-payment tenant market with PSP settlement >1.5% or >T+2; payout volume economics; bank partnership requiring FSPIOP | **PICKED UP (Phase 2)** — `crates/mojaloop-adapter` + payout saga |
| OpenCTI threat-intel | Wazuh baseline covers Phase 1 | Phase 2–3 | Credible targeted-attack signals or government-tenant questionnaire | DEFERRED |
| Self-hosted open-weight models | Hosted frontier for quality at launch | Phase 2–3 | Inference spend > $5–10k/mo steady, or tenant data-locality mandate | **PICKED UP (Phase 4)** — Ollama (llama.cpp) + Qwen via `inference.py` ModelRouter; PII redaction before inference; deterministic rule floor |
| Partner-network routing, corporate accounts, Avinode, GDS | Vertical depth before breadth | Phase 2–3 | Taylor fleet >70% utilization; >=3 EA bookers; first blocked aviation/hotel itinerary | **PICKED UP** — partners + corporate + Avinode port (Phase 2); GDS via Amadeus NDC seed (Phase 3, `domain/gds.go`) |

| Temporal saga binding | In-process domain orchestration covers Phase 1–2 volume | Phase 2 hardening | First multi-step money saga needing durable retries/compensation (payout saga §11.3) | **PICKED UP (hardening wave)** — `workers/temporal_worker` payout saga: signal + poll backstop, time-skipping tests |
| BSP-only WhatsApp egress | Infobip covers launch volume | Phase 3 | Flagship volume where BSP markup > direct Meta cost | PARTIAL — `meta.py` direct Cloud API adapter + signed webhook ingress live; Infobip remains default |
| Live PSP charges | Hosted-link capture suffices for pilot | Phase 2 hardening | First production payment | **PICKED UP (hardening wave)** — Stripe Checkout Sessions + signed webhook; sandbox fallback without CE_STRIPE_KEY |
| Public API + webhook marketplace | Internal surfaces sufficed for pilot | Phase 3 | First third-party integrator asking for API access | **PICKED UP (Phase 3)** — `cek_` API keys + signed at-least-once webhooks (`domain/apikeys.go`, `domain/webhooks.go`) |
| Spark (silver→gold at scale) | stdlib batch covers Phase-3 volume | Phase 4 | >=3 tenants or >=50k trips (unchanged trigger) | PARTIAL — gold layer live in `analytics/lakehouse/gold.py`; v1 replaced heuristics with fitted models (retention curve, empirical hazard, SPC); engines stay deferred |
| pgvector for case embeddings | jsonb + in-process cosine covers Phase-4 volume | Phase 5+ | Any tenant crosses ~50k cases, or similar-case recall latency becomes user-visible | DEFERRED — embeddings live in `case_embeddings` (jsonb); swap `pgEmbeddings` for pgvector ANN on trigger |
