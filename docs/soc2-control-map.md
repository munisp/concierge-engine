# SOC 2 Type II — control mapping (Phase 3, Master Spec §14)

Type II is an audit of *operating effectiveness over time*, so this map links
each Trust Services Criteria (2017 TSC, CC series) to the artifact that
implements it and the evidence the auditor samples. Evidence lives in the
system itself — audit log, outbox, delivery records — not in spreadsheets.

## CC6 — Logical access

| Criterion | Implementation | Evidence for the auditor |
|---|---|---|
| CC6.1 access credentials | API keys: hashed secrets (`domain/apikeys.go`), prefix lookup + constant-time compare, scope enforcement in `tenantctx` | `apikey.created/revoked` audit events; `api_keys` table (hashes only) |
| CC6.1 authentication | OIDC JWT at APISIX edge (internal), `cek_` keys (public API), HMAC-signed chauffeur tokens (`domain/trips.go`) | middleware tests; config review |
| CC6.2/6.3 least privilege | Postgres RLS `FORCE ROW LEVEL SECURITY` on every tenant table; `tenant_isolation` policy; bilateral `referral_bilateral` | `db/migrations/*.sql`; `docs/rls-isolation-tests.md` |
| CC6.6 boundary protection | Webhook ingress verification: Stripe t/v1 HMAC + 300s tolerance, Meta X-Hub-Signature-256; missing secret = refuse everything | `domain/stripe.go`, `ai/whatsapp-adapter/main.py` tests |

## CC7 — System operations

| Criterion | Implementation | Evidence |
|---|---|---|
| CC7.1 monitoring | Outbox is the durable event spine; webhook delivery records expose attempt history | `webhook_deliveries` rows, `GET /v1/webhooks/deliveries` |
| CC7.2 incident handling | Absorbing saga states make replays safe (payouts, referral settle); backoff caps prevent retry storms | `domain/payouts.go`, `domain/referrals.go` tests |
| CC7.3 recovery | Idempotency keys on every money mutation; outbox replay rebuilds downstream state | `payment_intents`, `postings` unique idem keys |

## CC8 — Change management

| Criterion | Implementation | Evidence |
|---|---|---|
| CC8.1 change authorization | GitHub PRs + branch protection on `main` | PR history |
| CC8.1 change testing | CI gates: Go vet+test, cargo fmt/clippy/test, pytest (4 suites), console tsc+vite build, buf lint | `.github/workflows/ci.yml` run history |

## CC9 — Risk mitigation

| Criterion | Implementation | Evidence |
|---|---|---|
| CC9.2 vendor risk | All external adapters (Stripe, Meta, Amadeus, Mojaloop) sit behind ports with mock/sandbox degrade — vendor outage never wedges the core | `domain/gds.go`, `domain/stripe.go` fallback tests |

## A1 — Availability

| Criterion | Implementation | Evidence |
|---|---|---|
| A1.2 availability processing | Temporal payout saga: durable retries, TTL, compensation; signal fast path + poll backstop | `workers/temporal_worker`, time-skipping tests |

## C1 — Confidentiality

| Criterion | Implementation | Evidence |
|---|---|---|
| C1.1 data classification | VIP field-level encryption (`CE_FIELD_KEY`, AES-GCM envelope); sensitive-read audit log | `domain/crypto.go`, `audit_log` rows |
| C1.1 disposal | DSAR endpoint `DELETE /v1/clients/{id}/preferences` | handler test |

## P-series (privacy, GDPR/UAE PDPL)

| Criterion | Implementation | Evidence |
|---|---|---|
| Consent | `ConsentScope` on preferences; `AcceptsReferrals` opt-in before any cross-tenant data handoff | `client_preferences`, `tenants.accepts_referrals` |
| Residency | `Tenant.Region` (EU|UAE) validated at creation; residency statement endpoint | `GET /v1/tenant/residency` |

## Gap honesty

What an auditor will still require outside this repo: the policy documents
(joiner/mover/leaver process, incident-response plan), the observation
window (Type II covers 3–12 months of operation), and infrastructure
evidence from the cloud provider's own SOC reports. The platform artifacts
above are the control *implementation* layer — they make the audit a
sampling exercise rather than a build project.
