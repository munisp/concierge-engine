# Phase 22 — Scheduling Windows, Conflict Gating, Conversation Threading

Audit-driven hardening wave answering three questions: how robust is the
scheduling engine, how seamless is web/WhatsApp correspondence, and how
robust is the correspondence engine. Pre-fix scores: scheduling ~45/100
(dispatch existed, scheduling did not — assignments carried no time windows
and zero conflict detection), correspondence ~80/100 (deep WhatsApp
coverage, but every message opened a **new** conversation via a per-message
external_ref, fragmenting agent context, the operator timeline, and the
funnel), cross-channel ~55/100 (WhatsApp-only, no web channel, no unified
thread view). This phase closes all three.

## Conversation threading fix (`domain/conversations.go`)

`ConversationStore.FindActive(tenant, client, channel)` returns the newest
non-CLOSED thread; `Open` is now get-or-create — an inbound message for a
client with a live thread on that channel appends to it instead of forking
a new conversation. `conversation.started` and acquisition attribution fire
only on a genuinely new thread, so the funnel counts conversations, not
messages (the per-message double-counting is gone). Client 360 inherits the
fix automatically — the timeline is now one thread per channel per client.

## Scheduling v1 (`domain/dispatch.go`)

- **Service windows**: `Assignment.StartAt`/`EndAt` (migration 0017). The
  window is resolved automatically from the booking's request item
  (`pickup_at` + `hours`, falling back to the tenant's
  `dispatch_config.default_duration_minutes`, else 120m). A zero window
  means "schedule unknown" — recorded, but ungated (honest degrade, never a
  fake conflict).
- **Conflict gating**: `Assign` refuses an overlapping window on the same
  fleet resource or crew member with **409 naming the conflicting
  assignment and its window**. Terminal statuses (`COMPLETED_TRIP`,
  `CANCELLED_TRIP`, `NO_SHOW`) free the resource; partner-only assignments
  (no fleet/crew) are ungated by design.
- **Turnaround buffer**: the tenant's `dispatch_config.turnaround_minutes`
  (default 30) pads the **end** of each busy window — a pickup must start
  at least the buffer after the previous drop-off, while a preceding
  assignment may butt right up against the next pickup. End-padding only:
  the buffer is for cleaning/repositioning after a trip, not before it.
- **Availability**: `GET /v1/dispatch/availability?from&to` returns busy
  windows (buffer-padded) and a free/busy verdict per active fleet resource
  over the queried range. Console dispatch board: availability checker card
  + assignment windows on every card.
- **Tenant config**: `dispatch_config` jsonb on tenants, patched via
  `PUT /v1/tenant`, with partial index
  `assignments_active_window_idx` (terminal statuses excluded) to keep
  conflict scans cheap.

## Unified client threads + web channel

`GET /v1/clients/{id}/conversations?limit` lists a client's threads across
channels. The capture page gains a "Message your concierge" card — a web
message box that get-or-creates the client's `WEB` thread, appends the
message, and runs advisory intake, so a web visitor and their WhatsApp
number now land in the same threaded model ("one thread per channel, one
client across channels").

## Verification

- 5 new domain tests: threading get-or-create/CLOSED-forks-new/funnel
  counts threads, client threads listing, window resolution
  (pickup+hours / tenant default / global default), conflict gate
  (15m-after-dropoff refused with 409 naming the conflict, 45m allowed,
  zero window ungated, terminal status frees, partner-only ungated),
  availability (busy padding, inactive resources skipped).
- 3 new pg tests: FindActive (match, wrong channel, RLS cross-tenant,
  CLOSED no-match), assignment window roundtrip incl. zero-window NULL,
  tenant `dispatch_config` roundtrip.
- Full suite: `go build`/`go vet` (both tags), `go test ./...`, whatsapp
  adapter 55 passed/2 skipped, concierge agent 26, analytics 16+1 skipped,
  console `tsc -b && vite build` clean.
