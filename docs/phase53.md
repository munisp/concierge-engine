# Phase 53 — Visual Campaign Builder (Mautic feature-borrow A1)

## Context

The Mautic assessment (see the Phase 52 notes in docs/phase52.md) ended
with a feature-borrow list of three gaps; the visual campaign builder
was the largest. Mautic's builder is a drag-drop canvas over a
campaign engine: triggers, actions (send), **decisions** (opened?
clicked?), and delays. This phase delivers that shape natively, on top
of the Phase 35 drip engine — without importing Mautic's GPL-3.0
PHP/MySQL stack and without a second state system.

## What was built

### Typed sequence nodes (model, no migration)

`SequenceStep` gains, all `omitempty` (old configs are unchanged):

- `label` — builder display name
- `template_id` — Phase 21 template; body still renders server-side and
  the RENDERED text is what is recorded and delivered
- `channel` (`whatsapp` default | `email`) + `subject` (required on
  email steps, enforced at validation AND at send time)
- `require_engagement` + `engagement_timeout_hours` — the decision node

### The engagement decision gate (domain)

Mautic's "opened?" decision, honestly derived:

- A gated step fires only after the PREVIOUS step's send earned real
  engagement — `email_opened` / `email_clicked` / `client_replied`
  attribution events at/after that send (the Phase 52 evidence kinds;
  sends themselves never count).
- While waiting, NOTHING is sent and NOTHING is recorded. The sweep
  re-evaluates hourly; a restored backup loses nothing (zero extra
  state — the Phase 35 constraint holds).
- On timeout (`engagement_timeout_hours > 0`) a TERMINAL halt marker is
  written on the ledger itself: status `held`, Detail prefixed
  `sequence halted: `. Progression checks the marker — downstream steps
  stop permanently for that client. A halt is visible in the ledger and
  on the canvas, never a silent drop.

### CRUD + live progress (API)

- `GET /v1/outreach/sequences` — definitions plus per-node counters
  (sent/scheduled/held/failed/halted/waiting) DERIVED from the same
  ledger the sweep acts on, plus enrolled audience size (segment-aware).
- `PUT /v1/outreach/sequences/{name}` — validated upsert (name-keyed,
  replace semantics; validation runs BEFORE the tenant row is touched).
- `DELETE /v1/outreach/sequences/{name}` — removes the definition.
  Ledger history survives; re-creating the same name RESUMES the
  cadence (the console delete confirmation says so out loud).
- `POST /v1/outreach/sequences/run` — on-demand sweep (builder's
  "run now", same code path as the hourly sweeper).

### Console canvas (`/sequences`, nav: Campaigns)

Vertical node canvas: enrolled-audience start node, per-step cards with
delay connectors, channel/template/gate badges, live ledger counters on
every node, and an inline editor per node (label, delay, channel,
subject, template picker from the tenant's real template store, body,
gate + timeout). Reorder (up/down), insert, delete step, save/discard,
run sweep now.

## Honesty rules

1. Every canvas number is a ledger derivation — there is no counter
   that could drift from what the sweep would do.
2. Waiting on engagement writes nothing; only real engagement opens a
   gate, and the evidence vocabulary is closed (Phase 52 kinds).
3. Timeout halts are recorded and labeled; halted clients are counted
   as halted, not merged into "held" silently (separate counter).
4. Delete never rewrites history; resume-on-recreate is documented in
   the UI at the point of action.

## Tests

Domain (6): gate stalls without engagement (no record, no backlog);
gate fires on real engagement; timeout halt + permanent downstream
block; typed-node passthrough (channel/subject/rendered template);
ledger-derived progress (tallies, waiting, halt visibility, enrolled);
CRUD validation matrix + replace-by-name + delete semantics.
Server (1): full HTTP path — 422 on invalid, upsert/replace, list with
derived counters, run-now sweep, delete 204 → 404 replay.
Console: `tsc -b && vite build` clean.

## Deferred (documented, not hidden)

- Branching ("two paths after a decision") — the gate is
  continue-or-halt; a second path needs a parallel sequence with its
  own segment (Mautic's full tree UI is a bigger wave).
- Drag-drop spatial canvas — the current canvas is a curated vertical
  flow (Puck lesson from Phase 32: curated beats freeform until demand
  proves otherwise).
