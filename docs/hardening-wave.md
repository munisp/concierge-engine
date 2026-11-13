# Integration hardening wave

Post-Phase-2 hardening: the three deferred bindings that Phase 2 explicitly
left as seams are now wired for real.

## 1. Temporal payout-saga binding (`workers/`)

The §11.3 payout saga now has a durable orchestrator. The domain layer keeps
money truth (idempotent postings, absorbing terminal states) — Temporal owns
retries, the callback-wait TTL, and compensation durability.

```
create payout (core-api, in-process)
  -> start PayoutSagaWorkflow (workflow_id = payout:{payout_id})
       1. quote_payout          FX quote via Mojaloop rail
       2. execute_payout        reserve + scheme transfer dispatch
       3. await terminal state  signal fast path + 30s poll backstop,
                                bounded by 17m TTL (adapter sweep + margin)
       4a. SETTLED              fulfil posting already applied by core-api
       4b. aborted/timeout      compensate_payout + HITL ops page
```

- `workers/temporal_worker/activities.py` — thin idempotent core-api calls;
  4xx domain rejections are non-retryable (`CoreAPIError`).
- `workers/temporal_worker/workflows.py` — the saga; compensation retries 10x
  and the domain absorb-guard dedupes replays.
- `workers/tests/` — 4 workflow tests on Temporal's time-skipping server:
  happy path, TTL expiry compensation, execute-failure compensation, and
  quote-domain-rejection without compensation.

Run: `make run-worker` (env: `CE_TEMPORAL_ADDRESS`, `CE_TEMPORAL_NAMESPACE`,
`CE_CORE_API_URL`).

## 2. Stripe live path (core-api, pure stdlib)

- `CE_STRIPE_KEY` set → `CreatePaymentLink` mints a real Checkout Session
  (form-encoded, `Idempotency-Key` = intent key, metadata carries
  tenant/quote/intent); hosted URL and session id stored on the intent.
- Unset → deterministic sandbox URL; dev and CI never touch the network.
- `POST /v1/payments/stripe/webhook` — `Stripe-Signature` verified against the
  raw body (HMAC-SHA256, `t,v1` scheme, ±5 min tolerance, constant-time).
  Tenant routing comes from the signed event metadata, never from headers;
  the route is exempt from tenant middleware for that reason.
- `checkout.session.completed` → existing `HandlePSPWebhook` → CAPTURED +
  split settlement postings. Unknown event kinds are acked, not processed.

## 3. Meta Cloud API direct path (whatsapp-adapter)

- `whatsapp_adapter/meta.py` — `MetaCloudAdapter` implements the same
  `ChannelPort` (text / template / interactive list) against the Graph API;
  `provider: "meta_direct"` in the tenant BSP config swaps it in.
- Webhook security: `verify_meta_signature` (X-Hub-Signature-256,
  HMAC-SHA256, constant-time; missing secret refuses everything), challenge
  handshake (`verify_challenge`), tenant routing from
  `metadata.phone_number_id` via `CE_META_TENANT_MAP` (fallback
  `CE_META_DEFAULT_TENANT`).

## Verification

| Suite | Result |
|---|---|
| go build + vet + test (core-api) | green — incl. 5 Stripe domain tests + signed webhook e2e |
| cargo fmt / clippy -D warnings / test | green (16 tests) |
| pytest concierge-agent | 16 passed |
| pytest whatsapp-adapter | 10 passed (incl. 6 Meta) |
| pytest analytics | 2 passed |
| pytest workers (time-skipping Temporal) | 4 passed |
| buf lint + build | clean |
