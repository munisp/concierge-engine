# Phase 33 — Imagery honesty: onboarding coverage, delete-scrub, render defense

**Status: delivered.** Trigger: "Proceed with all of the gaps" — the gaps
left after Phase 32 in the onboarding/catalog/media triangle: onboarding
had no per-service image step, deleting media left dangling references
that rendered as broken images on the public site, and legacy rows had no
render-time protection at all.

## 1. `service_imagery` onboarding step (22 steps, was 21)

The Phase 32 journey tracked site imagery (hero) but not the per-service
card images that make the catalog — and the quote/booking pages — feel
real. New derived step, grouped under `presence`, action `/catalog`:

```go
step("service_imagery", "presence", "Service card images",
    activeSvc > 0 && svcWithImg == activeSvc, svcImgDetail, "/catalog")
```

- Status is derived, never persisted: `done` iff every ACTIVE service
  carries a card image. Inactive services don't block it — retiring a
  service must not re-open onboarding.
- Detail reports honest coverage ("1 of 2 active services have a card
  image.") and, on an empty catalog, guides to services first instead of
  showing a vacuous 0-of-0.
- Skip/unskip behave like every other step: skip bites only while the
  step is pending; a derived "done" always wins.

## 2. Media delete scrubs every reference first

Before Phase 33, `DELETE /v1/media/{id}` removed the bytes and left every
reference behind — catalog `image_id`s, the site hero, gallery entries —
and the public site served `<img>` tags that 404'd. `deleteMedia` now
scrubs before it deletes:

```go
if err := h.scrubMediaRefs(ctx, tenantID, id); err != nil { ... }  // refs first
if err := h.svc.Store.Media().Delete(ctx, tenantID, id); err != nil { ... }
```

Ordering matters: if the scrub fails, media and references stay
consistent and the operator can retry; deleting first would dangle.

The scrub reaches **inactive** services too, which required a new store
method — `CatalogStore.ListAll` (memory + Postgres, both sorted by name,
tenant-scoped). `List` still filters on `active` because it feeds the
quote engine and the public site; the scrub can't afford that filter.

The console's Studio media library simplifies to match: delete is now
just `del + reload` — the server clears the references, and the reload
re-reads the tenant with them already gone (no more client-side patch +
extra wholesale site save).

## 3. Render-time defense for legacy rows

The scrub fixes deletes going forward, but rows written before it existed
(or via paths that never validated media existence — `PUT /v1/tenant`
validates shape, not media) could still dangle. Every public-site media
reference now resolves through:

```go
func (h *handlers) liveMediaURL(ctx context.Context, tenantID, rawBase, id string) string
```

A dangling id resolves to "" — the hero falls back to the theme gradient,
gallery entries drop out, service cards fall back to the monogram — and
the id never appears in the HTML. Write-side validation (Phase 31: bogus
catalog image ids are 422s) stays as the first line of defense.

## 4. Console polish

- **Catalog**: active services without a card image carry a "no card
  image" badge — the `service_imagery` gap surfaces exactly where it's
  fixed.
- **Studio**: media delete simplified (see §2).

## Tests

4 server + 1 pg:

- `TestServiceImageryStep33` — empty catalog (guidance detail, not
  0-of-0) → 2 services pending at "0 of 2" → skip/unskip while pending →
  "1 of 2" → full coverage done → inactive service doesn't block.
- `TestMediaDeleteScrub33` — image wired as service card (active +
  inactive), hero, and gallery; delete scrubs all four references; the
  landing HTML no longer mentions the id; re-delete 404s.
- `TestDanglingMediaRender33` — dangling hero/gallery ids render nothing
  and never appear in the HTML; a live id mixed with a dangling one still
  renders.
- `TestPGCatalogListAll33` (pg) — ListAll returns active + inactive,
  tenant-scoped, name-sorted; List still returns active only.
