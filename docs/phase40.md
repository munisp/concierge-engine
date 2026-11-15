# Phase 40 — Campaign economics + email deliverability guardrail

Phase 38's loop line ends with *"fund the next event from evidence"* — but
until now the platform tracked only the evidence, not the money. The
attribution spine knew what a campaign earned; nothing knew what it cost,
so cost-per-conversion and ROAS were uncomputable and budget followed
instinct. And Phase 39 opened an email lane with consent, caps and
one-click unsubscribe — but no brake on a tenant burning its sender
reputation with a dirty list. Phase 40 closes both.

## What shipped

**Core-api (Go)**

- **Spend ledger** (`campaign_spend`, migration 0028): one row per spend
  entry — ad buy, list purchase, event sponsorship. `amount_minor` in
  minor units with ISO-4217 currency beside it, `spent_on` as the day the
  money was spent (backfill-friendly), idempotent by
  `(tenant_id, idempotency_key)` — a retried POST replays to the original
  row and never double-books cost. FORCE RLS, same tenant-isolation
  posture as every ledger before it.
- `CampaignService.RecordSpend` validates (campaign non-empty, amount > 0,
  ISO currency shape via the Phase 15 validator, `spent_on` YYYY-MM-DD,
  defaulting to today) and audit-logs every entry; `SpendList` serves one
  campaign or the whole tenant.
- **Funnel economics.** `CampaignFunnel` gains `spend_minor`,
  `attributed_value_minor` (booking + referral_converted value under the
  campaign name, from the same `Funnel()` aggregation the growth board
  already trusts), `cost_per_conversion_minor`, `roas`, `currency` — and
  `mixed_currency`. Mixed-currency spend sums the totals but **withholds
  the ratios**: naira plus dollars is not one money, and a blended "ROAS"
  across currencies would be a lie, not a metric.
- **Economics board** — `GET /v1/outreach/campaigns` unions every campaign
  the tenant ran (broadcast sends-ledger keys) or paid for (spend ledger)
  into one sorted board of full funnels. `POST/GET
  /v1/outreach/campaigns/{id}/spend` record and list entries; the path
  campaign is authoritative over the body.
- **Deliverability guard.** The email adapter now forwards ESP bounces and
  complaints to the attribution spine (`email_bounced` / `email_complained`
  kinds, channel `email`; complaints still flip marketing consent off per
  Phase 39). `AttributionStore.CountSince(tenant, kind, channel, since)`
  reads the trailing window (events carry `At` now — the spine's `at`
  column finally surfaces in the model). On a marketing email verdict,
  when the tenant's 7-day volume is ≥ 20 sends and (bounces + complaints)
  / sent > 5%, the send is **held** — "deliverability guard: 14.3% of 21
  emails bounced or complained in 7 days (max 5%) — clean the list before
  sending more". Service email is exempt (a safety message still goes),
  WhatsApp is untouched, and the guard is self-healing: the bad window
  ages out of the 7-day view with no operator intervention. The verdict
  exposes `email_sent_7d` and `email_bounce_rate` so the console composer
  shows reputation health live.
- **Denominator honesty.** The hourly sweep's delivered scheduled sends
  now record `outreach_sent` on the attribution spine exactly as
  interactive sends do — the guard's denominator counts every attempted
  delivery, not just the ones an operator clicked.

**Email adapter (Python)**

- `/webhooks/email/events`: `bounced` and `complained` also POST
  `/v1/attribution` (advisory, like every adapter call) — the guard's
  numerator arrives by the same path as every other acquisition signal.

**Console (Growth)**

- New **Campaign economics** card: spend entry form (campaign, channel,
  amount, currency, note) and the board table — sent, converted, spend,
  attributed value, cost-per-conversion, ROAS per campaign.
- The campaign funnel card shows spend → attributed → cost/conversion →
  ROAS when spend exists, and flags mixed currencies instead of blending
  them.
- The single-send composer gains the channel selector (WhatsApp | email,
  subject required on email) and the verdict line surfaces email
  bounce-rate health when the email lane is selected.

## Numbers

- Guard: minimum volume 20 sends / 7d, ceiling 5% bounce+complaint.
  Gmail's bulk-sender complaint threshold (0.3%) excludes bounces; a small
  sender's bounce-heavy list breaches 5% long before complaints register.
- 7 domain + 2 server + 2 pg + 13 adapter tests, all suites green.
