# Phase 12 — SLA alerting on receipts, receipts console board, pg CI gate

Scope: receipts stop being passive evidence and start alerting (a failed
delivery on an approved reply is an SLA emergency, not a log line), the
console gets a receipts/SLA board, and the `pg` store now runs against a
real Postgres+PostGIS in CI — which immediately caught a class of latent
schema bugs.

## 1. Receipt → case correlation

New store surface (`ReceiptStore`): `LinkReceipt` (idempotent, first link
wins), `LinkedCase`, `ReceiptsByStatus`, `RefsForCase`. Migration
`0008_receipt_links.sql` (`UNIQUE (tenant_id, external_ref)`, RLS FORCE,
`ON CONFLICT DO NOTHING`). Memory + Postgres implementations.

`SendReply` now links the provider `wamid` it captures to the case
(advisory — a link failure never blocks a send) and includes the ref in
the audit/event payloads.

## 2. SLA alerting on receipt timelines

`ReceiptsService.Record` → `alertLinkedCase` after every stored receipt:

- **`failed`** — the approved reply never arrived while the SLA clock
  runs: case note with the evidence (`DELIVERY FAILED for approved reply
  (ref …)`), case pulled out of `WAITING_CLIENT` back to `IN_PROGRESS`
  (you cannot wait on a client who never received the message), case
  escalated, `case.reply_failed` event.
- **`read`** — proof the client saw the reply: case note with timestamp,
  `case.reply_read` event. Evidence, not escalation.
- `sent`/`delivered` — stored evidence only, no note spam.
- Unlinked refs (template sends) — stored, never touch a case.

Advisory discipline: alerting failures never reject the receipt — the
evidence is stored first.

New query surfaces: `Receipts.Failures` (recent failures correlated to
cases) and `Receipts.ForCase` (full delivery evidence per case). Routes:
`GET /v1/channels/receipts-failures?limit=`, `GET
/v1/channels/receipts-case?case_id=`.

## 3. Console receipts & SLA board

`/receipts` page: per-status count cards + delivery rate, failed-deliveries
table (newest first, linked to the case, "unlinked — template send" when
there is none), message-id lifecycle lookup, and per-case delivery
evidence. Nav entry "Receipts & SLA".

## 4. pg-backed integration tests in CI (the bug-catching part)

New CI job `pg`: `postgis/postgis:16-3.4` service, migrations applied as
superuser via psql, tests run as a **non-superuser `app` role** — RLS is
transparent to superusers, so testing as one would prove nothing.
`internal/store/pg_integration_test.go` (build tag `pg`, gated on
`CE_PG_TEST_DSN`; `CE_PG_APPLY_MIGRATIONS=1` for self-contained local runs
against a superuser DSN):

- receipts roundtrip + 3× redelivery idempotency
- links: first-wins, `LinkedCase`, `RefsForCase`, failures ordering
- cross-tenant isolation **enforced by RLS** (receipts, counts, links)
- geo areas WKT roundtrip through real PostGIS
- case create/read with NULL linked ids

The `go` job also gained `go build -tags pg ./...` — the pg tag broke
silently once; it is now gated twice.

### Latent bugs this exposed and fixed

1. **Migration 0007 was not applicable to a real database** — `tenant_id
   TEXT REFERENCES tenants(id)` against a `uuid` column; Postgres rejects
   the FK at DDL time. Fixed 0007 (uuid columns) before it ever ran.
2. **RLS policies in 0007/0008 would error at query time** — `uuid =
   current_setting(...)` has no operator; every other migration casts
   (`nullif(current_setting(...),'')::uuid`). Both policies fixed to the
   house style.
3. **`pgCases.Create`/`Update` would fail for any case without a client**
   — `""` strings cast into uuid columns. `nilIfEmpty` on all nullable FK
   columns; `nilIfZero` for nullable timestamps (`0001-01-01` is out of
   timestamptz range).
4. **`scanCase` made every such case unreadable** — NULL columns scanned
   into plain `string`/`time.Time`. Now `sql.NullString`/`sql.NullTime`.

None of these were reachable before Phase 11 fixed the pg build — the
integration job exists precisely because "it compiles" is not "it works".

## 5. Verification

| Suite | Result |
|---|---|
| Go gofmt / vet (default + `-tags pg`) / tests | clean |
| `go build -tags pg ./...` | OK (now CI-gated) |
| domain tests | pass (+5 Phase 12) |
| pg integration tests | compile; skip without DSN; run in CI job `pg` |
| whatsapp-adapter pytest | 52 passed |
| console build | pass (+Receipts page) |

## 6. Deferred / not in this phase

- **Adapter-facing alert push** — alerts are events + case notes; pushing
  "reply failed" into the operator's WhatsApp/console notifications is a
  dispatch concern on top of the event bus.
- **SLA board metrics** (median time-to-read per channel) — analytics
  gold-layer material, not core-api.
- **pg store full CRUD audit** — the integration job covers the Phase
  6/11/12 paths (geo, receipts, links, cases); the remaining Phase 1–3
  store surfaces get coverage as their domains are touched.
