# Phase 2 — implementation notes

Scope per the Master Spec phased roadmap (§14) and the deferred-component
register: everything the register pins to **Phase 2** is now implemented.

## What landed

### 1. Mojaloop interop + cross-border partner payouts (Spec §11.3)

- **`crates/mojaloop-adapter`** (new Rust service, :8220) — FSPIOP-shaped
  subset: `POST /v1/quotes` (locks FX, receive amount, scheme fees),
  `POST /v1/transfers` (idempotent on key; deterministic `tr-` IDs via
  sha256), `POST /v1/callbacks` (scheme ingress + dev fulfilment simulator,
  idempotent on `event_id`), timeout sweeper that converts stale PENDING
  transfers to TIMED_OUT and notifies core-api. The adapter translates; it
  never decides.
- **`internal/domain/payouts.go`** — the payout saga in core-api:
  `PENDING → QUOTED → IN_FLIGHT → SETTLED | COMPENSATED`. Ledger movements:
  reserve (`PARTNER_PAYABLE → IN_FLIGHT_SETTLEMENT`), fulfil
  (`IN_FLIGHT_SETTLEMENT → PARTNER_CLEARING` + FX/fees remainder to
  `FX_GAIN_LOSS`), compensate (`IN_FLIGHT_SETTLEMENT → PARTNER_PAYABLE` +
  HITL ops page). Absorbing terminal states make callback replays no-ops.
  MANUAL rail skips the adapter for ops-settled corridors.
- New ledger codes: `PARTNER_CLEARING` (6100), `FX_GAIN_LOSS` (7000), in both
  the Rust ledger adapter and the payments proto.

### 2. Partner network (register: "partner-network routing")

- Partner directory with vetting lifecycle (`PENDING → VETTED ⇄ SUSPENDED`),
  verticals/markets, quality score, payout rail config.
- Partner fill-rate metrics endpoint (`GET /v1/partners/metrics`) — the
  north-star KPI from Spec §4.
- Dispatch assignments already carried `partner_id`; payouts reference
  partners, closing the loop: fulfill via partner → settle via saga.

### 3. Corporate accounts (register: "corporate accounts")

- Account CRUD with net-terms + credit limit; clients link via `org_id`.
- Monthly invoice rollup (`POST /v1/corporate/accounts/{id}/invoices`):
  completed bookings in the period for the account's clients, idempotent per
  (account, period) key. `mark-paid` settles the invoice into TigerBeetle
  with the same split settlement as card capture — money truth is
  rail-independent.

### 4. Avinode / charter supply (register: "Avinode, GDS")

- `CharterSupply` port in core-api: `AvinodeAdapter` (live search + empty
  legs, activated by `CE_AVINODE_BASE_URL`/`CE_AVINODE_API_KEY`) and a
  deterministic mock for dev/demo. Responses always mark `source`, so the
  factuality guardrail can never pass mock supply off as live.
- GDS (Amadeus NDC/hotel) follows the same port; not wired — no blocked
  itinerary has triggered it yet.

### 5. Lakehouse bronze + silver (Spec §12)

- `analytics/lakehouse/bronze.py` — polls the platform outbox feed, lands
  partitioned JSONL (`dt=/tenant=`), marks published (at-least-once).
- `analytics/lakehouse/silver.py` — batch KPIs: daily per-tenant GTV,
  instant-vs-human quotes, partner fill rate, payout outcomes. Dedupes on
  event id. Spark/Sedona/Ray/DataFusion stay deferred per register triggers;
  the bronze layout is already compatible.

### 6. AI concierge v1 → v2 (Spec §7)

- **PII redaction before inference** is now enforced in the Python agent
  itself (`redact.py`: Luhn-validated card redaction, email + long-number
  redaction, untrusted-content delimiters) — the Rust ingestion parser is no
  longer the only redaction point.
- **Shadow mode** (`CE_SHADOW_MODE`): new tenant configs run draft-only —
  mutations and sends are recorded as shadowed, read-only tools and
  escalation still run.
- **Judged evals** (`judge.py`): heuristic judge (offline stand-in for the
  LLM-judge panel, same interface) scoring tone/leakage/escalation; reports
  the V1 bars (policy compliance 100%, zero-touch ≥60%).

## Phase 1 gap closure (this pass)

- **Chauffeur app v1**: token-authed trip PWA (`/chauffeur?token=…`) —
  HMAC-signed magic links, privacy-minimized trip view (masked client name,
  no pricing), status advance via the same transition machine, one-tap
  geostamps.
- **SEO foundation**: static tenant×vertical landing pages
  (`scripts/build_seo_pages.py` → `web/console/public/seo/`), JSON-LD,
  sitemap, robots.
- **Access audit logs on sensitive reads** (Spec §13): `audit_log` table +
  service; client reads and preference reads are logged; `GET /v1/audit`.
- **Field-level encryption on VIP data** (Spec §13): AES-256-GCM envelope on
  preference values for VIP-flagged clients (`CE_FIELD_KEY`), prefix-detected
  decryption, fail-closed on misconfiguration.

## Deferred unchanged

Fluvio, OpenCTI, self-hosted open-weight models, GDS wiring — triggers in
`docs/deferred-register.md`. Temporal/Kafka remain adapter-bound (outbox +
events), not yet live-connected.
