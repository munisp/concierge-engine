# Tenant WhatsApp onboarding runbook (WhatsApp spec §4)

Elapsed 3–10 days, dominated by Meta business verification; runs in parallel
with platform onboarding.

1. **Meta Business Manager** — tenant creates/links business portfolio;
   business verification (legal entity documents). Platform provides the
   guided checklist.
2. **WABA** — created under the tenant portfolio via BSP embedded signup
   (OAuth click-through; no credential handover).
3. **Phone number** — dedicated per tenant brand (new or migrated with
   off-peak porting window); display name submitted for Meta review.
4. **Platform connection** — WABA ID + phone ID (or BSP callback tokens) in
   tenant admin; adapter validates and runs a two-way test.
5. **Template library** — platform seeds brand templates: booking confirmed,
   chauffeur assigned, car 5-minutes-away, delay re-plan, quote follow-up,
   review request. Custom templates tracked to approval in admin.
6. **Opt-in wiring** — checkboxes on web/SEO forms, Instagram link-in-bio,
   scripted verbal opt-in for phone bookings; GDPR-grade consent records in CRM.
7. **Quality warm-up** — messaging limits ramp with quality rating; adapter
   enforces ramp plans and alerts ops on quality signals.
8. **Go-live checks** — end-to-end script in staging then production:
   register -> quote -> pay -> fulfill -> notify.

Acceptance (v1): new client from WhatsApp click to paid booking < 5 min without
human touch (instant-quote shapes); quote SLA instrumented end-to-end;
>=95% proactive notifications via approved templates; handoff briefs < 5 s.
