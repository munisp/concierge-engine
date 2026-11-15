# Phase 44 — Consent evidence ledger

GDPR Art. 7(1): *"the controller shall be able to demonstrate that the data
subject has consented."* Until this phase the platform held the **state** of
consent (the `client.Consent` map) but not the **proof** — when a scope was
granted or revoked, via which surface, and with what context. Phase 44 adds
the ledger.

## What shipped

**`consent_events` (migration 0031)** — append-only evidence rows:
`(tenant, client, scope, granted, source, detail, created_at)`. FORCE RLS.
No update/delete path anywhere in the codebase — evidence is immutable.

**Change-only recording.** `ClientService.SetConsentSourced` records a row
only when the value actually flips (or first appears). A replayed consent
PUT with the same value adds nothing — idempotency lives in the domain, so
retries never pollute the ledger.

**Closed source vocabulary.** `source` is validated against nine known
surfaces; anything else is a 422. The ledger stays queryable (`WHERE
source = 'keyword'`) instead of accumulating free-text drift:

| source | surface |
|---|---|
| `registration_default` | ResolveV2 jurisdiction defaults at client creation |
| `console` | operator toggle / bare API write (the default) |
| `keyword` | client texted STOP / START |
| `unsub` | `unsub/` HMAC link |
| `reopt` | `reopt/` HMAC link |
| `lead_capture` | consent granted on first lead capture |
| `lead_repeat_touch` | consent earned on a repeat touch |
| `esp_complaint` | ESP complaint webhook via the email adapter |
| `dsar_erasure` | revoked by the erasure cascade |

**Genesis rows.** Every client created through ResolveV2 starts its ledger
with the jurisdiction defaults it was born with — evidence that the
*starting* state was lawful for the client's region, which is half the
Art. 7 answer.

**Keyword evidence is verbatim.** A STOP row records `client texted
'stop'` in the detail — the strongest possible provenance.

**Erasure is wrapped, not weakened.** `EraseWithEvidence` snapshots the
consent map before tombstoning and writes a `dsar_erasure` revocation row
per granted scope. The rows survive erasure deliberately: they carry only
the pseudonymous `client_id` plus scope/granted/source — no personal data —
and they are exactly what a regulator asks for when a data subject claims
they never consented.

**DSAR export gains `consent_history`.** `ExportWithEvidence` wraps the
Phase 16 export (dsar.go untouched) and attaches the full ledger — the
Art. 15 access report now demonstrates consent instead of just asserting
the current map.

**Read surface.** `GET /v1/clients/{id}/consent-history` (tenant-scoped,
newest-first). The console Clients 360 page renders the timeline under the
consent toggles; a toggle click refreshes it.

**Email adapter** tags its complaint-driven consent flip with
`source: esp_complaint` + recipient detail.

## Design decisions

- **No new idempotency key**: the ledger is event-sourced, and change-only
  recording makes replays naturally no-ops.
- **Consent map stays the read path**: verdicts, caps, and gates keep
  reading `client.Consent` (O(1)); the ledger is write-side evidence, never
  on the hot path.
- **Fail-open where evidence is advisory**: registration-default rows and
  keyword rows never block the primary operation; the sourced write path
  (`SetConsentSourced`) is fail-closed because a consent change without its
  evidence row is worse than a retried request.

## Tests

- 5 domain tests (genesis, change-only, source validation, keyword
  verbatim, erasure revocations, export section, unknown client)
- 1 server test (endpoint, tenant isolation, 422 on unknown source, export
  section)
- 1 pg test (RLS round-trip, ordering, limit, tenant + client isolation)
- 13/13 email-adapter tests (complaint payload updated)
