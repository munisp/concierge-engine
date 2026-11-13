# Deferred-component register (Phase 1)

Source of truth: `concierge-engine-phase1-plan.xlsx` (Deferred Register sheet).
No Phase-1 regret: each item has an explicit un-defer trigger.

| Component | Why deferrable | Pick up | Un-defer trigger |
|---|---|---|---|
| Fluvio telemetry streams | Kafka carries Phase-1 volume | Phase 2 | >500 GPS pings/sec fleet-wide, or Kafka consumer-lag pain, or first multi-city dispatch board needing sub-second updates |
| Spark / Sedona / Ray / DataFusion | Phase 1 needs bronze landing + dashboards only | Phase 2–3 | First spatial-SQL product decision; rule of thumb: >=3 tenants or >=50k trips |
| Mojaloop interop | No instant-scheme corridors in Phase-1 markets | Phase 2 | Instant-payment tenant market with PSP settlement >1.5% or >T+2; payout volume economics; bank partnership requiring FSPIOP |
| OpenCTI threat-intel | Wazuh baseline covers Phase 1 | Phase 2 | Credible targeted-attack signals or government-tenant questionnaire |
| Self-hosted open-weight models | Hosted frontier for quality at launch | Phase 2–3 | Inference spend > $5–10k/mo steady, or tenant data-locality mandate |
| Partner-network routing, corporate accounts, Avinode, GDS | Vertical depth before breadth | Phase 2–3 | Taylor fleet >70% utilization; >=3 EA bookers; first blocked aviation/hotel itinerary |
