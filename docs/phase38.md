# Phase 38 — Lead provenance: lawful contact basis for acquired lists

Phase 37 gave greenfield tenants the import engine. Phase 38 answers the
next question honestly: *where did this list come from, and why may we
contact these people?* The loop the platform closes is unchanged —
**acquire early → prioritize by score → nurture automatically → convert to
durable accounts → attribute everything → fund the next event from
evidence** — but "acquire" now includes bought and partner-sourced data
without pretending it is organic.

## The problem

GDPR (and the platform's own consent gates) make purchased consumer lists
nearly unusable for marketing: the outreach engine is consent-gated, EU
numbers default to opt-in, and marketing sends to non-consented contacts
are held, never forced. The legitimate acquisition data classes are:

- **B2B contact data** (Cognism, Apollo, ZoomInfo, Lusha, Dealfront) —
  corporate travel buyers, EAs/PAs, event planners; human first touch
  under legitimate interest.
- **Partner-consented introductions** (hotels, hospitality resellers,
  DMCs) — the partner's consent travels with the introduction.
- **Audiences, not PII** (ad-platform intent/interest/geofenced segments)
  — clicks land on capture pages; the lead self-reports with consent.

## What changed

**Model.** `Lead.ListSource` (which vendor/partner the row came from,
e.g. `cognism-q1-2027`, `apex-hotels-csv`) and `Lead.ContactBasis`:
`self_reported` | `partner_consent` | `b2b_legitimate_interest`.

**Capture.** Basis validated against the known values (422 otherwise);
empty basis defaults to `self_reported` for organic inbound. Marketing
consent is **refused** (422) on a `b2b_legitimate_interest` basis
regardless of what the payload claims. The public site surface asserts
`self_reported` itself — the caller's payload cannot claim otherwise.

**Import.** Rows with no basis default to `b2b_legitimate_interest` — a
bought list must declare a better basis, not silently look organic.
`consent_marketing: true` survives only on `self_reported` /
`partner_consent` rows; otherwise it is stripped and reported in the
receipt's new `notices` array (the row still imports — it is worked by
human outreach, not rejected).

**Consent earned on the repeat touch.** This is the money moment for
bought data: human outreach → the prospect replies "yes, send me offers"
→ the operator re-captures the same contact with consent → dedup absorbs
it into the existing lead, the grant lands on the messaging identity
(grant-only), the score re-computes upward, and provenance (basis +
list source) is preserved on the lead. A B2B row can show
`consent_marketing: true` *after* the reply — the flag records the
current grant, the basis records where the row came from.

**Segments.** `SegmentRules.ContactBasis` joins through open leads like
`LeadKind` (AND semantics): "call list of B2B-sourced corporate leads" or
"exclude bought data from this blast" are declarative rules, exposed on
the Growth broadcast composer.

**Console.** Leads board/table carry basis + list-source chips ("b2b
sourced · from cognism-q1-2027 · human outreach only"); the import card
documents the governance and renders downgrade notices; Growth broadcast
gains a contact-basis selector.

**Migration 0026.** `list_source` / `contact_basis` columns (pre-existing
rows keep `''` = unknown), CHECK constraint, `idx_leads_basis` index.

## API

`CaptureLeadInput` gains `list_source` and `contact_basis` on both
`POST /v1/leads` and `POST /v1/leads/import` (and the public
`/site/{slug}/leads`, where the surface overrides it to `self_reported`).
The import receipt gains `notices: [{index, name, reason}]`.
`POST /v1/outreach/segments/preview` and broadcast/sequence `segment`
accept `contact_basis`.

## Verification

- 4 domain tests: import governance (default/strip/keep/reject + client
  consent mirror), capture defaults and 422s, repeat-touch consent
  earning with provenance preserved, basis segments (+ kind composition +
  conversion leaving the segment).
- 3 server tests: import receipt notices + board provenance, capture
  basis 422s, public site asserting self_reported.
- 1 pg test: column round-trip, CHECK refusal, index presence.
