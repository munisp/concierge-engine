# Phase 64 — Channel Engagement Closure

Phase 63 built the demand-channel spine (sourcing, commission, margin
analytics). Phase 64 closes the two engagement gaps the channel model
exposed:

1. **Sourced confirmations were silent.** A guest whose booking a hotel
   desk arranged received no confirmation message, and the arranger
   heard nothing back — the channel that sourced the demand had no
   proof the booking landed.
2. **Off-channel demand was misattributed forever.** When a partner's
   guest phones or emails the operator directly, the request lands as
   `direct` — the partner loses attribution and commission, and the
   margin report overstates the direct channel. Operators had no way
   to correct it.

## 1. Sourced-confirm notifications

`ChannelService.NotifySourcedConfirmed` fires when a sourced booking is
confirmed (wired into `BookingService.Confirm` via a detached,
recover-guarded goroutine — channel latency never holds the API
response, and direct bookings skip entirely).

- **Guest** receives a service-lane message (consent-exempt, idempotent
  on `src-confirm:{booking}`) naming the arranger: *"Your booking with
  us is confirmed — arranged for you by Hotel Eko."* Channel by
  contact: WhatsApp when a phone exists (default lane), email with an
  explicit subject when the guest is email-only.
- **Sourcing partner** receives a WhatsApp notice with the booking
  reference and the partner-portal URL, posted through the configured
  adapter. No partner phone or no adapter configured is an audited
  skip — never a fake "sent".

Every send is recorded in the outreach ledger with its verbatim status
(`SENT` / `FAILED` / skip reason) and audit-logged.

## 2. Operator reattribution

`POST /v1/requests/{id}/source` `{ "partner_id": "…" }` lets an
operator move an email/phone-arrived request to the partner who really
sourced it — or clear it back to `direct` with `""`.

- Idempotent: re-applying the current attribution is a no-op success.
- The partner must exist and not be SUSPENDED (404 / 403).
- Every confirmed booking under the request follows the reattribution
  (`bookings.SetSource`), so the margin report re-buckets the revenue.
- If the booking is CONFIRMED and no sourcing payout exists yet, the
  sourcing commission accrues at reattribution — the partner is made
  whole. **Clearing to direct never rewrites accrued payouts**;
  compensation, when owed, is the Phase 56 clawback flow, not a silent
  edit.
- Emits `channel.source_attributed` and an audit entry either way.

The console 360 page shows a channel badge per request row with an
inline partner dropdown for the correction; the partner list loads
lazily on first focus.

## 3. Files

- `internal/domain/channels64.go` — notifications + reattribution
- `internal/domain/booking.go` — detached sourced-confirm hook
- `internal/domain/eventcatalog.go` — `channel.source_attributed`
- `internal/store/{store.go,memory22.go,postgres.go,postgres24.go}` —
  `SetSource`, request `Update` persists source columns
- `internal/server/phase64.go` — the reattribution endpoint
- Console `Clients.tsx` — source badge + reattribute dropdown
- Tests: `domain/phase64_test.go`, `server/phase64_test.go`,
  `store/pg_integration_phase64_test.go`
