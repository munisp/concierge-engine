# Phase 37 — Greenfield bootstrap: bulk lead import + console board/feed views

A tenant with zero customers no longer faces an empty pipeline. Phase 37 adds
a bulk import path that turns an existing spreadsheet or CRM export into a
scored, deduped, consent-aware lead board — plus console upgrades that make
the pipeline and the outbound channel visible at a glance.

## Design: import is a batch wrapper over Capture, never a parallel path

Every imported row goes through `Leads.Capture` unchanged:

- phone/email normalization and open-lead **dedup with backfill** (a row that
  matches an existing open lead merges into it, re-scored),
- deterministic **scoring** with the same reason trail,
- `ResolveV2` **messaging-identity link** — imported leads are immediately
  reachable by sequences and broadcasts through the linked client,
- **grant-only consent mirror** (`consent_marketing: true` in the file grants;
  nothing in an import ever revokes),
- per-row `lead_captured` funnel events with `source=import` (default) so the
  funnel separates bootstrapped demand from live traffic.

Rows are **advisory**: an invalid row (no contact anchor, corporate without a
company, event without an anchor) is rejected with its row index and a
human-readable reason — it never aborts the batch. Store failures abort.
Rows carrying an `IdempotencyKey` (e.g. `crm-export-2027-01:{row}`) make a
re-import **replay-safe**: keyed replays count as deduped, never duplicated.

Batch cap: **500 rows** per call (`MaxImportBatch`); bigger exports are
chunked client-side.

## API

```
POST /v1/leads/import
{ "leads": [ <CaptureLeadInput>, ... ] }
→ 200 { total, created, deduped, rejected, lead_ids[], errors[]? }
→ 422 empty batch / batch > 500
```

The receipt lists per-row reject reasons (Capture validation messages are now
descriptive, e.g. `validation: corporate leads require a company`) so the
operator fixes and re-pastes only the failing rows.

## Console

- **Leads → Board view (default)**: five status columns (NEW / CONTACTED /
  QUALIFIED / CONVERTED / LOST) with lead cards — score badge with the reason
  trail on hover, kind/company/source, transition and convert actions. The
  Phase 36 table remains one click away (Board | Table toggle).
- **Leads → Bulk import panel**: paste CSV (documented header) or a JSON
  array; the receipt renders created/deduped/rejected plus the first ten row
  errors.
- **Growth → Recent outreach sends**: phone-style bubble feed (default) with
  per-status accent and chips (sent / held / scheduled / failed), template and
  scheduling detail; the dense table remains via the Feed/Table toggle.

## Verification

- 4 domain tests: mixed batch receipt + funnel, keyed replay idempotency,
  batch limits (empty/over-cap/at-cap), scoring + segment membership.
- 2 server tests: endpoint receipt + provenance, 422 limits.
- Console: `npm run build` green.
