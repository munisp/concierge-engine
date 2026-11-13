# Eval evidence — real Qwen smoke test (2026-11-14)

Environment: Ollama v0.40.16 (llama.cpp) on the build sandbox (2 cores,
3.9 GB RAM, CPU-only). Models: `qwen3:0.6b` (sized down from the
`qwen3:8b` default to fit the sandbox), `nomic-embed-text` (768-dim).
All calls went through the production `concierge-agent` endpoints —
no test doubles. This is a smoke/contract eval, not a quality benchmark.

## Triage (`POST /v1/ai/triage`, temperature 0)

| Input (truncated) | kind | priority | engine | latency | redacted |
|---|---|---|---|---|---|
| Driver no-show at LAX, missed flight, refund NOW | vip_request | P2 | ollama/qwen3:0.6b | 4.1s | email |
| Safety threat against principal, legal counsel | incident | P1 | ollama/qwen3:0.6b | 3.4s | — |
| Helicopter transfer Nice→Monaco, next Friday | vip_request | P2 | rules (fallback) | 6.1s | — |
| Cancellation policy question | vip_request | P2 | ollama/qwen3:0.6b | 4.4s | — |
| Book Maybach tonight 8pm, phone number | booking | P2 | ollama/qwen3:0.6b | 4.4s | number |
| Rude chauffeur, smelly car complaint | incident | P2 | ollama/qwen3:0.6b | 3.8s | — |

Verdicts:

- **PII redaction confirmed end-to-end**: raw email/phone never reached
  the model; `redacted_kinds` reported per call.
- **Engine provenance on every response** — including the one call that
  fell back to `rules` (model returned non-JSON; router degraded as
  designed). The fallback contract works against the real daemon, not
  just fakes.
- Kind accuracy is imperfect on a 0.6b CPU model (vip_request is
  over-triggered; the refund complaint should be incident). Acceptable
  for advisory triage — priority calls were all defensible and the
  state machine/money paths never touch the model. Revisit with
  `qwen3:8b` on GPU before raising any autonomy dial.

## Summarize / next-action

- Summarize produced structured state/blockers/next-step bullets from a
  5-event VIP timeline, engine `ollama/qwen3:0.6b`, ~5s.
- Next-action on a stranded-VIP TRIAGED case returned a client-contact
  action with one-sentence rationale, engine provenance attached, ~4.6s.
- Found and fixed during this smoke: the summarize endpoint dropped the
  engine label (contract gap). `ModelRouter.summarize` now returns
  `{"summary", "engine"}` like triage/next-action, and core-api's
  `CaseAssistClient.Summarize` propagates the real engine instead of a
  generic `assist` label.

## Embeddings (`POST /v1/ai/embed`, nomic-embed-text)

768-dim vectors, ~0.6s per call. Cosine sanity check:

| | refund no-show | late chauffeur | heli booking | policy question |
|---|---|---|---|---|
| refund no-show | 1.000 | **0.779** | 0.719 | 0.669 |
| late chauffeur | **0.779** | 1.000 | 0.676 | 0.621 |
| heli booking | 0.719 | 0.676 | 1.000 | 0.643 |
| policy question | 0.669 | 0.621 | 0.643 | 1.000 |

The two service-recovery complaints are each other's nearest neighbor;
the billing-policy question is the most distant from both. Ordering is
exactly what similar-case recall needs.

## Latency note

CPU-only, 2 cores: 3.5–6.5s per chat call. Fine for async triage-on-open
and on-demand assist; too slow for synchronous chat drafting. Production
targets a GPU host (or llama.cpp with more cores) for `qwen3:8b`.
