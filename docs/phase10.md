# Phase 10 — location messages → geo serviceability, Infobip inbound parity

User-directed wave: the last two WhatsApp intake gaps — dropped pins and
the BSP (Infobip) inbound path.

## (a) Location messages → geo service check

A dropped pin is one of the most common concierge inputs ("pick me up
here"). It now flows:

- `WebhookMessage` carries `latitude`/`longitude`/`location_name`/
  `location_address`; the Meta webhook routes `location` messages.
- `inbound()` folds the pin into the message text —
  `[location shared: Eko Hotel, Adetokunbo Ademola St (6.42810,3.42190)]` —
  then runs an **advisory** serviceability check against the Phase 6 geo
  service (`GET /v1/geo/check`) after identity resolution, and appends the
  verdict before anything is recorded:
  `→ serviceable: yes (Victoria Island)` / `serviceable: no`.
- Geo failure never invents a verdict: the text goes in without a
  serviceability claim and the response reports `geo.serviceable: null`.
- Conversation history, agent turn, and case intake all see the same text —
  triage can now reason about location (a pin outside every service area
  is a different conversation than one inside).

## (b) Infobip inbound parity

The BSP path previously had outbound only — inbound required a platform-
normalized webhook. New `POST /webhooks/whatsapp/infobip` normalizes
Infobip's `{"results": [...]}` format into the same `WebhookMessage`
pipeline:

- TEXT → text; IMAGE/VIDEO/AUDIO/DOCUMENT → `caption` + `url`; LOCATION →
  coordinates + name/address.
- Media fetches are now provider-agnostic: `fetch_media` dispatches —
  http(s) refs download directly (with `Authorization: App
  CE_INFOBIP_API_KEY`, mime read from the response Content-Type), Meta ids
  keep the two-step node flow. All Phase 7–9 extraction (OCR /
  transcription / video keyframes) works unchanged for Infobip media.
- Tenant resolution mirrors the Meta path: receiver number →
  `CE_INFOBIP_TENANT_MAP`, else `CE_INFOBIP_DEFAULT_TENANT`; no tenant →
  skipped honestly.
- Same `wa:<messageId>` idempotency downstream.

## Verification

- whatsapp-adapter: 47/47 (12 new: 4 location, 8 Infobip/fetch)
- concierge-agent 26/26, analytics 13+1 opt-in, Go, console: untouched,
  last verified green
