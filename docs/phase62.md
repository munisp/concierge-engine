# Phase 62 — Chauffeur Matching

Phases 58–61 personalized the ride and orchestrated the trip. Phase 62
personalizes **who drives**: dispatch stops being first-free-slot and
becomes a scored, explainable decision built only from signals the
platform holds honestly.

## 1. Signals, all earned

| Signal | Points | Source |
|---|---|---|
| Rider's favorite chauffeur | +100 | explicit mark (track page / console) |
| Speaks the rider's language | +30 | `comms.language` ∩ crew `languages` (BCP-47) |
| Conversation-style fit | +20 | `ride.conversation` ∩ crew `styles` (quiet/light/chatty) |
| Prior trips together | +10 each, cap +30 | completed-trip history (assignments→bookings→requests) |
| Quality | +0..10 | crew `quality_score` normalized |
| **Blocked** | **excluded** | hard filter — never outranked, never suggested |

Familiarity is capped so it can never outweigh an explicit mark. The
blocked list is a hard exclusion in `SuggestCrew`; an operator who
manually assigns a blocked chauffeur gets the decision recorded as
`score −1, "manual override"` — the trail stays honest even then.

## 2. The decision is recorded, not implied

Every crew assignment carries `match_score` + `match_reasons`
(migration 0041, human-readable strings — "speaks fr", "2 prior trips
together", "rider's favorite chauffeur"). A manual pick with no signals
records exactly that. The dispatch board shows the explanation under
each assignment.

## 3. Where marks come from

- **Track page (post-trip)** — "Ride together again" / "Don't match us
  again" toggle; the token proves the trip, the assignment names the
  chauffeur, the mark lands on the rider's profile.
- **Client 360** — console lists the rider's marks (names resolved
  against the crew roster) and can clear them.

`chauffeur_links` (migration 0041, RLS): one row per
(tenant, client, crew member), `kind ∈ {favorite, blocked}`.

## 4. Crew capabilities

`crew_members.languages` / `.styles` (jsonb) declare what a chauffeur
offers; the crew form in Dispatch captures them. Matching reads the
rider's **validated** preference vocabulary (Phase 58) — the same
enums, no new free-text.

## 5. Endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/bookings/{id}/crew-suggestions` | ranked, explained candidates |
| POST | `/v1/bookings/{id}/assign-matched` | assign the top match |
| GET/PUT/DELETE | `/v1/clients/{id}/chauffeur-links[/{crewID}]` | console link management |
| PUT | `/v1/track/{token}/chauffeur` | rider self-serve mark |

`AssignMatched` with no available candidates is a 422 with an honest
explanation — never a silent random pick. Crew-only assignments (no
fleet resource) are now legal.

## 6. Honesty ledger

- No ML, no inferred affinity: every point traces to a declared
  preference, an explicit mark, or a completed trip.
- Quality score is a weak signal (max 10 of 180+ possible) — it breaks
  ties, it doesn't make decisions.
- The match explanation renders on the dispatch board verbatim.
- pg integration asserts the join count excludes other crews and
  respects RLS cross-tenant.
