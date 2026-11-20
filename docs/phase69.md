# Phase 69 — console aging page

Phase 67 built the derived receivables view (`GET /v1/folios/aging`);
Phase 69 makes it a first-class operator surface at `/aging`.

## What the operator sees

- **Per-currency summary cards** — total outstanding, total overdue,
  and 30+ day exposure. Currencies are never blended (same rule as the
  aging API and the channel margin analytics).
- **The bucket table** — one row per partner per currency: open folios,
  current / 1–7 / 8–30 / 30+ day buckets, total, and the worst-case
  age (`oldest_overdue_days`). Sorted most-overdue first by the API —
  the top row is the next collection call to make. Overdue cells are
  highlighted; in-terms rows stay muted.
- **"Run dunning sweep"** — triggers `POST /v1/dunning/run` on demand
  (same cadence as the hourly sweeper) and shows the receipt verbatim:
  `N sent · M skipped/failed`. The numbers come from the server; the
  page never assumes a send succeeded.

## Discipline

- Console-only phase: the API surface is unchanged, so every number is
  still computed server-side from live ISSUED folios on every load —
  there is no aging table to drift from the statements.
- Empty state is explicit ("Nothing outstanding") rather than a blank
  table.

## Files

- `web/console/src/pages/Aging.tsx` — the page.
- `web/console/src/App.tsx` — route + nav link.
- Verified by the console production build plus the Phase 67 server
  tests that pin the underlying endpoint.
