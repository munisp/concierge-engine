# Phase 29 — Multi-locale site engine, premium redesign, AI concierge surfacing

Phase 28 made the public site rank; Phase 29 makes it *convert* — in the
customer's language, with a top-player look, and with the AI concierge one
tap away on every page. Reference tenant: **DDPortugal** (ddportugal.com),
a Lisbon luxury tours/transport operator whose Portuguese-only,
contact-form site is the exact brownfield profile this phase fixes.

## Multi-locale engine

- `site_config.locales` — ordered content locales, **first = default**.
  The default locale keeps the canonical unprefixed URLs
  (`/site/{slug}/book/{id}`); every additional locale gets its own tree at
  `/site/{slug}/{locale}/...`. Falls back to `site_config.locale`, then `en`.
- **Unconfigured locale prefixes 404** — no duplicate-content pages, ever.
- **hreflang cluster** on every page: one `alternate` per configured locale
  plus `x-default` → the default-locale URL. Canonicals are per-locale.
- **Content resolution order:** per-resource i18n maps
  (`catalog_services.name_i18n/desc_i18n`, `service_areas.name_i18n`,
  `site_config.brand_i18n` tagline/description) → canonical names →
  localized vertical labels. JSONB presentation overrides only — the
  canonical `name` column stays the default-locale truth (migration 0022).
- **Chrome dictionaries** (buttons, section headings, trust chips, footer)
  ship fully translated: en, pt-PT, de, fr, es.
- **Guide + FAQ prose** ships fully translated in **en and pt-PT**.
  de/fr/es render localized chrome with English body prose — an honest
  degrade: we do not machine-translate marketing copy the tenant never
  reviewed. Tenants add full prose for any locale via the i18n maps.
- Sitemap lists every configured locale tree; Search Console verification,
  custom domains, ETag/304 caching and OG cards all work per locale.
- OG card renderer is ASCII-bitmap-based; card text is passed through
  `asciiFold` (Fátima → Fatima). Page HTML is full UTF-8 — the fold applies
  only to the card image.
- Console Settings → *Public site & SEO* gains a content-locales field
  (comma-separated, first = default). `brand_i18n` is preserved across
  console saves; per-resource translations are managed via the API.

Routing note: Go's ServeMux cannot express `/site/{slug}/{locale}/book/{id}`
alongside `/site/{slug}/book/{id}` (wildcard/literal segment overlap), so
the whole `/site/{slug}/` subtree dispatches through one handler that
resolves the optional locale prefix manually.

## Premium redesign (top-player benchmark)

Rebuilt the public template against the Viator / GetYourGuide / Blacklane
pattern — pure SSR + CSS, no external assets, no JS dependency:

- **Hero**: full-bleed dark band, brand wordmark, tagline, review rating
  chip, dual CTA row (primary *Book* + ghost *Chat on WhatsApp*).
- **Tour card grid**: monogram band, localized name, two-line description,
  "from €X" price and a *Book now →* link per card; responsive 2→1 columns.
- **Trust bar** with *conditional* claims only: "Secure online payment"
  renders only when a payment rail is enabled, "WhatsApp confirmation"
  only when a WhatsApp number is configured, star rating only from
  consented reviews. Nothing is claimed that the tenant can't do.
- **Sticky mobile CTA bar** (book + chat) below 900px — where tour bookings
  actually happen.
- **Footer** with partner-directory link and localized tagline.

Honest limit vs. the top players: no photo galleries until tenants supply
licensed imagery (the platform will not invent or scrape photos), and no
fake review counts — ratings render only from consented first-party
reviews.

## AI concierge in the inquiry path

Every page now carries a WhatsApp chat CTA (header ghost button + sticky
mobile bar) built from `brand.whatsapp_number`, plus the booking CTA into
the capture flow. Behind that button is the full agent stack:

1. **Channels** — WhatsApp (whatsapp-adapter), web capture chat, voice
   (voice-adapter). All land in the same intake.
2. **Intake** — idempotent per-channel keys; a retried WhatsApp delivery
   never creates a duplicate inquiry.
3. **Agent turns** (`/v1/turns`) — policy-gated tools with tiers, per-turn
   budget caps, and shadow mode for new tenants.
4. **RAG** — similar-case recall over past resolved inquiries (Phase 4b
   embeddings) so repeat questions ("how much is Sintra for 6 people?")
   answer from precedent.
5. **Deterministic rule floor** — safety/recovery rules always run; the LLM
   can never skip them.
6. **Human handoff** — operator-approved case replies (HUMAN_STAFF) when the
   agent is out of policy or confidence.
7. **Receipts** — WhatsApp advisory sends carry wamid receipts; the SLA
   evidence loop records response times against the tenant's SLA config.

Multilingual: replies follow the client's locale, matching the site trees
this phase adds — a German tourist landing on `/site/ddportugal/en/` and
tapping WhatsApp gets answered in English by the same pipeline.

## DDPortugal tie-in

`scripts/seed-ddportugal.sh` seeds the reference tenant end to end:
bilingual site config (pt-PT default + en, `ddportugal.com` custom
domains), 10 catalog entries (Lisboa/Sintra/Fátima/Évora/Porto tours,
airport transfers, event transport) with pt-PT/en names and descriptions,
and 5 service areas with localized names. Names/durations/prices are
**illustrative demo data** shaped after DDPortugal's public catalogue — a
real onboarding imports the tenant's own catalog through the same calls.

Against their current site (Portuguese-only, contact-form funnel,
TripAdvisor-owned reviews), this delivers: English search capture (the
majority of their tourist demand), instant booking + payment instead of
email ping-pong, first-party review equity, per-channel attribution, and an
AI concierge that answers inquiries in the customer's language 24/7.
