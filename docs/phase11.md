# Phase 11 — Receipts & SLA evidence, contacts intake, console geo page

Scope: delivery/read receipts from provider status callbacks recorded as
queryable SLA evidence, contacts/vCard intake on both WhatsApp channels,
and a console page for service-area CRUD + serviceability checks on top of
the Phase 6 geo routes. Also fixes two latent bugs found while building:
geo-check verdicts always degrading in production, and the `pg`-tagged
build never compiling.

## 1. Delivery/read receipts → SLA evidence

### Core (`services/core-api`)

- **Model** — `model.MessageReceipt` (`internal/model/model5.go`): id,
  tenant, channel, external_ref (provider message id), status, `at`
  (provider timestamp), created_at.
- **Store** — `ReceiptStore` (`internal/store/store.go`): `AddReceipt`
  idempotent per `(tenant, external_ref, status)` — provider status
  callbacks are at-least-once and frequently redelivered, so replays fold
  to a single row. `Receipts(ctx, tenant, externalRef)` returns the
  lifecycle of one message; `ReceiptCounts` returns per-status counts for
  a tenant (SLA dashboards). Memory (`memory5.go`) and Postgres
  (`postgres5.go`, `ON CONFLICT DO NOTHING`) implementations.
- **Migration** — `db/migrations/0007_message_receipts.sql`: status CHECK
  (`sent|delivered|read|failed`), `UNIQUE (tenant_id, external_ref,
  status)`, RLS FORCE tenant isolation, index on `(tenant_id,
  external_ref)`.
- **Domain** — `ReceiptsService` (`internal/domain/receipts.go`):
  validates status against the whitelist, defaults channel to
  `whatsapp`, defaults `at` to now when absent, emits a
  `channel.receipt` outbox event per accepted transition.
- **Routes** (`internal/server/phase7.go`):
  - `POST /v1/channels/receipts` — record one receipt (`at` accepts
    RFC3339 or epoch seconds).
  - `GET /v1/channels/receipts?external_ref=` — lifecycle of a message.
  - `GET /v1/channels/receipts-counts` — per-status counts (SLA board).
- **Outbound correlation** — `cases.SendReply` now decodes the WhatsApp
  adapter response body and captures `body.messages[0].id` into
  `ReplyResult.ExternalRef`. Every operator reply leaving the platform
  carries the provider message id, so inbound status callbacks join to
  outbound sends without a lookup table.

### Adapter (`ai/whatsapp-adapter`)

- **Meta statuses** — `meta_inbound` iterates `entry[].changes[].value
  .statuses[]` alongside messages: each status maps to
  `_record_receipt(tenant, "whatsapp", id, status, timestamp)` which POSTs
  to `/v1/channels/receipts`. Recording is advisory — core unreachable
  never breaks the webhook. Statuses and messages in the same payload are
  both processed.
- **Infobip status endpoint** — `POST /webhooks/whatsapp/infobip/status`:
  `_INFOBIP_STATUS_MAP` normalizes Infobip `status.groupName` →
  sent/delivered/read/failed (ACCEPTED/PENDING→sent, DELIVERED→delivered,
  SEEN/READ→read, REJECTED/UNDELIVERABLE/EXPIRED→failed). Unknown groups
  are acknowledged and skipped. Same tenant map as inbound
  (`CE_INFOBIP_TENANT_MAP` / `CE_INFOBIP_DEFAULT_TENANT`).

### SLA evidence flow

```
operator reply → SendReply → wamid captured (ExternalRef)
WhatsApp/Infobip → status callback → /v1/channels/receipts
SLA question → GET /v1/channels/receipts?external_ref=wamid...
             → sent/delivered/read timeline with provider timestamps
```

Redelivery-safe: replayed callbacks are idempotent, so the timeline is
exactly the set of distinct states reached.

## 2. Contacts / vCard intake

Both channels normalize shared contacts into intake text
`[contact shared: Name — phone]`:

- **Meta** — `type=contacts`, `contacts[0].name.formatted_name` +
  `contacts[0].phones[0].phone`.
- **Infobip** — `type=CONTACT`, `message.contact.firstName/lastName` +
  first phone number.

Flows through the same conversation/agent/case-intake path as text, so a
guest sharing a phone number (driver, restaurant, referrer) lands in the
case as structured text. Advisory label, idempotent via `wa:<message-id>`.

## 3. Console geo page

`web/console/src/pages/Geo.tsx` (routed at `/geo`, nav entry "Geo"):

- **Service areas table** — `GET /v1/geo/areas`, polygon size per area.
- **Create area** — name + textarea of `lat,lng` lines → `POST
  /v1/geo/areas`.
- **Serviceability tester** — lat/lng → `GET /v1/geo/check`, renders
  `serviceable — inside: <areas>` or `outside all service areas`.
- **Nearest partners** — lat/lng → `GET
  /v1/geo/nearest-partners?limit=5`, distance column (renders
  `unlocated` for partners without coordinates).

## 4. Latent bugs fixed in this phase

1. **Geo verdict always degrading in production** (`whatsapp_adapter/
   main.py`). Core `/v1/geo/check` returns `areas` as a list of *strings*;
   the adapter did `a.get("name")` per element, which throws on strings —
   caught by the advisory guard, so every real location message would have
   ended with "geo check unavailable". Phase 10 fakes returned dicts,
   masking it. Fix: accept both shapes (`a.get("name") if isinstance(a,
   dict) else str(a)`); the test fake now returns strings, matching core.
2. **`pg` build tag never compiled.** Three latent breaks surfaced the
   first time `go build -tags pg ./...` actually ran in this session:
   - `pgx/v5` was imported by the pg files but missing from `go.mod`
     (module fetch via goproxy.cn mirror + `go mod tidy`).
   - `platformTx` was defined on `pgOutbox` but called on `*postgres` —
     receiver moved.
   - `postgres` lacked the `Geo()` accessor required by the `Store`
     interface (`postgres5.go`).
   The pg build is now part of local verification; CI will gate it too.

## 5. Verification

| Suite | Result |
|---|---|
| Go gofmt / vet | clean |
| Go tests (all packages) | pass |
| `go build -tags pg ./...` | OK (first time ever) |
| whatsapp-adapter pytest | 52 passed (+5 new: receipts ×3 shapes, contacts ×2 channels, infobip status map) |
| concierge-agent pytest | 26 passed |
| analytics pytest | 13 passed, 1 skipped (Sedona, opt-in) |
| console `npm ci && npm run build` | pass |

New Go tests (`internal/domain/phase7_test.go`): record+query roundtrip,
3× redelivery idempotency, unknown status rejected, cross-tenant
isolation. `phase4_test.go` asserts `SendReply` captures
`wamid.reply-1` from the adapter response.

## 6. Deferred / not in this phase

- **Real WhatsApp/Infobip callback validation** — needs live provider
  accounts; the status-mapping tables are unit-tested against payload
  shapes from both vendors' docs.
- **SLA threshold alerting** — receipts are stored evidence; alerting on
  read-but-unanswered-after-N-minutes is a console/agent feature on top.
- **Geo page polygon map rendering** — textarea authoring is functional;
  a leaflet-style map editor is a UX upgrade, not a correctness gap.
