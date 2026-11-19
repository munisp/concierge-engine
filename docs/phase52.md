# Phase 52 — Engagement-driven send-time optimization (Mautic feature-borrow A2)

## Context

The Mautic assessment (`github.com/mautic/mautic`) concluded: do not embed
Mautic (~80% overlap with native outreach; consent source-of-truth conflict;
GPL-3.0; PHP/MySQL stack tax), borrow its three genuine gaps instead:

1. Visual campaign builder — deferred to a later phase (big wave).
2. **Smart send-time optimization from real engagement signals — this phase.**
3. Marketer-editable content blocks — later phase (Studio engine).

## What changed

Phase 35's optimizer (`OutreachConfig.OptimizeSendTime`) inferred a client's
best send hour from the modal `CreatedAt` hour of their **bookings** — a
proxy. The attribution spine already carries the real signal: `email_opened`
/ `email_clicked` (Phase 39/41) and `client_replied` (Phase 42) events with
timestamps.

`OutreachService.engagementHour` (outreach52.go) builds a 24-bucket
histogram over the client's own engagement events and returns the modal
hour's next occurrence (≥1h out, same rule as before). `deferral` now:

1. tries the engagement hour when `OptimizeSendTime` is on
   (minimum `StoMinEngagements = 3` events — below that the signal is noise
   and the engine says so);
2. falls back to the Phase 35 booking-pattern hour;
3. lets the quiet-hours window end override either;
4. returns the provenance string, which lands in the send's audit detail:
   `scheduled for … (smart send time: peak engagement hour from 9
   opens/clicks/replies)` / `(send time from booking pattern (insufficient
   engagement data))` / `(quiet hours until window end)`.

`OutreachVerdict` gains `optimized_send_at` so the console composer previews
the computed delivery time for marketing sends (service sends never defer
and never carry the hint).

## Honesty rules

- Only the client's OWN engagement timestamps are used — never a global
  "best time to email" statistic.
- Below the minimum sample the engine degrades to the weaker signal and
  the detail admits it; with no evidence at all the send goes immediately.
- Non-engagement kinds (`outreach_sent` etc.) and zero timestamps are not
  evidence.

## Tests (6, hermetic, domain package)

engagement-beats-booking; min-sample fallback with honest detail; no
evidence → immediate; quiet-hours override with reason; verdict preview
(marketing yes / service no); `engagementHour` unit (kind filter, zero-At
filter, next-day roll).
