# Phase 55 — Industry Packs & Vertical Campaigns

Marketing automation is now industry-aware: the platform can segment audiences by
service vertical (proven and declared) and run campaigns around curated
**industry packs** — named bundles of catalog services with their own landing
page, pitch copy, and one-click launch.

## What shipped

### 1. Vertical segment axes (`domain/segments.go`, `model/model8.go`)

`SegmentRules` gains two new combinable axes:

| Rule | Kind | Semantics |
|------|------|-----------|
| `vertical` | **proven** | client has a booking whose request contains an item with that vertical |
| `lead_vertical` | **declared** | client has an *open* lead (not CONVERTED/LOST) whose `Verticals` contains it |

`clientStats` gains `Verticals map[string]bool`, populated in
`clientBookingStats` from `ServiceRequest.Items[].Vertical` via a memoized
request fetch (one fetch per request, not per booking). Lead matching reuses the
existing lead index; lost/converted leads no longer qualify — honest recency.

Both axes combine with all existing rules (recency, spend, tier, engagement,
consent) through the same AND semantics.

### 2. Industry packs (`model/model.go`, `domain/studio.go`)

```go
type IndustryPack struct {
    Slug       string            `json:"slug"`
    Name       string            `json:"name"`
    ServiceIDs []string          `json:"service_ids"`
    Pitch      map[string]string `json:"pitch,omitempty"` // locale → copy
    PromoID    string            `json:"promo_id,omitempty"`
    Active     bool              `json:"active"`
}
```

Stored on `SiteConfig.Packs` (jsonb, **no migration**). Validation
(`ValidatePacks`, wired into `ValidateSiteConfig`):

- ≤ 12 packs, ≤ 12 services per pack, pitch ≤ 600 runes per locale
- slug: `^[a-z0-9][a-z0-9-]{0,59}$`, unique per tenant (duplicate → 422)

### 3. Pack landing pages (`server/phase55.go`, `server/site.go`)

`GET /site/{tenant}/packs/{slug}` renders a public landing page:

- pitch copy in the request locale (default-locale fallback), else joined
  service names — never fabricated marketing text
- service cards resolved against the **active catalog**; dangling IDs are
  dropped silently
- **resolvability honesty**: inactive pack → 404; zero resolvable services → 404
- CTA carries `utm_campaign=pack-{slug}` for attribution
- sitemap lists only resolvable packs; `packs` added to `siteReserved`

### 4. One-click launch (`POST /v1/packs/{slug}/launch`)

Builds a `BroadcastInput` with `Campaign: "pack-{slug}"` and a **vertical-derived
default segment**:

- single-vertical pack → `segment.vertical = <that vertical>` (proven affinity)
- multi-vertical pack → audience defaults to all reachable clients and the
  response carries an explicit notice:
  `"pack spans X, Y — audience defaulted to all reachable clients; pass a segment to narrow"`

Response: `{result, campaign, segment, notice, pack_url}`.

### 5. Console (`web/console/src/pages/Growth.tsx`)

`IndustryPacks` section on the Growth page: create packs (name → slugified,
service checkboxes from the live catalog, EN pitch), activate/deactivate,
delete, and **Launch** — body + channel, result card shows campaign, matched,
sent, held, and the multi-vertical notice when present. Landing-page link per
pack.

## Tests

- `domain/phase55_test.go` — vertical affinity segment, lead-vertical segment
  (LOST lead excluded), pack validation: **3/3 pass**
- `server/phase55_test.go` — pack page 200 + copy/UTM assertions, inactive →
  404, dangling-only → 404, sitemap gating; launch single-vertical segment,
  multi-vertical notice, unknown pack 404: **2/2 pass**
- Console `tsc -b && vite build`: clean
