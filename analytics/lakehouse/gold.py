"""Gold layer (Phase 3, Master Spec §14 — "ML pricing/churn/LTV").

v0 is deliberately heuristic, deterministic and explainable — every output
carries the rule that produced it, so pricing/churn decisions remain
auditable (§13) and the AI autonomy dial contract (raise only on eval
evidence) is respected. Trained models replace these rules only after the
eval harness demonstrates lift; the interface stays identical.

Consumes: lakehouse/bronze (raw events), lakehouse/silver/daily_kpis.json
Produces: lakehouse/gold/client_ltv.json, churn_risk.json,
          pricing_suggestions.json
"""

from __future__ import annotations

import json
import statistics
from collections import defaultdict
from datetime import date, datetime
from pathlib import Path
from typing import Any

from .silver import _load_bronze


def _ts(e: dict[str, Any]) -> str:
    return str(e.get("created_at", ""))


def _client_events(events: list[dict[str, Any]]) -> dict[str, list[dict[str, Any]]]:
    """booking.confirmed + payment.captured grouped by client."""
    by_client: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for e in events:
        payload = e.get("payload") or {}
        cid = payload.get("client_id")
        if cid and e.get("event_type") in ("booking.confirmed", "payment.captured"):
            by_client[f"{e.get('tenant_id', 'platform')}|{cid}"].append(e)
    for v in by_client.values():
        v.sort(key=_ts)
    return by_client


def build_client_ltv(root: Path) -> dict[str, Any]:
    """LTV per client = observed revenue + expected repeat revenue.

    expected = avg_booking_value * repeat_probability * horizon_bookings,
    where repeat_probability is the fraction of the tenant's clients with
    >1 booking (network-wide cold start: per-tenant, falling back to 0.25
    for single-booking-only tenants). Rule labels make every number
    traceable.
    """
    events = _load_bronze(root)
    by_client = _client_events(events)
    per_tenant_counts: dict[str, list[int]] = defaultdict(list)
    for key, evs in by_client.items():
        tenant = key.split("|", 1)[0]
        n = sum(1 for e in evs if e.get("event_type") == "booking.confirmed")
        per_tenant_counts[tenant].append(n)
    repeat_prob = {
        t: (sum(1 for n in ns if n > 1) / len(ns)) if ns else 0.0
        for t, ns in per_tenant_counts.items()
    }

    rows = []
    for key, evs in sorted(by_client.items()):
        tenant, cid = key.split("|", 1)
        revenue = sum(
            int((e.get("payload") or {}).get("amount_minor", 0))
            for e in evs if e.get("event_type") == "payment.captured"
        )
        bookings = sum(1 for e in evs if e.get("event_type") == "booking.confirmed")
        avg = revenue // bookings if bookings else revenue
        rp = repeat_prob.get(tenant, 0.25) or 0.25
        expected = int(avg * rp * 4)  # 4-booking horizon
        rows.append({
            "tenant_id": tenant, "client_id": cid,
            "bookings": bookings, "revenue_minor": revenue,
            "avg_booking_minor": avg, "repeat_probability": round(rp, 3),
            "ltv_minor": revenue + expected,
            "rule": "observed_revenue + avg*repeat_prob*4",
        })
    return {"grain": "tenant|client", "rows": rows}


def build_churn_risk(root: Path, today: date | None = None) -> dict[str, Any]:
    """Churn risk from booking recency vs the client's own cadence.

    risk = min(1, days_since_last / (2 * median_gap_days)); a client with a
    single booking is scored against a 45-day default gap. Deterministic and
    explainable; the risk tier drives concierge outreach playbooks.
    """
    today = today or date.today()
    by_client = _client_events(_load_bronze(root))
    rows = []
    for key, evs in sorted(by_client.items()):
        tenant, cid = key.split("|", 1)
        days = []
        for e in evs:
            if e.get("event_type") != "booking.confirmed":
                continue
            try:
                days.append(datetime.fromisoformat(_ts(e).replace("Z", "+00:00")).date())
            except ValueError:
                continue
        if not days:
            continue
        gaps = [max((b - a).days, 1) for a, b in zip(days, days[1:])]
        typical = statistics.median(gaps) if gaps else 45
        since = max((today - days[-1]).days, 0)
        risk = min(1.0, since / (2 * typical))
        tier = "high" if risk >= 0.75 else "medium" if risk >= 0.4 else "low"
        rows.append({
            "tenant_id": tenant, "client_id": cid,
            "days_since_last_booking": since, "typical_gap_days": typical,
            "churn_risk": round(risk, 3), "tier": tier,
            "rule": "min(1, days_since / (2*median_gap))",
        })
    return {"grain": "tenant|client", "rows": rows}


def build_pricing_suggestions(root: Path) -> dict[str, Any]:
    """Pricing suggestions from silver daily KPIs — bounded nudges only.

    Rules (all ±5% max per cycle, never self-executing — a human or the
    autonomy-dial-gated agent applies them):
      * instant_share >= 0.8 and partner_fill >= 0.5  -> demand healthy: +5%
      * instant_share <= 0.2                          -> catalog not
        auto-quotable enough: review catalog, no price move
      * partner_fill <= 0.2                           -> supply thin:
        raise partner margin, not client price
    """
    silver_path = root / "silver" / "daily_kpis.json"
    if not silver_path.exists():
        return {"grain": "tenant", "rows": []}
    silver = json.loads(silver_path.read_text())

    agg: dict[str, dict[str, Any]] = defaultdict(lambda: {
        "instant": 0, "human": 0, "partner": 0, "fleet": 0, "days": 0,
    })
    for r in silver.get("rows", []):
        a = agg[r["tenant_id"]]
        a["instant"] += r["quotes_instant"]
        a["human"] += r["quotes_human"]
        a["partner"] += r["assignments_partner"]
        a["fleet"] += r["assignments_fleet"]
        a["days"] += 1

    rows = []
    for tenant, a in sorted(agg.items()):
        quotes = a["instant"] + a["human"]
        assigns = a["partner"] + a["fleet"]
        instant_share = a["instant"] / quotes if quotes else 0.0
        fill = a["partner"] / assigns if assigns else 0.0
        if quotes >= 10 and instant_share >= 0.8 and fill >= 0.5:
            rows.append({"tenant_id": tenant, "action": "raise_price", "pct": 5,
                         "rule": "instant_share>=0.8 & partner_fill>=0.5",
                         "instant_share": round(instant_share, 3), "partner_fill": round(fill, 3)})
        elif quotes >= 10 and instant_share <= 0.2:
            rows.append({"tenant_id": tenant, "action": "review_catalog", "pct": 0,
                         "rule": "instant_share<=0.2",
                         "instant_share": round(instant_share, 3), "partner_fill": round(fill, 3)})
        elif assigns >= 10 and fill <= 0.2:
            rows.append({"tenant_id": tenant, "action": "raise_partner_margin", "pct": 5,
                         "rule": "partner_fill<=0.2",
                         "instant_share": round(instant_share, 3), "partner_fill": round(fill, 3)})
    return {"grain": "tenant", "rows": rows}


def write_gold(root: Path) -> list[Path]:
    out = root / "gold"
    out.mkdir(parents=True, exist_ok=True)
    paths = []
    for name, data in (
        ("client_ltv", build_client_ltv(root)),
        ("churn_risk", build_churn_risk(root)),
        ("pricing_suggestions", build_pricing_suggestions(root)),
    ):
        p = out / f"{name}.json"
        p.write_text(json.dumps(data, indent=2))
        paths.append(p)
    return paths
