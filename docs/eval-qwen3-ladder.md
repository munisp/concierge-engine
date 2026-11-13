# Eval ladder — qwen3:0.6b vs qwen3:4b on real hardware (2026-11-14)

Same triage battery as `eval-qwen3-smoke.md`, re-run on `qwen3:4b`
(2.5 GB Q4) in the build sandbox (2 cores, 3.9 GB RAM, CPU-only).
All calls through the production concierge-agent endpoints, no doubles.

## Triage battery comparison (expected labels are evaluators', not ground truth)

| Input (truncated) | 0.6b result | 4b result | Verdict |
|---|---|---|---|
| Driver no-show LAX, missed flight, refund NOW | vip_request/P2 (4.1s) | booking/P2 (109.5s) | both miss `incident`; priorities defensible |
| Safety threat against principal, legal counsel | incident/P1 (3.4s) | **incident/P1, clean summary** (after one timeout+fallback pass) | both correct |
| Helicopter Nice→Monaco next Friday | rules→vip_request/P2 | rules→vip_request/P2 (daemon 500 under RAM pressure) | floor held |
| Cancellation policy question | vip_request/P2 (miss) | **inquiry/P3 — correct** (109.7s) | 4b wins |
| Book Maybach tonight 8pm, phone number | booking/P2 (4.4s) | booking/P2 (144.1s) | both correct, number redacted both |
| Rude chauffeur, smelly car | incident/P2 (3.8s) | booking/P3 (4b) / incident via rules | **0.6b + rules beat 4b** |

## What the ladder actually showed

1. **Quality gain is real but modest at 4b**: the policy question fixed
   (inquiry, correct priority) and the safety case got a noticeably better
   summary ("Safety threat against VIP principal requires immediate legal
   counsel"). But 4b still mis-kinds service-recovery complaints as
   bookings — the evaluators' top confusion at every size so far.
2. **Resource floor beats model size**: on 2-core/3.9GB the 4b run produced
   three fallbacks (one 150s timeout, two daemon 500s under memory
   pressure) and the box OOM-recycled mid-session. The deterministic rule
   floor caught the most safety-critical case (P1 incident) when the model
   couldn't answer — the architecture working exactly as designed.
3. **Latency**: 0.6b = 3.5–6.5s/call; 4b = 30–145s/call on this hardware.
   Async triage-on-open tolerates this; interactive drafting does not.
4. **Thinking suppression**: qwen3's thinking traces blew past the token
   budget until `think: false` was added to chat requests (plus the new
   `CE_OLLAMA_TIMEOUT` env knob — 30s default is too tight for CPU hosts).

## Actions taken from this eval

- **Complaint-kind override — implemented**: when core rules detect a
  safety/legal or service-recovery signal (incident at P1/P2), their kind
  wins and priority can only escalate; provenance stays honest
  (`engine` keeps `ollama/<model>`, the reason carries `[rules-override]`).
  Covered by `test_triage_rules_override_complaint_miskind`.
- **`CE_OLLAMA_TIMEOUT` env knob — implemented** (default 30s; CPU-only
  hosts should set >= 120s), and `think: false` on chat requests so qwen3
  thinking traces can't burn the token budget on structured tasks.
- Production sizing: `qwen3:8b` on a GPU host (or 4+ core CPU with 8 GB+)
  remains the target; re-run this battery there before touching the
  autonomy dial. The sandbox cannot host 8b — documented, not hidden.
- Keep `CE_OLLAMA_TIMEOUT >= 120` on CPU-only hosts.

## Honesty notes

- The 4b battery completed triage only; summarize/next-action/embed had
  been validated live on 0.6b (see smoke doc) and the 4b rerun was cut
  short when the sandbox OOM-recycled. Results above are exactly what was
  observed — no extrapolation.
