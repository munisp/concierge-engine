# Phase 27 — Tenant onboarding journey + brownfield integration + acquisition innovation pack

Three answers in one phase: how a tenant gets from signup to fully live,
how an existing business integrates instead of rebuilding, and ten concrete
channel plays that feed the Phase 26 acquisition machine (cohort tracker +
10% fee).

## The onboarding journey (derived, never stored)

`GET /v1/onboarding` returns the tenant's setup journey as a checklist whose
statuses are **computed from real resources on every read** — catalog count,
service areas, brand profile, payment rails, WhatsApp channel, operator
roster, promo campaigns, bookings, cohort state. A checklist that persists
its own progress drifts from reality the first time someone edits the
catalog directly; this one cannot. The only persisted state is the playbook
track and dismissed steps (`onboarding_config` jsonb, migration 0020):
`PUT /v1/onboarding` sets `track` (`new` | `existing`, defaulted from the
tenant archetype) and skips/reinstates steps.

Ten steps: brand profile → catalog → service areas → payments → WhatsApp →
operator roster → website (live by construction; the step is "know it
exists") → first campaign → first booking → 90-day acquisition goal. The
console `/setup` wizard renders the checklist with a progress bar, next-step
pointer, and one-click skip/reinstate.

## Two playbooks: new vs. existing

**New business** — the Phase 3 instant-on already seeds a starter catalog
and sandbox WhatsApp; the journey walks the operator through the rest.

**Existing business (brownfield)** — the same steps, but the actions are
import/connect rather than create:

- `POST /v1/import/catalog` — bulk service import, idempotent by name
  (case-insensitive), currency defaults to the tenant's pricing currency.
  Re-running a spreadsheet export is safe.
- `POST /v1/import/clients` — bulk client-book import, deduped by phone.
  Imported clients arrive with **marketing consent OFF** (consent cannot be
  imported, only collected), an `imported` marker, and first-touch channel
  `import` — deliberately NOT in the acquisition taxonomy, so the platform
  never takes its 10% on a book of business the tenant already had.
- `GET /embed/{slug}` — the embeddable booking widget for the website they
  already have (iframe-able, stripped chrome, `target=_top` CTAs stamped
  `channel=widget`). The wizard shows the paste-ready snippet.
- WhatsApp integration — `brand.whatsapp_number` activates click-to-chat
  deep links; the sandbox BSP channel covers the interim.

## The ten acquisition innovations

All ten terminate in the Phase 14 attribution spine, so the cohort tracker
and acquisition fee see them without new machinery (`qr`, `widget`, `gift`
join the default channel taxonomy):

1. **QR booking codes** — `GET /v1/marketing/qr?target=landing|svc:{id}|area:{id}`
   renders a printable PNG (go-qrcode) encoding a capture URL stamped
   `channel=qr`. Lobbies, vehicles, hotel desks, receipts.
2. **Link-in-bio page** — `GET /site/{slug}/links`: one mobile-first page
   with every booking CTA, for Instagram/TikTok/WhatsApp bios. Stamped
   `channel=social`; listed in the sitemap.
3. **WhatsApp click-to-chat** — `GET /v1/marketing/wa-links`: per-service
   `wa.me` deep links with prefilled intent text. Unconfigured tenants get
   the setup instruction, never a fabricated link.
4. **Post-trip reviews → rich snippets** — the track page (token-authed,
   no login) gains a review form on trip completion; `POST
   /v1/track/{token}/review` resolves the client anchor
   assignment→booking→request so a rating can't attach to someone else's
   trip. One review per assignment (structural). Only publish-consented
   reviews feed `aggregateRating` JSON-LD on the SEO landing — the stars
   Google renders are exactly the evidence held.
5. **Gift vouchers** — promos with `gift: true`: forced single-redemption
   credit an existing client hands to a non-client. Redemption stamps
   `gift:<code>` attribution; the recipient's first booking assesses the
   acquisition fee like any platform-sourced client.
6. **Referral boost windows** — `reward_program.boost_until` +
   `boost_multiplier`: time-boxed double-sided reward drives without
   touching the base program. Expired or malformed windows are inert.
7. **Dormant-lead nudges** — the lifecycle engine's third rule: a captured
   lead with zero bookings `lead_nudge_days` (default 7) after creation
   gets a marketing-lane activation nudge (consent-gated, one attempt per
   ISO week). Win-back owns lapsed customers; this owns never-converted
   leads.
8. **Embeddable booking widget** — see brownfield above; doubles as the
   acquisition channel for tenants whose existing site already has traffic.
9. **Switch-offer codes** — promos with `switch_offer: true`: competitor
   migration campaigns whose redemption fires `promo.switch_redeemed` and
   pings the operator roster — a high-intent signal worth a personal
   welcome within the hour.
10. **Partner co-marketing links** — `GET /v1/partners/{id}/acquisition`:
    the partner's trackable capture link (`channel=partner`,
    `campaign=partner:{id}`) plus their conversion funnel (leads, requests,
    bookings, attributed value) — the evidence for revenue-share
    conversations.

## Verification

8 domain tests (`phase20_test.go`: journey derivation + track/skip, catalog
& client imports with idempotency/consent/attribution, gift lifecycle →
acquisition fee, switch alert, boost window incl. expired/malformed, lead
nudge scoping + weekly idempotency, review consent gating + replay,
partner link + metrics), 4 server tests (onboarding endpoints + selective
patch survival, import endpoints, QR PNG bytes + WA links + partner link,
track review auth + /links + /embed + sitemap), 3 pg integration tests
(onboarding_config roundtrip/non-wipe, promo flags roundtrip, reviews RLS).
Full Go sweep + `go vet` (both build tags), console build, all four Python
suites green.
