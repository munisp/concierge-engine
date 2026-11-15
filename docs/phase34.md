# Phase 34 — Taylor Travel Management reference demo

**Status: delivered.** Trigger: after mapping the platform's value against
taylortravelmanagement.com — a real VIP travel management group (chauffeur,
private jets, helicopters, yachts, close protection; Schiphol GAT HQ, Dubai
office) whose entire digital storefront is a brochure plus a "Get a Quote"
form with a one-hour email-response promise — the natural next step was a
seeded demo tenant on their actual business.

## What the demo shows

`scripts/seed-taylor.sh` provisions a flagship tenant shaped after Taylor's
public footprint (illustrative pricing — a real onboarding imports the
tenant's own catalog via the same calls):

- **9 service lines**, split the way the business actually works:
  - *Instant-quotable:* Schiphol airport transfer (base + per-km), hourly
    chauffeur (base + per-hour), VIP meet & greet at the GAT, intercity
    transfers — the quote engine prices these in seconds, no 1-hour email.
  - *On-request:* event/roadshow transport, helicopter, private jet, yacht
    charter, close protection — `auto_quotable: false`, honestly routed to
    a human quote instead of a fabricated number.
- **Tenant config:** EUR pricing rules with 30% deposit, NL VAT (21%,
  flat engine), dispatch windows, English default + German locale for the
  Germany hub, brand tagline and published Schiphol office line as the
  WhatsApp entry point.
- **Service areas:** Amsterdam & Schiphol, Dubai — the geo gate applies
  from the first booking.
- **Imagery:** 10 AI-generated editorial assets (hero + 9 service cards +
  6-image gallery strip) uploaded and wired through the media API — which
  flips the Phase 33 `service_imagery` onboarding step to done
  ("9 of 9 active services have a card image").

## Why it matters

This is the flagship-archetype pitch in one artifact: their service lines
map one-to-one onto the platform's verticals, their one-hour quote SLA
becomes an instant booking flow, and the parts of the business that can't
be priced automatically are honestly labelled on-request. The static
export (`GET /v1/studio/export`) doubles as a shareable preview: booking
CTAs rerouted to WhatsApp, embed iframe replaced with a static panel.

## Verification

Seed run against a local core-api (in-memory store): tenant, 9 services,
2 areas, 10 media uploads, hero/gallery wiring all green; site renders in
en + de; onboarding `service_imagery` step observed done on the seeded
tenant.
