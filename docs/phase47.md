# Phase 47: KYC / Identity Verification Lane

Client.KYCStatus existed from Phase 1 as a write-only field — set to
`none` at registration, never checked, never advanced. Phase 47 makes it
real: a verification case workflow, an operator decision queue, the client
status that advances on approval only, and the booking gate that finally
reads it.

## The case workflow

```
PENDING --submit--> IN_REVIEW --decide--> APPROVED | REJECTED
    |                                        (terminal, absorbing)
    +-- expiry sweep (default 30d) --> EXPIRED
```

- **One open case per (tenant, client, level)** — enforced by a partial
  unique index in pg and the same rule in the memory store. A second open
  at the same level while one is in flight is a 409, not a duplicate row.
- **Idempotent open** — replayed create with the same idempotency key
  returns the original case.
- **Approval advances, never regresses** — an ENHANCED client approving a
  leftover STANDARD case stays ENHANCED; opening a case at or below the
  level the client already holds is refused.
- **Terminal states absorbing** — a decision replay with the same outcome
  is a no-op; a conflicting re-decision is 409.
- **Stale cases expire** — identity documents go stale, so an undecided
  case older than the tenant's `case_expiry_days` (default 30) flips to
  EXPIRED in the hourly sweep (next to the retention sweep) and via
  `POST /v1/kyc/expire` on demand.

## Data minimization by design

The platform never stores identity documents. A case carries only the
metadata: `document_type` (closed vocabulary: passport, national_id,
driving_licence, company_registry, proof_of_address, source_of_wealth) and
`document_ref` — the operator's pointer into the tenant's own secure
document channel. GDPR erasure (Phase 44) scrubs those references
immediately; the case row (level, decision, timestamps, decided_by)
survives as the AML accountability record, in the same exempt class as
financial records.

## Provider honesty

The only provider today is `manual` — operator review. No third-party KYC
vendor (Smile Identity, Onfido) is wired, and nothing in the code pretends
otherwise. When a tenant contracts one, it attaches behind the same case
shape; the provider column is already on the row.

## The booking gate

`Tenant.KycConfig` (migration 0033) carries the thresholds, in booking
total minor units:

| Field | Default | Negative means |
|---|---|---|
| `standard_above_minor` | 1,000,000 (€10k) | never require STANDARD |
| `enhanced_above_minor` | 10,000,000 (€100k) | never require ENHANCED |
| `case_expiry_days` | 30 | never expire cases |
| `disabled` | false | gate skips the tenant entirely |

`BookingService.Confirm` evaluates the gate after quote-expiry: at or
above a threshold, the request's client must hold the matching level or
the confirm fails 409 `kyc_required`. The gate is **fail-closed** — if it
cannot be evaluated, the booking does not confirm, because a compliance
control that degrades open is not a control. When the gate fires it also
auto-opens a PENDING case (advisory, idempotency key
`auto:{client}:{level}`) so the operator queue shows the blocked client
immediately.

`KycConfig` patches through the existing `PUT /v1/tenant` selective-patch
field; thresholds round-trip through pg with every other tenant config.

## Operator surface

- `POST /v1/kyc/cases` — open (client_id, level, idempotency_key)
- `GET /v1/kyc/cases?status=` — queue (newest first, status filter)
- `GET /v1/kyc/cases/{id}` — one case
- `POST /v1/kyc/cases/{id}/submit` — record document metadata → IN_REVIEW
- `POST /v1/kyc/cases/{id}/decide` — approve / reject (reason required)
- `GET /v1/clients/{id}/kyc` — a client's case history
- `POST /v1/kyc/expire` — run the stale-case sweep on demand
- Console `/kyc` page: open-case form, filterable queue, submit + decide
  actions inline.

Events: `kyc.case_opened`, `kyc.case_submitted`, `kyc.decided`,
`kyc.status_sync_failed`, `kyc.scrub_failed`. Audit: `kyc.open`,
`kyc.decide`, `kyc.expire`.
