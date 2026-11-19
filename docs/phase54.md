# Phase 54 — Marketer Content Blocks (Mautic feature-borrow A3)

## Context

Third and final gap from the Mautic assessment: marketer-editable
content. Mautic lets marketers drop reusable content blocks into emails
and landing pages without a deploy. The concierge-engine site is
server-rendered from real data with engine-owned copy (locale dicts +
generated brand text); this phase opens four curated copy slots to
marketers — without handing over the page structure (the Phase 32 rule:
curated beats freeform until demand proves otherwise).

## What was built

### Model (no migration)

`site_config.content_blocks`: block key → locale → plain text. Rides the
existing site_config jsonb; both write paths (`PUT /v1/tenant` wholesale
patch and any future targeted write) run `ValidateSiteConfig`, which now
validates blocks too.

### Closed vocabulary (domain/studio.go)

Four slots, each a deliberate marketing surface:

- `hero_tagline` — the hero paragraph. **Display only**: meta
  description, OG and JSON-LD stay engine-derived, so SEO/snippet copy
  can never diverge from the real brand data (a Mautic page has no such
  guarantee — here it is structural).
- `cta_label` — the primary landing CTA button.
- `footer_note` — the closing line on every page.
- `announcement` — a slim top bar; empty = the bar is not in the DOM at
  all (no empty-banner artifacts).

Validation: closed key vocabulary, BCP-47 locale keys, plain text only
(`<`/`>` rejected — the template escapes anyway and literal markup would
confuse), 280-rune cap, empty values dropped (clearing a block restores
the engine copy). Unknown/invalid → 422, nothing persists.

### Render (server/site.go + site_tmpl.go)

`contentBlock(t, locale, key)` resolves page locale → default locale →
engine copy. The hero paragraph binds a new `HeroTag` field (defaults to
`Description` in `renderSite`, so service/area/partner pages are
untouched); landing overrides HeroTag/CTALabel/FooterTagline/Announcement
from blocks. The editorial layout family renders the same DOM, so blocks
apply identically under both designs (tested).

### Console (Studio page)

New "Content blocks" card: one labeled row per slot per configured
locale, local edit buffer with an explicit save (no save-per-keystroke),
wholesale site_config patch through the existing Studio save path.
Clearing a field restores engine copy — the card says so.

## Honesty rules

1. Structure stays engine-owned: four slots, plain text, no HTML.
2. SEO surfaces (meta/OG/JSON-LD) stay engine-derived — tested.
3. Engine copy is the fallback, not a dead end: empty block = the
   original copy returns.
4. Everything renders through the real template — the Studio static
   export picks blocks up with no extra work.

## Tests

Server (2): invalid blocks (unknown slot / markup / oversized / bad
locale) → 422 and never render; valid blocks render on the landing
(hero, CTA, footer, announcement bar), og:description stays
engine-derived, de-only block does not leak onto the en landing, cleared
blocks restore engine copy and remove the announcement bar from the
DOM; editorial design renders blocks identically.

## Deferred

- Reusable named snippets referenced from multiple surfaces (Mautic's
  full block library) — the four curated slots cover the current
  marketing surface; demand-driven.
- Agent-drafted block suggestions in Studio generate — the draft flow
  (Phase 30) already produces tagline/description; wiring it to blocks
  is a follow-up.
