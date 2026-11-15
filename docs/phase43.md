# Phase 43 — post-sunset re-engagement: win-back touch + reopt/ links

Phase 41/42 stop the bleeding: dead addresses, dead numbers, and
never-engaged clients are held out of the marketing lane. But a hold without
a door back loses the merely-quiet forever — a client who simply had a busy
quarter should not be unreachable in perpetuity. Phase 43 adds the two
industry-standard return paths, both auditable, both capped.

## Win-back touch (the 1-per-90-days exemption)

A sunset-held client whose marketing consent is still on may receive **one
marketing touch per 90 days** on the held channel (`ReengagementCooldownDays`).

Design decisions:

- **Eligibility derives from the sends ledger, not a counter.** A granted
  touch carries a `Detail` marker (`re-engagement touch — …`); the cooldown
  scan counts marker sends with status `sent` or `scheduled` on that channel
  within the window. A **failed dispatch never burns the window** (failed
  records don't count), and a **scheduled touch already holds the window**
  (scheduled records do) — concurrent or deferred sends can't double-grant.
- **The sweep can deliver its own touch.** `Verdict` is now an internal
  `verdict(…, excludeSendID)`; `RunDue` passes the send being delivered so
  its own marker doesn't hold it at delivery. `finish()` re-prepends the
  marker across the terminal transition because `UpdateSendResult`
  overwrites `Detail` — the sent record IS the cooldown.
- **Fail closed on store error.** The sunset rules themselves are advisory
  (fail open), but the exemption re-opens a deliberately closed lane — when
  the cooldown cannot be verified, the hold stands.
- **Dead endpoints are never win-back material.** The unreachable-number
  hold (Phase 42) and the suppression list (Phase 41) have no exemption.
- **Consent still governs.** The exemption fires inside the sunset branch,
  before the consent check — a sunset-held client who withdrew consent gets
  nothing (the consent denial stands).
- The verdict exposes `reengagement_touch: true` so the console composer can
  say "this send is the rare touch", and the held reason names the cooldown.

## reopt/ — one-tap re-subscribe

For clients whose consent is OFF (unsubscribed, or never opted in under
opt-in jurisdiction defaults), the marketing lane is closed regardless of
sunset — the only door back is the client opening it themselves.

- **`reopt/` HMAC token family** on `TripService` (mirrors `unsub/`):
  `ReoptTokenFor` / `ParseReoptToken`; other families are rejected — an
  unsubscribe link can never re-subscribe anyone.
- **`GET /v1/reopt/{token}`** — public (tenantctx-exempt like `/v1/unsub/`),
  idempotent: flips `ConsentMarketing` on, emits `client.reopted`, tracks a
  `reengagement_optin` attribution so funnel reporting can prove win-back
  value, and renders a confirmation page. Replay reports
  already-subscribed.
- **Placement: service-lane email footers only.** A service email to a
  client whose marketing consent is off carries a one-line footer with the
  link (immediate sends in `Send`, scheduled sends in `RunDue`).
  Transactional content stays primary; the link is passive. Neutral wording
  by design — an opt-in-jurisdiction client never "opted out". WhatsApp
  service messages get no footer (Meta template constraints make appended
  marketing CTAs unsafe outside the 24h window).

## Interaction with existing rules

- Verdict order unchanged: erasure → email suppression → consent set →
  service early-return → since/cap → email deliverability guard → email
  sunset (now with exemption) → WA unreachable → WA sunset (now with
  exemption) → consent → intro lane → deny. The tenant-wide deliverability
  guard still precedes the sunset — a win-back touch never fires during an
  active bounce-rate incident.
- Sunset resets are untouched: one open/click (email) or one client reply
  (WA) clears the sunset entirely — the exemption never fires again because
  the sunset itself is gone.
- Phase 41/42 test invariants moved: the first verdict after sunset now
  grants the touch; the hold asserts on the *next* verdict within the
  cooldown.

## Files

- `services/core-api/internal/domain/reengage43.go` — cooldown const +
  marker, eligibility, reopt token family, `ReoptInByToken`, footer helper.
- `services/core-api/internal/domain/outreach.go` — verdict split +
  exemptions + `ReengagementTouch` flag; Send marker detail + service-email
  reopt footer; RunDue excludeSendID + marker preservation + scheduled
  service footer.
- `services/core-api/internal/server/phase43.go` — `GET /v1/reopt/{token}`.
- `services/core-api/internal/tenantctx/tenantctx.go` — `/v1/reopt/` exempt.
- `web/console/src/pages/Growth.tsx` — verdict chip.
- Tests: `domain/phase43_test.go` (cooldown lifecycle, unreachable not
  exempt, token family, footer matrix), `server/phase43_test.go` (endpoint
  incl. tampered/cross-family tokens, verdict flag over HTTP); Phase 41/42
  sunset tests updated to the new invariant.

No migration — the sends ledger and attribution spine already carry
everything the cooldown and the opt-in trail need.
