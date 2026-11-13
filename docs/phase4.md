# Phase 4 — Self-hosted AI (Ollama/Qwen) + next-generation case management

User-directed wave beyond the §14 roadmap. Two coupled moves: own the model
runtime, and turn the CRM into an operational case system that uses it.

## Self-hosted inference — Ollama (llama.cpp) serving Qwen

Un-defers the register item *"self-hosted open-weight models"* (trigger:
tenant data-locality mandates + inference-spend economics).

- `ai/concierge-agent/concierge_agent/inference.py`:
  - **OllamaProvider** — OpenAI-compatible chat completions against a local
    Ollama daemon (`CE_OLLAMA_BASE_URL`, default `http://localhost:11434`),
    model `CE_OLLAMA_MODEL` (default `qwen3:8b`). Cached 10s health probe on
    `/api/tags`.
  - **ModelRouter** — per-task routing with an honesty contract: every
    response carries an `engine` field (`ollama/qwen3:8b` | `rules`) so the
    audit trail and evals know exactly what produced it.
  - **PII redaction before inference is unchanged and enforced** — the
    router calls `prepare_for_inference()` before any token leaves; tests
    assert the model never sees raw emails/numbers.
  - **Deterministic floor** — Ollama unreachable or garbage JSON →
    rule-based triage/summary/next-action. CI and dev never need a GPU or a
    pulled model; production gets the real model.
- New agent endpoints: `POST /v1/ai/triage`, `/v1/ai/summarize`,
  `/v1/ai/next-action`, `GET /v1/ai/health`.

Run the model runtime:

```bash
ollama serve &                 # llama.cpp server
ollama pull qwen3:8b           # one-time model fetch
CE_OLLAMA_MODEL=qwen3:8b make run-ai
```

## Case management — on top of, not instead of, the CRM

The CRM (clients + preferences) stays the identity/preference spine. A
**Case** is the accountable unit of concierge work: it binds a client,
their conversations, service requests and bookings into one workflow with
owner, priority, SLA clock, tasks and timeline.

- `domain/cases.go` + `model.Case/CaseTask/CaseNote` + migration
  `0004_cases.sql` (FORCE RLS tenant isolation on all three tables — cases
  carry client PII context, no tenant-less exceptions).
- **Auto-triage on open**: kind (`booking|incident|inquiry|vip_request`),
  priority (P1–P4) and one-line summary from the inbound text via the
  concierge-agent; core-api keeps its own `core-rules` floor so intake never
  depends on any model being alive. Provenance recorded
  (`triage_engine`: `ollama/qwen3:8b` | `rules` | `core-rules`).
- **State machine**: OPEN → TRIAGED → IN_PROGRESS ⇄ WAITING_CLIENT /
  WAITING_PARTNER → RESOLVED → CLOSED (terminal). Assignment implies triage
  acknowledgment; resolve/close stamp their timestamps; illegal transitions
  are conflicts.
- **SLA clocks by priority** (P1=1h, P2=4h, P3=24h, P4=72h) with a sweeper
  (`POST /v1/cases/sla-scan`) that flags overdue cases, emits
  `case.sla_breach`, and never double-escalates.
- **AI assist on demand**: case summary and next-action endpoints, each
  reporting the engine that produced them.
- **Metrics**: open/resolved counts, SLA breaches, avg resolution hours,
  AI-triage share (the autonomy-evidence input for raising the dial).
- Console: **Cases** page — board with state filter, SLA-overdue marking,
  detail view with tasks/timeline/AI summary/next-action.

## API summary

| Route | Purpose |
|---|---|
| `POST /v1/cases` · `GET /v1/cases?state=&kind=` | open (AI-triaged, idempotent) · list |
| `GET /v1/cases/{id}` | detail |
| `POST /v1/cases/{id}/assign` · `/transition` · `/link` | owner, state machine, CRM spine links |
| `POST/GET /v1/cases/{id}/tasks` · `POST .../tasks/{tid}/complete` | checklist (idempotent complete) |
| `POST/GET /v1/cases/{id}/notes` | timeline |
| `GET /v1/cases/{id}/summary` · `/next-action` | AI assist with engine provenance |
| `POST /v1/cases/sla-scan` · `GET /v1/cases/metrics` | SLA sweeper · KPIs |

## Tests

- Python (`tests/test_inference.py`): fake Ollama daemon (stdlib
  http.server) — request shape, JSON extraction, **redaction-before-
  inference asserted against the recorded request body**, rule fallback on
  daemon-down and on garbage JSON, endpoint contract stability.
- Go (`internal/domain/phase4_test.go`): full lifecycle incl. illegal
  transitions + terminal CLOSED, idempotent open/task-complete, SLA scan
  escalation without doubles, assist wiring against a fake agent
  (provenance flows through), assist-down fallback to `core-rules`,
  metrics, cross-tenant isolation.

## Phase 4b — SLA sweeper, per-tenant SLAs, semantic recall

Follow-up wave on the case system:

- **Background SLA sweeper**: `cmd/core-api` runs a 1-minute ticker that
  calls `CaseService.ScanSLA` for every tenant — breaches surface within a
  minute without an operator hitting `POST /v1/cases/sla-scan`. The manual
  endpoint stays for on-demand checks and tests.
- **Per-tenant SLA overrides**: `SLAConfig.CaseSLAHours` (`{"P1": 1, ...}`)
  — a tenant's map overrides the defaults per priority; anything unset
  falls through to P1=1h/P2=4h/P3=24h/P4=72h. UAE hosts can run tighter
  VIP clocks without forking code.
- **Semantic similar-case recall**: on open, the case title+summary is
  embedded via the agent's new `POST /v1/ai/embed` (Ollama
  `CE_OLLAMA_EMBED_MODEL`, default `nomic-embed-text`) and stored in
  `case_embeddings` (migration `0005_case_embeddings.sql`, FORCE RLS,
  jsonb vector). `GET /v1/cases/{id}/similar?limit=5` cosine-ranks past
  cases in the tenant — grounded institutional memory for triage, and the
  console shows them under "Similar past cases". Embedding failure is
  advisory: the case still opens, similarity just degrades gracefully.
  pgvector stays the scale-out path (un-defer trigger: tenant crosses
  ~50k cases or recall latency hurts).

- **Real-model smoke evidence**: `docs/eval-qwen3-smoke.md` — full eval
  battery against a live Ollama daemon (`qwen3:0.6b`, sandbox-sized):
  triage/summarize/next-action/embed with engine provenance on every
  response, redaction confirmed against the real model, embedding
  cosine-sanity table. Found and fixed one contract gap (summarize now
  carries its engine label end-to-end).

## Honesty notes

- `qwen3:8b` is the default, not the only option — any Ollama-served model
  works via `CE_OLLAMA_MODEL`.
- Local Qwen drafts and triages; money and state transitions stay in
  deterministic Go code. The autonomy dial contract is untouched: AI output
  is advisory until eval evidence says otherwise.
