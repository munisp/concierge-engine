# Phase 5 — case intake loop, gold-layer ML, real-model eval ladder

User-directed wave: wire the WhatsApp channel into the case system,
replace the gold-layer heuristics with fitted probabilistic models, and
push the real-model eval up the size ladder as far as the sandbox allows.

## (a) WhatsApp → case intake loop

Every inbound WhatsApp message (BSP webhook or Meta Cloud API direct) now
flows: normalize → resolve client → conversation → message → agent turn →
**case intake**.

- New endpoint `POST /v1/cases/intake` (`domain/cases.go: Intake`) carries
  the policy in the domain layer, not the adapter: triage once, open a
  case only when the message warrants accountable follow-up — **incidents
  and VIP requests always; anything at P1/P2; plain inquiries and routine
  P3/P4 bookings stay in the conversation flow** and return
  `{"opened": false, ...triage evidence...}`.
- **Idempotent**: the adapter sends `wa:<whatsapp-message-id>`; with no
  external id it derives a stable `wa:auto:<sha256>` from
  tenant+contact+text. Webhook redelivery replays report
  `opened: true, reason: "duplicate delivery"` without creating a second
  case (`TestCaseIntakeIdempotentReplay` asserts metrics stay at 1).
- Opened cases link back to the conversation (CRM spine) automatically.
- **Advisory**: intake failure never breaks the message flow — the agent
  reply still goes out (`test_intake_failure_does_not_break_message_flow`).

## (b) Gold layer: fitted models replace flat heuristics

`analytics/lakehouse/gold.py` v1 — still stdlib-only and deterministic
(closed-form estimates, no RNG), still labeled per row:

- **LTV** — smoothed empirical retention curve (Kaplan-Meier style):
  per-tenant continuation probabilities r_k = P(reach booking k+1 |
  reached k), Laplace-smoothed; thin tenants shrink toward the platform
  curve; no repeat data anywhere falls back to the flat 0.25 prior.
  Expected future bookings = chained continuation over a 4-booking
  horizon. Unlike v0's flat repeat_prob, this differentiates a client at
  booking 4 from a one-timer by the shape of the tenant's actual
  retention curve (`test_client_ltv_retention_curve_rewards_loyalty`).
- **Churn** — empirical-hazard survival model: pool the tenant's observed
  inter-booking gaps, score each client's cadence-adjusted silence against
  the survival curve (fast bookers score risky sooner). Tenants with < 8
  pooled gaps fall back to the v0 recency rule, labeled.
- **Pricing** — statistical process control on the tenant's own daily
  instant-share series (trailing 7-day mean vs baseline mean ± max(1σ,
  5pt)) instead of fixed 0.8/0.2 cutoffs; thin history falls back to the
  absolute floors. Supply-side partner-fill check unchanged. Still ±5%
  bounded and never self-executing.

Row schema keeps `rule` semantics via a `model` field
(`km-retention/v1`, `empirical-hazard/v1`, `spc/v1: ...`, or the labeled
fallback) so the audit trail always knows what produced a number.

## (c) Real-model eval ladder

`docs/eval-qwen3-smoke.md` covered `qwen3:0.6b`. This wave re-runs the
same battery on the largest Qwen3 that fits the build sandbox and records
the comparison — see `docs/eval-qwen3-ladder.md`. The production default
remains `qwen3:8b`; the sandbox (2 cores / 3.9 GB RAM, CPU-only) cannot
load it, which is documented in the eval rather than hidden.
